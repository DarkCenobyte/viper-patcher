//go:build ignore

package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

const portablePermissionMask uint32 = 0o777

type applyOperations struct {
	chmod func(*os.File, os.FileMode) error
	sync  func(*os.File) error
}

var defaultApplyOperations = applyOperations{
	chmod: func(file *os.File, mode os.FileMode) error {
		return file.Chmod(mode)
	},
	sync: func(file *os.File) error {
		return file.Sync()
	},
}

func (operations applyOperations) withDefaults() applyOperations {
	if operations.chmod == nil {
		operations.chmod = defaultApplyOperations.chmod
	}
	if operations.sync == nil {
		operations.sync = defaultApplyOperations.sync
	}
	return operations
}

// ApplyOptions configures one patch application operation.
type ApplyOptions struct {
	PatchPath         string
	Root              string
	Direction         Direction
	ExpectedPatchHash string
	WorkerBudget      int
}

// PreparedApplyOptions configures application through an already validated and
// fingerprinted PreparedPatch.
type PreparedApplyOptions struct {
	Root         string
	Direction    Direction
	WorkerBudget int
}

// Apply applies a patch using the fast handle-based path and automatic worker allocation.
func Apply(ctx context.Context, patchPath, root string, direction Direction, callback progress.Callback) error {
	return ApplyWithOptions(ctx, ApplyOptions{PatchPath: patchPath, Root: root, Direction: direction}, callback)
}

// ApplyWithOptions validates and applies a configured patch operation.
func ApplyWithOptions(ctx context.Context, options ApplyOptions, callback progress.Callback) error {
	return applyWithOperations(ctx, options, callback, defaultApplyOperations)
}

// ApplyPreparedWithOptions reuses the stable handle owned by prepared. It does
// not reopen or fingerprint the patch again.
func ApplyPreparedWithOptions(ctx context.Context, prepared *PreparedPatch, options PreparedApplyOptions, callback progress.Callback) error {
	return applyPreparedWithOperations(ctx, prepared, options, callback, defaultApplyOperations)
}

type preparedApplicationFile struct {
	targetName    string
	temporaryName string
	expectation   fileExpectation
	preparedEvent progress.Event
}

type applicationFilePreparer struct {
	root           *installationRoot
	patch          *openedPatch
	direction      Direction
	fileCount      int
	perFileWorkers int
	callback       progress.Callback
	operations     applyOperations
	decoders       *decoderPool
}

func validateApplyRequest(ctx context.Context, direction Direction) error {
	if direction != Forward && direction != Reverse {
		return fmt.Errorf("unsupported patch direction %q", direction)
	}
	return ctx.Err()
}

func applyWithOperations(ctx context.Context, options ApplyOptions, callback progress.Callback, operations applyOperations) error {
	if err := validateApplyRequest(ctx, options.Direction); err != nil {
		return err
	}
	opened, err := openPatchForApply(options.PatchPath, options.ExpectedPatchHash)
	if err != nil {
		return err
	}
	return applyOpenedPatchWithOperations(ctx, opened, opened.Close, options.Root, options.Direction, options.WorkerBudget, callback, operations)
}

func applyPreparedWithOperations(ctx context.Context, prepared *PreparedPatch, options PreparedApplyOptions, callback progress.Callback, operations applyOperations) error {
	if err := validateApplyRequest(ctx, options.Direction); err != nil {
		return err
	}
	opened, release, err := prepared.acquire()
	if err != nil {
		return err
	}
	return applyOpenedPatchWithOperations(ctx, opened, release, options.Root, options.Direction, options.WorkerBudget, callback, operations)
}

