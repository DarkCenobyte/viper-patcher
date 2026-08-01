package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

const (
	fineDigestWireBytes    = uint64(8 + len(patchformat.Digest{}))
	maxPortableFineDigests = 1 << 19
)

type fineVerificationPlan struct {
	ChunkSize      uint32
	Indexes        []uint64
	CanonicalBytes uint64
	FineBytes      uint64
}

func retainPortableFineVerification(entry *patchformat.FileEntry, total *int) {
	count := len(entry.SourceFineChunks) + len(entry.TargetFineChunks)
	if count == 0 {
		return
	}
	if total == nil || *total > maxPortableFineDigests-count {
		entry.SourceFineChunkSize = 0
		entry.TargetFineChunkSize = 0
		entry.SourceFineChunks = nil
		entry.TargetFineChunks = nil
		return
	}
	*total += count
}

func fineVerificationCandidates(mode OptimizationMode) []uint32 {
	switch mode {
	case OptimizeApplySpeed:
		return []uint32{64 << 10, 256 << 10, 1 << 20}
	case OptimizePatchSize:
		return []uint32{1 << 20}
	default:
		return []uint32{256 << 10, 1 << 20}
	}
}

func fineVerificationTablePenalty(mode OptimizationMode) uint64 {
	switch mode {
	case OptimizeApplySpeed:
		return 1
	case OptimizePatchSize:
		return 16
	default:
		return 4
	}
}

func maxFineVerificationTableBytes() uint64 {
	if strconvIntSizeRuntime() == 32 {
		return 4 << 20
	}
	return 16 << 20
}

func windowNeedsFineVerification(window patchformat.WindowDescriptor) bool {
	return window.Kind == patchformat.WindowDeltaRaw ||
		window.Kind == patchformat.WindowDeltaZstd
}

func addBandRange(
	indexes map[uint64]struct{},
	offset uint64,
	size uint32,
	chunkSize uint64,
	maximum int,
) bool {
	if size == 0 {
		return true
	}
	if offset > ^uint64(0)-(uint64(size)-1) {
		return false
	}
	first := offset / chunkSize
	last := (offset + uint64(size) - 1) / chunkSize
	for index := first; ; index++ {
		indexes[index] = struct{}{}
		if len(indexes) > maximum {
			return false
		}
		if index == last {
			break
		}
	}
	return true
}

func bandBytes(fileSize, index, chunkSize uint64) uint64 {
	offset := index * chunkSize
	if offset >= fileSize {
		return 0
	}
	remaining := fileSize - offset
	if remaining < chunkSize {
		return remaining
	}
	return chunkSize
}

func collectReferencedBands(
	fileSize uint64,
	windows []patchformat.WindowDescriptor,
	chunkSize uint32,
	maximum int,
) ([]uint64, uint64, bool) {
	indexes := make(map[uint64]struct{})
	for _, window := range windows {
		if !windowNeedsFineVerification(window) {
			continue
		}
		if !addBandRange(indexes, window.SourceOffset, window.SourceSize,
			uint64(chunkSize), maximum) {
			return nil, 0, false
		}
	}
	if len(indexes) == 0 {
		return nil, 0, true
	}
	result := make([]uint64, 0, len(indexes))
	var bytes uint64
	for index := range indexes {
		result = append(result, index)
		bytes += bandBytes(fileSize, index, uint64(chunkSize))
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result, bytes, true
}

func planFineVerification(
	fileSize uint64,
	windows []patchformat.WindowDescriptor,
	mode OptimizationMode,
) fineVerificationPlan {
	if fileSize == 0 {
		return fineVerificationPlan{}
	}
	canonicalIndexes, canonicalBytes, ok := collectReferencedBands(
		fileSize, windows, uint32(patchformat.IdentityChunkSize),
		patchformat.MaxFineDigestsPerFile,
	)
	if !ok || len(canonicalIndexes) == 0 {
		return fineVerificationPlan{}
	}

	penalty := fineVerificationTablePenalty(mode)
	bestScore := canonicalBytes
	var best fineVerificationPlan
	for _, chunkSize := range fineVerificationCandidates(mode) {
		maximum := int(maxFineVerificationTableBytes() / fineDigestWireBytes)
		if maximum > patchformat.MaxFineDigestsPerFile {
			maximum = patchformat.MaxFineDigestsPerFile
		}
		indexes, fineBytes, complete := collectReferencedBands(
			fileSize, windows, chunkSize, maximum,
		)
		if !complete || len(indexes) == 0 {
			continue
		}
		tableBytes := uint64(len(indexes)) * fineDigestWireBytes
		if tableBytes > maxFineVerificationTableBytes() {
			continue
		}
		if tableBytes > (^uint64(0)-fineBytes)/penalty {
			continue
		}
		score := fineBytes + tableBytes*penalty
		if score >= bestScore {
			continue
		}
		// Avoid changing the format for microscopic wins. The table must save
		// at least 256 KiB and at least twice its own encoded size.
		savings := canonicalBytes - fineBytes
		if savings < 256<<10 || savings < tableBytes*2 {
			continue
		}
		bestScore = score
		best = fineVerificationPlan{
			ChunkSize:      chunkSize,
			Indexes:        indexes,
			CanonicalBytes: canonicalBytes,
			FineBytes:      fineBytes,
		}
	}
	return best
}

func buildFineVerification(
	ctx context.Context,
	file *os.File,
	fileSize uint64,
	windows []patchformat.WindowDescriptor,
	mode OptimizationMode,
) (uint32, []patchformat.FineDigest, error) {
	plan := planFineVerification(fileSize, windows, mode)
	if plan.ChunkSize == 0 || len(plan.Indexes) == 0 {
		return 0, nil, nil
	}
	buffer := make([]byte, int(plan.ChunkSize))
	result := make([]patchformat.FineDigest, len(plan.Indexes))
	for position, index := range plan.Indexes {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		offset := index * uint64(plan.ChunkSize)
		length := bandBytes(fileSize, index, uint64(plan.ChunkSize))
		if length == 0 || length > uint64(len(buffer)) || offset > uint64(^uint64(0)>>1) {
			return 0, nil, fmt.Errorf("invalid fine verification band %d", index)
		}
		read, err := file.ReadAt(buffer[:int(length)], int64(offset))
		if err != nil && !(errors.Is(err, io.EOF) && read == int(length)) {
			return 0, nil, fmt.Errorf("read fine verification band %d: %w", index, err)
		}
		if read != int(length) {
			return 0, nil, io.ErrUnexpectedEOF
		}
		digest, err := nativev4.HashBytes(buffer[:int(length)])
		if err != nil {
			return 0, nil, err
		}
		result[position] = patchformat.FineDigest{Index: index, Digest: digest}
	}
	return plan.ChunkSize, result, nil
}
