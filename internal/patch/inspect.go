package patch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func Inspect(root string, parsed patchformat.Patch) (ValidationResult, error) {
	return InspectContext(context.Background(), root, parsed)
}
func InspectContext(ctx context.Context, root string, parsed patchformat.Patch) (ValidationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	installation, err := openInstallationRoot(root)
	if err != nil {
		return ValidationResult{}, err
	}
	defer installation.Close()
	results := make([]struct {
		missing, issue, source, target bool
		item                           FileIssue
	}, len(parsed.Header.Files))
	err = parallelFor(ctx, len(results), min(effectiveWorkers(0), 16), func(ctx context.Context, index int) error {
		entry := parsed.Header.Files[index]
		file, identity, targetName, err := installation.openStableRegularFile(entry.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				results[index].missing = true
				return nil
			}
			if localized, localErr := localPatchPath(entry.Path); localErr == nil {
				if info, lerr := installation.root.Lstat(localized); lerr == nil && !info.Mode().IsRegular() {
					results[index].issue = true
					results[index].item = FileIssue{entry.Path, IssueNotRegular}
					return nil
				}
			}
			return err
		}
		defer file.Close()
		if identity.Size() < 0 {
			return fmt.Errorf("invalid file size")
		}
		session, err := nativev4.NewSession(file, nil, nil)
		if err != nil {
			return err
		}
		rootDigest, _, err := session.HashFileTree(ctx, false, uint64(identity.Size()), patchformat.IdentityChunkSize)
		session.Close()
		if err != nil {
			return err
		}
		if err = stableRootUnchanged(installation, file, targetName, identity); err != nil {
			return err
		}
		results[index].source = rootDigest == entry.SourceDigest && uint64(identity.Size()) == entry.SourceSize
		results[index].target = rootDigest == entry.TargetDigest && uint64(identity.Size()) == entry.TargetSize
		if !results[index].source && !results[index].target {
			results[index].issue = true
			results[index].item = FileIssue{entry.Path, IssueHashMismatch}
		}
		return nil
	})
	if err != nil {
		return ValidationResult{}, err
	}
	result := ValidationResult{CanApplyForward: true, CanApplyReverse: parsed.Header.Reverse}
	sourceSeen, targetSeen := false, false
	for index, item := range results {
		if item.missing {
			result.Missing = append(result.Missing, parsed.Header.Files[index].Path)
			result.CanApplyForward = false
			result.CanApplyReverse = false
			continue
		}
		if item.issue {
			result.Issues = append(result.Issues, item.item)
		}
		if !item.source {
			result.CanApplyForward = false
		}
		if !item.target {
			result.CanApplyReverse = false
		}
		sourceSeen = sourceSeen || item.source
		targetSeen = targetSeen || item.target
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
	case sourceSeen && targetSeen && len(result.Issues) == 0:
		result.State = StateMixedFiles
	default:
		result.State = StateInvalidFiles
	}
	return result, nil
}
