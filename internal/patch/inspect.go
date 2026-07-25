package patch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
)

// Inspect validates files in root against both source and target states.
func Inspect(root string, parsed patchformat.Patch) (ValidationResult, error) {
	result := ValidationResult{
		CanApplyForward: true,
		CanApplyReverse: parsed.Header.Reverse,
		Missing:         make([]string, 0),
		Issues:          make([]FileIssue, 0),
	}
	mixedState := false
	matchedSource := false
	matchedTarget := false

	for _, entry := range parsed.Header.Files {
		path, err := pathutil.SecureJoinExisting(root, entry.Path)
		if err != nil {
			return ValidationResult{}, err
		}
		file, info, err := openStableRegularFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				result.Missing = append(result.Missing, entry.Path)
				result.CanApplyForward = false
				result.CanApplyReverse = false
				continue
			}
			if lstatInfo, lstatErr := os.Lstat(path); lstatErr == nil && !lstatInfo.Mode().IsRegular() {
				result.Issues = append(result.Issues, FileIssue{Path: entry.Path, Reason: IssueNotRegular})
				result.CanApplyForward = false
				result.CanApplyReverse = false
				continue
			}
			return ValidationResult{}, fmt.Errorf("inspect %q: %w", entry.Path, err)
		}
		digest, size, hashErr := hashutil.Reader(file)
		closeErr := file.Close()
		if hashErr != nil {
			return ValidationResult{}, fmt.Errorf("hash %q: %w", entry.Path, hashErr)
		}
		if closeErr != nil {
			return ValidationResult{}, fmt.Errorf("close %q after inspection: %w", entry.Path, closeErr)
		}
		mode := uint32(info.Mode().Perm())
		sourceHashMatches := digest == entry.SourceHash && size == entry.SourceSize
		targetHashMatches := digest == entry.TargetHash && size == entry.TargetSize
		sourceMatches := sourceHashMatches && mode == entry.SourceMode
		targetMatches := targetHashMatches && mode == entry.TargetMode

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
		reason := IssueHashMismatch
		if sourceHashMatches || targetHashMatches {
			reason = IssueModeMismatch
		}
		result.Issues = append(result.Issues, FileIssue{
			Path:       entry.Path,
			Reason:     reason,
			ActualHash: digest,
			ActualMode: mode,
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
	mixedState = len(result.Issues) == 0 && matchedSource && matchedTarget
	if mixedState {
		result.State = StateMixedFiles
		return result, nil
	}
	result.State = StateInvalidFiles
	return result, nil
}
