package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

const portablePermissionMask uint32 = 0o777

type applyOperations struct {
	chmod func(*os.File, os.FileMode) error
}

var defaultApplyOperations = applyOperations{chmod: func(file *os.File, mode os.FileMode) error {
	return file.Chmod(mode)
}}

// ApplyOptions configures one patch application operation.
type ApplyOptions struct {
	PatchPath         string
	Root              string
	Direction         Direction
	ExpectedPatchHash string
}

// ApplyWithOptions validates and applies a configured patch operation.
func ApplyWithOptions(ctx context.Context, options ApplyOptions, callback progress.Callback) error {
	return applyWithOperations(ctx, options, callback, defaultApplyOperations)
}

func applyWithOperations(ctx context.Context, options ApplyOptions, callback progress.Callback, operations applyOperations) error {
	if options.Direction != Forward && options.Direction != Reverse {
		return fmt.Errorf("unsupported patch direction %q", options.Direction)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	workDirectory, err := os.MkdirTemp("", "viper-patcher-apply-*")
	if err != nil {
		return fmt.Errorf("create application work directory: %w", err)
	}
	root, err := openInstallationRoot(options.Root)
	if err != nil {
		return errors.Join(
			err,
			wrapJoinedError("remove application work directory", os.RemoveAll(workDirectory)),
		)
	}

	callback = synchronizedProgress(callback)
	patchSnapshot, operationError := copyPatchSnapshot(options.PatchPath, workDirectory, options.ExpectedPatchHash)
	if options.ExpectedPatchHash != "" && (errors.Is(operationError, errSnapshotContentMismatch) ||
		(operationError == nil && patchSnapshot.Hash != options.ExpectedPatchHash)) {
		operationError = fmt.Errorf("selected patch changed after it was inspected")
	}
	progress.Report(callback, progress.Event{Stage: progress.StagePreparing})

	var parsed patchformat.Patch
	if operationError == nil {
		parsed, operationError = openPrivatePatchSnapshot(patchSnapshot)
	}
	if operationError == nil && options.Direction == Reverse && !parsed.Header.Reverse {
		operationError = fmt.Errorf("patch does not contain reverse differentials")
	}

	transaction := newRootTransaction(root.root)
	prepared := make([]progress.Event, 0, len(parsed.Header.Files))
	if operationError == nil {
		for index, entry := range parsed.Header.Files {
			if err := ctx.Err(); err != nil {
				operationError = err
				break
			}
			offset, length, expectedInput, expectedOutput := differential(entry, options.Direction)
			sourceSnapshot, targetName, err := snapshotInstallationFile(
				root,
				entry.Path,
				filepath.Join(workDirectory, fmt.Sprintf("%06d.reference", index)),
				snapshotContentExpectation{hash: expectedInput.hash, size: expectedInput.size, hasSize: true},
			)
			if err != nil {
				operationError = fmt.Errorf("snapshot installed file %q: %w", entry.Path, err)
				break
			}
			if sourceSnapshot.Hash != expectedInput.hash || sourceSnapshot.Size != expectedInput.size {
				operationError = fmt.Errorf("installed file %q does not match the required %s input state", entry.Path, options.Direction)
				break
			}
			segmentOffset, ok := checkedAdd(parsed.DataOffset, offset)
			if !ok {
				operationError = fmt.Errorf("differential offset for %q overflows", entry.Path)
				break
			}

			temporary, temporaryName, err := createRootTemp(root.root, filepath.Dir(targetName), ".viper-patcher-output-")
			if err != nil {
				operationError = fmt.Errorf("create temporary output for %q: %w", entry.Path, err)
				break
			}
			progress.Report(callback, progress.Event{
				FileIndex:  index + 1,
				FileCount:  len(parsed.Header.Files),
				Path:       entry.Path,
				Stage:      progress.StageApplying,
				TotalBytes: expectedOutput.size,
			})
			err = zstd.DecompressSegmentToFile(sourceSnapshot.SnapshotPath, patchSnapshot.SnapshotPath, segmentOffset, length, temporary, expectedOutput.size, func(processed, total uint64) {
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
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("apply %s differential for %q: %w", options.Direction, entry.Path, err))
				break
			}
			if err := temporary.Sync(); err != nil {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("sync generated file %q: %w", entry.Path, err))
				break
			}
			progress.Report(callback, progress.Event{
				FileIndex: index + 1,
				FileCount: len(parsed.Header.Files),
				Path:      entry.Path,
				Stage:     progress.StageVerifying,
			})
			if _, err := temporary.Seek(0, io.SeekStart); err != nil {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("rewind generated file %q: %w", entry.Path, err))
				break
			}
			digest, size, err := hashutil.Reader(temporary)
			if err != nil {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("hash generated file %q: %w", entry.Path, err))
				break
			}
			if digest != expectedOutput.hash || size != expectedOutput.size {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("generated file %q failed integrity verification", entry.Path))
				break
			}
			if err := applyOutputMode(temporary, sourceSnapshot.Mode, operations.chmod); err != nil {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("set permissions for %q: %w", entry.Path, err))
				break
			}
			outputInfo, err := temporary.Stat()
			if err != nil {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("inspect generated file %q: %w", entry.Path, err))
				break
			}
			if !outputModeMatches(uint32(outputInfo.Mode().Perm()), sourceSnapshot.Mode) {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("generated file %q does not preserve the installed permissions", entry.Path))
				break
			}
			if err := temporary.Sync(); err != nil {
				operationError = cleanupRootTemporary(root.root, temporary, temporaryName, fmt.Errorf("sync generated metadata for %q: %w", entry.Path, err))
				break
			}
			if err := temporary.Close(); err != nil {
				operationError = errors.Join(fmt.Errorf("close generated file %q: %w", entry.Path, err), wrapRootRemoveError(root.root, temporaryName))
				break
			}
			if err := transaction.Add(targetName, temporaryName, fileExpectation{
				Identity: sourceSnapshot.Identity,
				Hash:     sourceSnapshot.Hash,
				Size:     sourceSnapshot.Size,
			}); err != nil {
				operationError = errors.Join(err, wrapRootRemoveError(root.root, temporaryName))
				break
			}
			preparedEvent := progress.Event{
				FileIndex:      index + 1,
				FileCount:      len(parsed.Header.Files),
				Path:           entry.Path,
				Stage:          progress.StageFilePrepared,
				ProcessedBytes: expectedOutput.size,
				TotalBytes:     expectedOutput.size,
			}
			prepared = append(prepared, preparedEvent)
			progress.Report(callback, preparedEvent)
		}
	}

	if operationError == nil {
		if err := ctx.Err(); err != nil {
			operationError = err
		} else {
			operationError = transaction.Commit()
		}
	}
	committed := operationError == nil || IsCommittedWarning(operationError)
	transactionCleanup := transaction.Cleanup()
	rootCloseError := root.Close()
	workCleanupError := os.RemoveAll(workDirectory)
	if !committed {
		return errors.Join(
			operationError,
			wrapJoinedError("cleanup abandoned transaction", transactionCleanup),
			wrapJoinedError("close target root", rootCloseError),
			wrapJoinedError("remove application work directory", workCleanupError),
		)
	}

	for _, event := range prepared {
		event.Stage = progress.StageFileCompleted
		progress.Report(callback, event)
	}
	progress.Report(callback, progress.Event{
		FileIndex: len(parsed.Header.Files),
		FileCount: len(parsed.Header.Files),
		Stage:     progress.StageCompleted,
	})
	return committedWarning(
		"patch application",
		operationError,
		wrapJoinedError("cleanup committed transaction", transactionCleanup),
		wrapJoinedError("close target root", rootCloseError),
		wrapJoinedError("remove application work directory", workCleanupError),
	)
}

func cleanupRootTemporary(root *os.Root, file *os.File, path string, operationError error) error {
	var closeError error
	if file != nil {
		closeError = file.Close()
	}
	return errors.Join(operationError, wrapJoinedError("close temporary output", closeError), wrapRootRemoveError(root, path))
}

func wrapRootRemoveError(root *os.Root, path string) error {
	if path == "" {
		return nil
	}
	err := root.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove temporary output %q: %w", path, err)
}

type fileState struct {
	hash string
	size uint64
}

func differential(entry patchformat.FileEntry, direction Direction) (offset, length uint64, input, output fileState) {
	if direction == Reverse {
		return entry.ReverseOffset, entry.ReverseLength,
			fileState{hash: entry.TargetHash, size: entry.TargetSize},
			fileState{hash: entry.SourceHash, size: entry.SourceSize}
	}
	return entry.ForwardOffset, entry.ForwardLength,
		fileState{hash: entry.SourceHash, size: entry.SourceSize},
		fileState{hash: entry.TargetHash, size: entry.TargetSize}
}
