package patch

import (
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
)

// Inspect validates files in root against source and, when available, target hashes.
func Inspect(root string, parsed patchformat.Patch) (ValidationResult, error) {
	forwardMatches := 0
	reverseMatches := 0
	missing := make([]string, 0)
	mismatched := make([]HashMismatch, 0)

	for _, entry := range parsed.Header.Files {
		path, err := pathutil.SecureJoinExisting(root, entry.Path)
		if err != nil {
			return ValidationResult{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, entry.Path)
				continue
			}
			return ValidationResult{}, fmt.Errorf("inspect %q: %w", entry.Path, err)
		}
		if !info.Mode().IsRegular() {
			missing = append(missing, entry.Path)
			continue
		}
		digest, _, err := hashutil.File(path)
		if err != nil {
			return ValidationResult{}, err
		}
		switch digest {
		case entry.SourceHash:
			forwardMatches++
		case entry.TargetHash:
			reverseMatches++
		default:
			mismatched = append(mismatched, HashMismatch{Path: entry.Path, Actual: digest})
		}
	}

	if len(missing) > 0 {
		return ValidationResult{State: StateMissingFiles, Missing: missing, Mismatched: mismatched}, nil
	}
	if len(mismatched) == 0 && forwardMatches == len(parsed.Header.Files) {
		return ValidationResult{State: StateForwardReady}, nil
	}
	if parsed.Header.Reverse && len(mismatched) == 0 && reverseMatches == len(parsed.Header.Files) {
		return ValidationResult{State: StateReverseReady}, nil
	}
	if len(mismatched) == 0 {
		// A mixed source/target state is still invalid relative to either complete state.
		for _, entry := range parsed.Header.Files {
			mismatched = append(mismatched, HashMismatch{Path: entry.Path, Actual: "mixed source/target state"})
		}
	}
	return ValidationResult{State: StateHashMismatch, Mismatched: mismatched}, nil
}
