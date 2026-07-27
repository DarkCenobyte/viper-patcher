package patch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

const maxInspectionWorkers = 16

type inspectedApplicationFile struct {
	missing       bool
	hasIssue      bool
	issue         FileIssue
	sourceMatches bool
	targetMatches bool
}

// Inspect validates files in root against both source and target states.
func Inspect(rootPath string, parsed patchformat.Patch) (ValidationResult, error) {
	return InspectContext(context.Background(), rootPath, parsed)
}

// InspectContext validates files in root and stops promptly when ctx is canceled.
func InspectContext(ctx context.Context, rootPath string, parsed patchformat.Patch) (result ValidationResult, resultError error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	root, err := openInstallationRoot(rootPath)
	if err != nil {
		return ValidationResult{}, err
	}
	defer func() {
		if closeError := root.Close(); closeError != nil {
			resultError = errors.Join(resultError, fmt.Errorf("close target root: %w", closeError))
		}
	}()

	workerBudget := runtime.NumCPU()
	if workerBudget > maxInspectionWorkers {
		workerBudget = maxInspectionWorkers
	}
	if workerBudget < 1 {
		workerBudget = 1
	}
	fileWorkers, hashWorkers := workerAllocation(workerBudget, len(parsed.Header.Files))
	inspected := make([]inspectedApplicationFile, len(parsed.Header.Files))
	if err := parallelFor(ctx, len(parsed.Header.Files), fileWorkers, func(ctx context.Context, index int) error {
		entryResult, err := inspectApplicationFile(ctx, root, parsed.Header.Files[index], hashWorkers)
		if err != nil {
			return err
		}
		inspected[index] = entryResult
		return nil
	}); err != nil {
		return ValidationResult{}, err
	}

	result = ValidationResult{
		CanApplyForward: true,
		CanApplyReverse: parsed.Header.Reverse,
		Missing:         make([]string, 0),
		Issues:          make([]FileIssue, 0),
	}
	matchedSource := false
	matchedTarget := false
	for index, entryResult := range inspected {
		entry := parsed.Header.Files[index]
		if entryResult.missing {
			result.Missing = append(result.Missing, entry.Path)
			result.CanApplyForward = false
			result.CanApplyReverse = false
			continue
		}
		if entryResult.hasIssue {
			result.Issues = append(result.Issues, entryResult.issue)
			result.CanApplyForward = false
			result.CanApplyReverse = false
			continue
		}
		if !entryResult.sourceMatches {
			result.CanApplyForward = false
		}
		if !entryResult.targetMatches {
			result.CanApplyReverse = false
		}
		matchedSource = matchedSource || entryResult.sourceMatches
		matchedTarget = matchedTarget || entryResult.targetMatches
		if !entryResult.sourceMatches && !entryResult.targetMatches {
			result.Issues = append(result.Issues, FileIssue{Path: entry.Path, Reason: IssueHashMismatch})
		}
	}

	return finalizeValidationResult(result, parsed.Header.Reverse, matchedSource, matchedTarget), nil
}

func inspectApplicationFile(ctx context.Context, root *installationRoot, entry patchformat.FileEntry, hashWorkers int) (inspectedApplicationFile, error) {
	file, identity, localized, err := root.openStableRegularFile(entry.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return inspectedApplicationFile{missing: true}, nil
		}
		if localized == "" {
			localized, _ = localPatchPath(entry.Path)
		}
		if localized != "" {
			if info, lstatErr := root.root.Lstat(localized); lstatErr == nil && !info.Mode().IsRegular() {
				return inspectedApplicationFile{hasIssue: true, issue: FileIssue{Path: entry.Path, Reason: IssueNotRegular}}, nil
			}
		}
		return inspectedApplicationFile{}, fmt.Errorf("inspect %q: %w", entry.Path, err)
	}

	digest, size, hashError := hashutil.FileParallel(ctx, file, uint64(identity.Size()), hashWorkers, nil)
	currentInfo, statError := file.Stat()
	pathInfo, pathError := root.root.Lstat(localized)
	closeError := file.Close()
	if hashError != nil {
		return inspectedApplicationFile{}, fmt.Errorf("hash %q: %w", entry.Path, hashError)
	}
	if statError != nil {
		return inspectedApplicationFile{}, fmt.Errorf("inspect %q after hashing: %w", entry.Path, statError)
	}
	if pathError != nil {
		return inspectedApplicationFile{}, fmt.Errorf("inspect %q after hashing: %w", entry.Path, pathError)
	}
	if closeError != nil {
		return inspectedApplicationFile{}, fmt.Errorf("close %q after inspection: %w", entry.Path, closeError)
	}
	if !currentInfo.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !osSameFile(identity, currentInfo, pathInfo) ||
		currentInfo.Size() < 0 || uint64(currentInfo.Size()) != size || !currentInfo.ModTime().Equal(identity.ModTime()) {
		return inspectedApplicationFile{}, fmt.Errorf("file %q changed while it was being inspected", entry.Path)
	}

	return inspectedApplicationFile{
		sourceMatches: digest == entry.SourceHash && size == entry.SourceSize,
		targetMatches: digest == entry.TargetHash && size == entry.TargetSize,
	}, nil
}

func finalizeValidationResult(result ValidationResult, reverseAvailable, matchedSource, matchedTarget bool) ValidationResult {
	if !reverseAvailable {
		result.CanApplyReverse = false
	}
	switch {
	case len(result.Missing) > 0:
		result.State = StateMissingFiles
	case result.CanApplyForward && result.CanApplyReverse:
		result.State = StateBidirectionalReady
	case result.CanApplyForward:
		result.State = StateForwardReady
	case result.CanApplyReverse:
		result.State = StateReverseReady
	case len(result.Issues) == 0 && matchedSource && matchedTarget:
		result.State = StateMixedFiles
	default:
		result.State = StateInvalidFiles
	}
	return result
}

func osSameFile(infos ...fs.FileInfo) bool {
	if len(infos) < 2 {
		return true
	}
	for _, info := range infos[1:] {
		if !os.SameFile(infos[0], info) {
			return false
		}
	}
	return true
}
