package patch

import (
	"fmt"
	"os"
)

// CreateEstimate is a conservative estimate of peak temporary disk usage.
type CreateEstimate struct {
	SnapshotBytes        uint64
	DifferentialBytes    uint64
	PatchBytes           uint64
	ExistingOutputBytes  uint64
	WorkDirectoryBytes   uint64
	OutputDirectoryBytes uint64
	TotalBytes           uint64
}

// EstimateCreate calculates the temporary disk space that patch creation may
// require without changing any source, target, or output file.
func EstimateCreate(options CreateOptions) (CreateEstimate, error) {
	plan, err := createPlanFromOptions(options)
	if err != nil {
		return CreateEstimate{}, err
	}
	var estimate CreateEstimate
	var headerBytes uint64 = 4096 + uint64(len(options.Comment))
	for _, pair := range plan.pairs {
		sourceSize, err := regularFileSize(pair.sourcePath)
		if err != nil {
			return CreateEstimate{}, err
		}
		targetSize, err := regularFileSize(pair.targetPath)
		if err != nil {
			return CreateEstimate{}, err
		}
		if estimate.SnapshotBytes, err = addEstimate(estimate.SnapshotBytes, sourceSize, targetSize); err != nil {
			return CreateEstimate{}, err
		}
		forwardBound, err := differentialWorkBound(targetSize)
		if err != nil {
			return CreateEstimate{}, err
		}
		if estimate.DifferentialBytes, err = addEstimate(estimate.DifferentialBytes, forwardBound); err != nil {
			return CreateEstimate{}, err
		}
		if options.CreateReverse {
			reverseBound, err := differentialWorkBound(sourceSize)
			if err != nil {
				return CreateEstimate{}, err
			}
			if estimate.DifferentialBytes, err = addEstimate(estimate.DifferentialBytes, reverseBound); err != nil {
				return CreateEstimate{}, err
			}
		}
		if headerBytes, err = addEstimate(headerBytes, uint64(len(pair.relativePath)), 1280); err != nil {
			return CreateEstimate{}, err
		}
	}
	if info, err := os.Lstat(plan.outputPath); err == nil {
		if info.Size() < 0 {
			return CreateEstimate{}, fmt.Errorf("existing output has an invalid size")
		}
		estimate.ExistingOutputBytes = uint64(info.Size())
	} else if !os.IsNotExist(err) {
		return CreateEstimate{}, fmt.Errorf("inspect existing output: %w", err)
	}

	estimate.PatchBytes, err = addEstimate(headerBytes, estimate.DifferentialBytes)
	if err != nil {
		return CreateEstimate{}, err
	}
	estimate.WorkDirectoryBytes, err = addEstimate(estimate.SnapshotBytes, estimate.DifferentialBytes)
	if err != nil {
		return CreateEstimate{}, err
	}
	estimate.OutputDirectoryBytes, err = addEstimate(estimate.PatchBytes, estimate.ExistingOutputBytes)
	if err != nil {
		return CreateEstimate{}, err
	}
	estimate.TotalBytes, err = addEstimate(estimate.WorkDirectoryBytes, estimate.OutputDirectoryBytes)
	if err != nil {
		return CreateEstimate{}, err
	}
	return estimate, nil
}

func differentialWorkBound(targetSize uint64) (uint64, error) {
	compressed, err := compressionBoundEstimate(targetSize)
	if err != nil {
		return 0, err
	}
	// Format 3 may temporarily hold one uncompressed sparse or COPY/ADD stream
	// while its compressed payload is produced. Twice the target size plus one
	// MiB safely covers operation metadata even for adversarial tiny records.
	instructionRaw, err := addEstimate(targetSize, targetSize, 1<<20)
	if err != nil {
		return 0, err
	}
	return addEstimate(instructionRaw, compressed)
}

func compressionBoundEstimate(size uint64) (uint64, error) {
	return addEstimate(size, size/128, 1<<20)
}

func addEstimate(values ...uint64) (uint64, error) {
	var total uint64
	for _, value := range values {
		var ok bool
		total, ok = checkedAdd(total, value)
		if !ok {
			return 0, fmt.Errorf("temporary disk-space estimate overflows")
		}
	}
	return total, nil
}