func applyOpenedPatchWithOperations(ctx context.Context, opened *openedPatch, releasePatch func() error, rootPath string, direction Direction, workerBudget int, callback progress.Callback, operations applyOperations) error {
	operations = operations.withDefaults()
	root, err := openInstallationRoot(rootPath)
	if err != nil {
		return errors.Join(err, wrapJoinedError("release patch", releasePatch()))
	}
	parsed := opened.parsed
	if direction == Reverse && !parsed.Header.Reverse {
		return errors.Join(
			fmt.Errorf("patch does not contain reverse differentials"),
			wrapJoinedError("close target root", root.Close()),
			wrapJoinedError("release patch", releasePatch()),
		)
	}
	callback = newApplicationProgress(callback, parsed.Header.Files, direction)
	plan := newApplicationPlan(workerBudget, parsed.Header.Files, direction)
	decoders, err := newDecoderPool(plan.decoderCount, processApplicationResources.zstdWindowBudget, opened.file)
	if err != nil {
		return errors.Join(err, wrapJoinedError("close target root", root.Close()), wrapJoinedError("release patch", releasePatch()))
	}

	prepared := make([]preparedApplicationFile, len(parsed.Header.Files))
	preparer := applicationFilePreparer{
		root:           root,
		patch:          opened,
		direction:      direction,
		fileCount:      len(parsed.Header.Files),
		perFileWorkers: plan.perFileWorkers,
		callback:       callback,
		operations:     operations,
		decoders:       decoders,
	}
	operationError := parallelFor(ctx, len(parsed.Header.Files), plan.fileWorkers, func(ctx context.Context, index int) error {
		entry := parsed.Header.Files[index]
		result, err := preparer.prepare(ctx, index, entry)
		if err != nil {
			return err
		}
		prepared[index] = result
		return nil
	})
	if operationError == nil {
		progress.Report(callback, progress.Event{Stage: progress.StagePreparing})
	}

	transaction := newRootTransaction(root.root)
	if operationError == nil {
		for index := range prepared {
			file := &prepared[index]
			if err := transaction.Add(file.targetName, file.temporaryName, file.expectation); err != nil {
				operationError = err
				break
			}
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
	var preparedCleanupErrors []error
	if !committed {
		for index := range prepared {
			if prepared[index].temporaryName == "" {
				continue
			}
			if err := root.root.Remove(prepared[index].temporaryName); err != nil && !os.IsNotExist(err) {
				preparedCleanupErrors = append(preparedCleanupErrors, fmt.Errorf("remove prepared output %q: %w", prepared[index].temporaryName, err))
			}
		}
	}
	decoderCloseError := decoders.Close()
	rootCloseError := root.Close()
	patchReleaseError := releasePatch()
	if !committed {
		return errors.Join(
			operationError,
			wrapJoinedError("cleanup abandoned transaction", transactionCleanup),
			wrapJoinedError("remove unregistered prepared outputs", errors.Join(preparedCleanupErrors...)),
			wrapJoinedError("close decoders", decoderCloseError),
			wrapJoinedError("close target root", rootCloseError),
			wrapJoinedError("release patch", patchReleaseError),
		)
	}

	for _, file := range prepared {
		event := file.preparedEvent
		event.Stage = progress.StageFileCompleted
		progress.Report(callback, event)
	}
	progress.Report(callback, progress.Event{FileIndex: len(parsed.Header.Files), FileCount: len(parsed.Header.Files), Stage: progress.StageCompleted})
	return committedWarning(
		"patch application",
		operationError,
		wrapJoinedError("cleanup committed transaction", transactionCleanup),
		wrapJoinedError("close decoders", decoderCloseError),
		wrapJoinedError("close target root", rootCloseError),
		wrapJoinedError("release patch", patchReleaseError),
	)
}

func (preparer applicationFilePreparer) prepare(ctx context.Context, index int, entry patchformat.FileEntry) (preparedApplicationFile, error) {
	offset, length, expandedLength, method, expectedInput, expectedOutput := differential(entry, preparer.direction)
	source, identity, targetName, err := preparer.root.openStableRegularFile(entry.Path)
	if err != nil {
		return preparedApplicationFile{}, fmt.Errorf("open installed file %q: %w", entry.Path, err)
	}
	cleanupSource := true
	defer func() {
		if cleanupSource {
			_ = source.Close()
		}
	}()
	if identity.Size() < 0 || uint64(identity.Size()) != expectedInput.size {
		return preparedApplicationFile{}, fmt.Errorf("installed file %q does not match the required %s input size", entry.Path, preparer.direction)
	}

	segmentOffset, ok := checkedAdd(preparer.patch.parsed.DataOffset, offset)
	if !ok {
		return preparedApplicationFile{}, fmt.Errorf("differential offset for %q overflows", entry.Path)
	}
	temporary, temporaryName, err := createRootTemp(preparer.root.root, filepath.Dir(targetName), ".viper-patcher-output-")
	if err != nil {
		return preparedApplicationFile{}, fmt.Errorf("create temporary output for %q: %w", entry.Path, err)
	}
	cleanupTemporary := true
	defer func() {
		if cleanupTemporary {
			_ = temporary.Close()
			_ = preparer.root.root.Remove(temporaryName)
		}
	}()

	event := progress.Event{FileIndex: index + 1, FileCount: preparer.fileCount, Path: entry.Path, TotalBytes: expectedOutput.size}
	chunkWorkers := adaptiveChunkWorkers(preparer.perFileWorkers, maxUint64(expectedInput.size, expectedOutput.size))

	switch method {
	case patchformat.MethodSparse:
		event.Stage = progress.StageApplying
		progress.Report(preparer.callback, event)
		err = func() error {
			decoder, releaseDecoder, acquireError := preparer.decoders.acquire(ctx, preparer.patch.file, segmentOffset, length)
			if acquireError != nil {
				return acquireError
			}
			defer releaseDecoder()
			return applyCompressedInstructionStream(ctx, decoder, expandedLength, func(reader io.Reader) error {
				return applySparseStreamParallel(ctx, source, reader, temporary, expectedOutput.size, expectedInput.hash, expectedOutput.hash, chunkWorkers, preparer.callback, event)
			})
		}()
		if err != nil {
			return preparedApplicationFile{}, fmt.Errorf("apply %s differential for %q: %w", method, entry.Path, err)
		}
	case patchformat.MethodCopyAdd:
		if err := applyCopyAddConcurrent(ctx, source, preparer.patch.file, temporary, segmentOffset, length, expandedLength, expectedInput, expectedOutput, chunkWorkers, preparer.callback, event, preparer.decoders); err != nil {
			return preparedApplicationFile{}, fmt.Errorf("apply %s differential for %q: %w", method, entry.Path, err)
		}
	case patchformat.MethodChunkedReplace:
		event.Stage = progress.StageApplying
		progress.Report(preparer.callback, event)
		if err := applyChunkedReplace(ctx, source, preparer.patch.file, temporary, segmentOffset, length, expectedInput, expectedOutput, chunkWorkers, preparer.callback, event, preparer.decoders); err != nil {
			return preparedApplicationFile{}, fmt.Errorf("apply %s differential for %q: %w", method, entry.Path, err)
		}
	case patchformat.MethodReplace:
		if err := applyStandaloneReplaceConcurrent(ctx, source, preparer.patch.file, temporary, segmentOffset, length, expectedInput, expectedOutput, chunkWorkers, preparer.callback, event, preparer.decoders); err != nil {
			return preparedApplicationFile{}, fmt.Errorf("apply %s differential for %q: %w", method, entry.Path, err)
		}
	default:
		return preparedApplicationFile{}, fmt.Errorf("unsupported differential method %q", method)
	}

	if err := applyOutputMode(temporary, uint32(identity.Mode().Perm()), preparer.operations.chmod); err != nil {
		return preparedApplicationFile{}, fmt.Errorf("set permissions for %q: %w", entry.Path, err)
	}
	outputInfo, err := temporary.Stat()
	if err != nil {
		return preparedApplicationFile{}, fmt.Errorf("inspect generated file %q: %w", entry.Path, err)
	}
	if outputInfo.Size() < 0 || uint64(outputInfo.Size()) != expectedOutput.size {
		return preparedApplicationFile{}, fmt.Errorf("generated file %q has an unexpected size", entry.Path)
	}
	if !outputModeMatches(uint32(outputInfo.Mode().Perm()), uint32(identity.Mode().Perm())) {
		return preparedApplicationFile{}, fmt.Errorf("generated file %q does not preserve the installed permissions", entry.Path)
	}
	if err := preparer.operations.sync(temporary); err != nil {
		return preparedApplicationFile{}, fmt.Errorf("sync generated file %q: %w", entry.Path, err)
	}
	if err := temporary.Close(); err != nil {
		return preparedApplicationFile{}, fmt.Errorf("close generated file %q: %w", entry.Path, err)
	}
	if err := source.Close(); err != nil {
		return preparedApplicationFile{}, fmt.Errorf("close installed file %q after preparation: %w", entry.Path, err)
	}

	cleanupSource = false
	cleanupTemporary = false
	preparedEvent := progress.Event{FileIndex: index + 1, FileCount: preparer.fileCount, Path: entry.Path, Stage: progress.StageFilePrepared, ProcessedBytes: expectedOutput.size, TotalBytes: expectedOutput.size}
	progress.Report(preparer.callback, preparedEvent)
	return preparedApplicationFile{
		targetName:    targetName,
		temporaryName: temporaryName,
		expectation: fileExpectation{
			Identity: identity,
			Size:     expectedInput.size,
		},
		preparedEvent: preparedEvent,
	}, nil
}
