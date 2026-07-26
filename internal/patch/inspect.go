package patch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

// Inspect validates files in root against both source and target states.
func Inspect(rootPath string, parsed patchformat.Patch) (result ValidationResult, resultError error) {
	root, err := openInstallationRoot(rootPath)
	if err != nil {
		return ValidationResult{}, err
	}
	defer func() {
		if closeError := root.Close(); closeError != nil {
			resultError = errors.Join(resultError, fmt.Errorf("close target root: %w", closeError))
		}
	}()

	result = ValidationResult{
		CanApplyForward: true,
		CanApplyReverse: parsed.Header.Reverse,
		Missing:         make([]string, 0),
		Issues:          make([]FileIssue, 0),
	}
	matchedSource := false
	matchedTarget := false

	for _, entry := range parsed.Header.Files {
		file, info, localized, err := root.openStableRegularFile(entry.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				result.Missing = append(result.Missing, entry.Path)
				result.CanApplyForward = false
				result.CanApplyReverse = false
				continue
			}
			if localized == "" {
				localized, _ = localPatchPath(entry.Path)
			}
			if localized != "" {
				if lstatInfo, lstatErr := root.root.Lstat(localized); lstatErr == nil && !lstatInfo.Mode().IsRegular() {
					result.Issues = append(result.Issues, FileIssue{Path: entry.Path, Reason: IssueNotRegular})
					result.CanApplyForward = false
					result.CanApplyReverse = false
					continue
				}
			}
			return ValidationResult{}, fmt.Errorf("inspect %q: %w", entry.Path, err)
		}
		digest, size, hashErr := hashutil.Reader(file)
		currentInfo, statErr := file.Stat()
		pathInfo, pathErr := root.root.Lstat(localized)
		closeErr := file.Close()
		if hashErr != nil {
			return ValidationResult{}, fmt.Errorf("hash %q: %w", entry.Path, hashErr)
		}
		if statErr != nil {
			return ValidationResult{}, fmt.Errorf("inspect %q after hashing: %w", entry.Path, statErr)
		}
		if pathErr != nil {
			return ValidationResult{}, fmt.Errorf("inspect %q after hashing: %w", entry.Path, pathErr)
		}
		if closeErr != nil {
			return ValidationResult{}, fmt.Errorf("close %q after inspection: %w", entry.Path, closeErr)
		}
		if !currentInfo.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !osSameFile(info, currentInfo, pathInfo) ||
			currentInfo.Size() < 0 || uint64(currentInfo.Size()) != size || !currentInfo.ModTime().Equal(info.ModTime()) {
			return ValidationResult{}, fmt.Errorf("file %q changed while it was being inspected", entry.Path)
		}

		sourceMatches := digest == entry.SourceHash && size == entry.SourceSize
		targetMatches := digest == entry.TargetHash && size == entry.TargetSize

		if !sourceMatches {
			result.CanApplyForward = false
		}
		if !targetMatches {
			result.CanApplyReverse = false
		}
		matchedSource = matchedSource || sourceMatches
		matchedTarget = matchedTarget || targetMatches

		if sourceMatches || targetMatches {
			continue
		}
		result.Issues = append(result.Issues, FileIssue{
			Path:       entry.Path,
			Reason:     IssueHashMismatch,
			ActualHash: digest,
		})
	}

	if !parsed.Header.Reverse {
		result.CanApplyReverse = false
	}
	if len(result.Missing) > 0 {
		result.State = StateMissingFiles
		return result, nil
	}
	if result.CanApplyForward && result.CanApplyReverse {
		result.State = StateBidirectionalReady
		return result, nil
	}
	if result.CanApplyForward {
		result.State = StateForwardReady
		return result, nil
	}
	if result.CanApplyReverse {
		result.State = StateReverseReady
		return result, nil
	}
	if len(result.Issues) == 0 && matchedSource && matchedTarget {
		result.State = StateMixedFiles
		return result, nil
	}
	result.State = StateInvalidFiles
	return result, nil
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
