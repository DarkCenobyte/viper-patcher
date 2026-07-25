package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

const portablePermissionMask uint32 = 0o777

type applyOperations struct {
	chmod func(string, os.FileMode) error
}

var defaultApplyOperations = applyOperations{chmod: os.Chmod}

// ApplyOptions configures one patch application operation.
type ApplyOptions struct {
	PatchPath         string
	Root              string
	Direction         Direction
	ExpectedPatchHash string
}

// Apply validates and applies a patch direction transactionally.
func Apply(ctx context.Context, patchPath, root string, direction Direction, callback progress.Callback) error {
	return ApplyWithOptions(ctx, ApplyOptions{PatchPath: patchPath, Root: root, Direction: direction}, callback)
}

// ApplyWithOptions validates and applies a configured patch operation.
func ApplyWithOptions(ctx context.Context, options ApplyOptions, callback progress.Callback) error {
	return applyWithOperations(ctx, options, callback, defaultApplyOperations)
}

func applyWithOperations(ctx context.Context, options ApplyOptions, callback progress.Callback, operations applyOperations) (resultError error) {
	patchPath := options.PatchPath
	root := options.Root
	direction := options.Direction
	if direction != Forward && direction != Reverse {
		return fmt.Errorf("unsupported patch direction %q", direction)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	workDirectory, err := os.MkdirTemp("", "viper-patcher-apply-*")
	if err != nil {
		return fmt.Errorf("create application work directory: %w", err)
	}
	defer func() {
		if cleanupError := os.RemoveAll(workDirectory); cleanupError != nil {
			resultError = errors.Join(resultError, fmt.Errorf("remove application work directory: %w", cleanupError))
		}
	}()

	patchSnapshot, err := copyPatchSnapshot(patchPath, workDirectory)
	if err != nil {
		return err
	}
	if options.ExpectedPatchHash != "" && patchSnapshot.Hash != options.ExpectedPatchHash {
		return fmt.Errorf("selected patch changed after it was inspected")
	}
	progress.Report(callback, progress.Event{Stage: progress.StagePreparing})
	parsed, err := Open(patchSnapshot.SnapshotPath)
	if err != nil {
		return err
	}
	if direction == Reverse && !parsed.Header.Reverse {
		return fmt.Errorf("patch does not contain reverse differentials")
	}
	validation, err := Inspect(root, parsed)
	if err != nil {
		return err
	}
	if !validation.Ready(direction) {
		return validation.ErrorFor(direction)
	}

	transaction := NewTransaction()
	defer func() {
		if cleanupError := transaction.Cleanup(); cleanupError != nil {
			resultError = errors.Join(resultError, cleanupError)
		}
	}()

	for index, entry := range parsed.Header.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := pathutil.SecureJoinExisting(root, entry.Path)
		if err != nil {
			return err
		}
		sourceSnapshot, err := snapshotRegularFile(path, filepath.Join(workDirectory, fmt.Sprintf("%06d.reference", index)))
		if err != nil {
			return fmt.Errorf("snapshot installed file %q: %w", entry.Path, err)
		}
		offset, length, expectedInput, expectedOutput := differential(entry, direction)
		if sourceSnapshot.Hash != expectedInput.hash || sourceSnapshot.Size != expectedInput.size || sourceSnapshot.Mode != expectedInput.mode {
			return fmt.Errorf("installed file %q changed after preflight validation", entry.Path)
		}

		temporary, err := os.CreateTemp(filepath.Dir(path), ".viper-patcher-output-*")
		if err != nil {
			return fmt.Errorf("create temporary output for %q: %w", entry.Path, err)
		}
		temporaryPath := temporary.Name()
		if err := temporary.Close(); err != nil {
			return removeTemporaryAfterError(temporaryPath, fmt.Errorf("close temporary output for %q: %w", entry.Path, err))
		}

		progress.Report(callback, progress.Event{
			FileIndex:  index + 1,
			FileCount:  len(parsed.Header.Files),
			Path:       entry.Path,
			Stage:      progress.StageApplying,
			TotalBytes: expectedOutput.size,
		})
		err = zstd.DecompressSegment(sourceSnapshot.SnapshotPath, patchSnapshot.SnapshotPath, parsed.DataOffset+offset, length, temporaryPath, expectedOutput.size, func(processed, total uint64) {
			progress.Report(callback, progress.Event{
				FileIndex:      index + 1,
				FileCount:      len(parsed.Header.Files),
				Path:           entry.Path,
				Stage:          progress.StageApplying,
				ProcessedBytes: processed,
				TotalBytes:     total,
			})
		})
		if err != nil {
			return removeTemporaryAfterError(temporaryPath, fmt.Errorf("apply %s differential for %q: %w", direction, entry.Path, err))
		}

		progress.Report(callback, progress.Event{
			FileIndex: index + 1,
			FileCount: len(parsed.Header.Files),
			Path:      entry.Path,
			Stage:     progress.StageVerifying,
		})
		digest, size, err := hashutil.File(temporaryPath)
		if err != nil {
			return removeTemporaryAfterError(temporaryPath, fmt.Errorf("hash generated file %q: %w", entry.Path, err))
		}
		if digest != expectedOutput.hash || size != expectedOutput.size {
			return removeTemporaryAfterError(temporaryPath, fmt.Errorf("generated file %q failed integrity verification", entry.Path))
		}
		if err := setPortableMode(temporaryPath, expectedOutput.mode, operations.chmod); err != nil {
			return removeTemporaryAfterError(temporaryPath, fmt.Errorf("set permissions for %q: %w", entry.Path, err))
		}
		if err := transaction.Add(path, temporaryPath, fileExpectation{
			Identity: sourceSnapshot.Identity,
			Hash:     sourceSnapshot.Hash,
			Size:     sourceSnapshot.Size,
			Mode:     sourceSnapshot.Mode,
		}); err != nil {
			return removeTemporaryAfterError(temporaryPath, err)
		}
		progress.Report(callback, progress.Event{
			FileIndex:      index + 1,
			FileCount:      len(parsed.Header.Files),
			Path:           entry.Path,
			Stage:          progress.StageFileCompleted,
			ProcessedBytes: expectedOutput.size,
			TotalBytes:     expectedOutput.size,
		})
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	progress.Report(callback, progress.Event{
		FileIndex: len(parsed.Header.Files),
		FileCount: len(parsed.Header.Files),
		Stage:     progress.StageCompleted,
	})
	return nil
}

func removeTemporaryAfterError(path string, operationError error) error {
	removeError := os.Remove(path)
	if removeError == nil || os.IsNotExist(removeError) {
		return operationError
	}
	return errors.Join(operationError, fmt.Errorf("remove temporary file %q: %w", path, removeError))
}

type fileState struct {
	hash string
	size uint64
	mode uint32
}

func differential(entry patchformat.FileEntry, direction Direction) (offset, length uint64, input, output fileState) {
	if direction == Reverse {
		return entry.ReverseOffset, entry.ReverseLength,
			fileState{hash: entry.TargetHash, size: entry.TargetSize, mode: entry.TargetMode & portablePermissionMask},
			fileState{hash: entry.SourceHash, size: entry.SourceSize, mode: entry.SourceMode & portablePermissionMask}
	}
	return entry.ForwardOffset, entry.ForwardLength,
		fileState{hash: entry.SourceHash, size: entry.SourceSize, mode: entry.SourceMode & portablePermissionMask},
		fileState{hash: entry.TargetHash, size: entry.TargetSize, mode: entry.TargetMode & portablePermissionMask}
}

func setPortableMode(path string, mode uint32, chmod func(string, os.FileMode) error) error {
	if chmod == nil {
		return fmt.Errorf("file permission operation is not configured")
	}
	return chmod(path, os.FileMode(mode&portablePermissionMask))
}
