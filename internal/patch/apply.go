package patch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

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
	Parallelism       int
}

// Apply applies a patch using the fast handle-based path and automatic file-level parallelism.
func Apply(ctx context.Context, patchPath, root string, direction Direction, callback progress.Callback) error {
	return ApplyWithOptions(ctx, ApplyOptions{PatchPath: patchPath, Root: root, Direction: direction}, callback)
}

// ApplyWithOptions validates and applies a configured patch operation.
func ApplyWithOptions(ctx context.Context, options ApplyOptions, callback progress.Callback) error {
	return applyWithOperations(ctx, options, callback, defaultApplyOperations)
}

type preparedApplicationFile struct {
	source        *os.File
	targetName    string
	temporaryName string
	expectation   fileExpectation
	preparedEvent progress.Event
}

type decoderPool struct {
	available chan *zstd.Decoder
	all       []*zstd.Decoder
}

func newDecoderPool(count int) (*decoderPool, error) {
	if count < 1 {
		count = 1
	}
	pool := &decoderPool{available: make(chan *zstd.Decoder, count), all: make([]*zstd.Decoder, 0, count)}
	for range count {
		decoder, err := zstd.NewDecoder()
		if err != nil {
			_ = pool.Close()
			return nil, err
		}
		pool.all = append(pool.all, decoder)
		pool.available <- decoder
	}
	return pool, nil
}

func (pool *decoderPool) acquire() *zstd.Decoder {
	return <-pool.available
}

func (pool *decoderPool) release(decoder *zstd.Decoder) {
	pool.available <- decoder
}

func (pool *decoderPool) Close() error {
	if pool == nil {
		return nil
	}
	var closeErrors []error
	for _, decoder := range pool.all {
		closeErrors = append(closeErrors, decoder.Close())
	}
	pool.all = nil
	return errors.Join(closeErrors...)
}

func applyWithOperations(ctx context.Context, options ApplyOptions, callback progress.Callback, operations applyOperations) error {
	if options.Direction != Forward && options.Direction != Reverse {
		return fmt.Errorf("unsupported patch direction %q", options.Direction)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	openedPatch, err := openPatchForApply(options.PatchPath, options.ExpectedPatchHash)
	if err != nil {
		return err
	}
	root, err := openInstallationRoot(options.Root)
	if err != nil {
		return errors.Join(err, wrapJoinedError("close patch", openedPatch.Close()))
	}
	callback = synchronizedProgress(callback)
	parsed := openedPatch.parsed
	if options.Direction == Reverse && !parsed.Header.Reverse {
		return errors.Join(
			fmt.Errorf("patch does not contain reverse differentials"),
			wrapJoinedError("close target root", root.Close()),
			wrapJoinedError("close patch", openedPatch.Close()),
		)
	}

	parallelism := options.Parallelism
	if parallelism <= 0 {
		parallelism = runtime.NumCPU()
	}
	if parallelism > runtime.NumCPU() {
		parallelism = runtime.NumCPU()
	}
	if parallelism > len(parsed.Header.Files) {
		parallelism = len(parsed.Header.Files)
	}
	if parallelism < 1 {
		parallelism = 1
	}
	decoders, err := newDecoderPool(parallelism)
	if err != nil {
		return errors.Join(err, wrapJoinedError("close target root", root.Close()), wrapJoinedError("close patch", openedPatch.Close()))
	}

	prepared := make([]preparedApplicationFile, len(parsed.Header.Files))
	operationError := parallelFor(ctx, len(parsed.Header.Files), parallelism, func(ctx context.Context, index int) error {
		entry := parsed.Header.Files[index]
		result, err := prepareApplicationFile(ctx, root, openedPatch, entry, options.Direction, index, len(parsed.Header.Files), callback, operations, decoders)
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
	var sourceCloseErrors []error
	for index := range prepared {
		if prepared[index].source != nil {
			sourceCloseErrors = append(sourceCloseErrors, prepared[index].source.Close())
			prepared[index].source = nil
		}
	}
	decoderCloseError := decoders.Close()
	rootCloseError := root.Close()
	patchCloseError := openedPatch.Close()
	if !committed {
		return errors.Join(
			operationError,
			wrapJoinedError("cleanup abandoned transaction", transactionCleanup),
			wrapJoinedError("remove unregistered prepared outputs", errors.Join(preparedCleanupErrors...)),
			wrapJoinedError("close source files", errors.Join(sourceCloseErrors...)),
			wrapJoinedError("close decoders", decoderCloseError),
			wrapJoinedError("close target root", rootCloseError),
			wrapJoinedError("close patch", patchCloseError),
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
		wrapJoinedError("close source files", errors.Join(sourceCloseErrors...)),
		wrapJoinedError("close decoders", decoderCloseError),
		wrapJoinedError("close target root", rootCloseError),
		wrapJoinedError("close patch", patchCloseError),
	)
}

func prepareApplicationFile(ctx context.Context, root *installationRoot, patch *openedPatch, entry patchformat.FileEntry, direction Direction, index, fileCount int, callback progress.Callback, operations applyOperations, decoders *decoderPool) (preparedApplicationFile, error) {
	offset, length, expandedLength, method, expectedInput, expectedOutput := differential(entry, direction)
	source, identity, targetName, err := root.openStableRegularFile(entry.Path)
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
		return preparedApplicationFile{}, fmt.Errorf("installed file %q does not match the required %s input size", entry.Path, direction)
	}

	segmentOffset, ok := checkedAdd(patch.parsed.DataOffset, offset)
	if !ok {
		return preparedApplicationFile{}, fmt.Errorf("differential offset for %q overflows", entry.Path)
	}
	temporary, temporaryName, err := createRootTemp(root.root, filepath.Dir(targetName), ".viper-patcher-output-")
	if err != nil {
		return preparedApplicationFile{}, fmt.Errorf("create temporary output for %q: %w", entry.Path, err)
	}
	cleanupTemporary := true
	defer func() {
		if cleanupTemporary {
			_ = temporary.Close()
			_ = root.root.Remove(temporaryName)
		}
	}()

	event := progress.Event{FileIndex: index + 1, FileCount: fileCount, Path: entry.Path, Stage: progress.StageApplying, TotalBytes: expectedOutput.size}
	progress.Report(callback, event)

	switch method {
	case patchformat.MethodSparse, patchformat.MethodCopyAdd:
		if method == patchformat.MethodSparse && expectedInput.size != expectedOutput.size {
			return preparedApplicationFile{}, fmt.Errorf("sparse differential for %q changes file size", entry.Path)
		}
		operationsFile, operationsPath, err := decompressInstructionStream(ctx, patch.file, segmentOffset, length, expandedLength, method, decoders)
		if err != nil {
			return preparedApplicationFile{}, fmt.Errorf("decompress %s operations for %q: %w", method, entry.Path, err)
		}
		defer func() {
			_ = operationsFile.Close()
			_ = os.Remove(operationsPath)
		}()
		if method == patchformat.MethodSparse {
			err = applySparseStream(source, operationsFile, temporary, expectedOutput.size, expectedInput.hash, expectedOutput.hash, callback, event)
		} else {
			err = applyCopyAddStream(source, operationsFile, temporary, expectedInput.size, expectedOutput.size, expectedInput.hash, expectedOutput.hash, callback, event)
		}
		if err != nil {
			return preparedApplicationFile{}, fmt.Errorf("apply %s differential for %q: %w", method, entry.Path, err)
		}
	case patchformat.MethodPatchFrom, patchformat.MethodReplace:
		if err := verifySourceForDecode(source, expectedInput); err != nil {
			return preparedApplicationFile{}, fmt.Errorf("validate installed file %q: %w", entry.Path, err)
		}
		outputHash := sha256.New()
		decoder := decoders.acquire()
		var reference *os.File
		if method == patchformat.MethodPatchFrom {
			reference = source
		}
		err = decoder.DecompressSegmentToFile(ctx, reference, patch.file, segmentOffset, length, temporary, expectedOutput.size, func(processed, total uint64) {
			event.ProcessedBytes = processed
			event.TotalBytes = total
			progress.Report(callback, event)
		}, func(block []byte) error {
			_, writeError := outputHash.Write(block)
			return writeError
		})
		decoders.release(decoder)
		if err != nil {
			return preparedApplicationFile{}, fmt.Errorf("apply %s differential for %q: %w", method, entry.Path, err)
		}
		if hex.EncodeToString(outputHash.Sum(nil)) != expectedOutput.hash {
			return preparedApplicationFile{}, fmt.Errorf("generated file %q failed SHA-256 verification", entry.Path)
		}
	default:
		return preparedApplicationFile{}, fmt.Errorf("unsupported differential method %q", method)
	}

	if err := applyOutputMode(temporary, uint32(identity.Mode().Perm()), operations.chmod); err != nil {
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
	if err := temporary.Close(); err != nil {
		return preparedApplicationFile{}, fmt.Errorf("close generated file %q: %w", entry.Path, err)
	}

	cleanupSource = false
	cleanupTemporary = false
	preparedEvent := progress.Event{FileIndex: index + 1, FileCount: fileCount, Path: entry.Path, Stage: progress.StageFilePrepared, ProcessedBytes: expectedOutput.size, TotalBytes: expectedOutput.size}
	progress.Report(callback, preparedEvent)
	return preparedApplicationFile{
		source:        source,
		targetName:    targetName,
		temporaryName: temporaryName,
		expectation: fileExpectation{
			Identity: identity,
			Size:     expectedInput.size,
		},
		preparedEvent: preparedEvent,
	}, nil
}

func decompressInstructionStream(ctx context.Context, patch *os.File, offset, length, expandedLength uint64, method string, decoders *decoderPool) (*os.File, string, error) {
	operationsFile, err := os.CreateTemp("", "viper-patcher-operations-*")
	if err != nil {
		return nil, "", fmt.Errorf("create operation buffer: %w", err)
	}
	operationsPath := operationsFile.Name()
	decoder := decoders.acquire()
	decodeError := decoder.DecompressSegmentToFile(ctx, nil, patch, offset, length, operationsFile, expandedLength, nil, nil)
	decoders.release(decoder)
	if decodeError != nil {
		_ = operationsFile.Close()
		_ = os.Remove(operationsPath)
		return nil, "", fmt.Errorf("decode %s stream: %w", method, decodeError)
	}
	return operationsFile, operationsPath, nil
}

func verifySourceForDecode(source *os.File, expected fileState) error {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest, size, err := hashutil.Reader(source)
	if err != nil {
		return err
	}
	if digest != expected.hash || size != expected.size {
		return fmt.Errorf("source SHA-256 or size does not match patch metadata")
	}
	_, err = source.Seek(0, io.SeekStart)
	return err
}

type fileState struct {
	hash string
	size uint64
}

func differential(entry patchformat.FileEntry, direction Direction) (offset, length, expandedLength uint64, method string, input, output fileState) {
	if direction == Reverse {
		return entry.ReverseOffset, entry.ReverseLength, entry.ReverseExpandedLength, entry.ReverseDifferentialMethod(),
			fileState{hash: entry.TargetHash, size: entry.TargetSize},
			fileState{hash: entry.SourceHash, size: entry.SourceSize}
	}
	return entry.ForwardOffset, entry.ForwardLength, entry.ForwardExpandedLength, entry.ForwardDifferentialMethod(),
		fileState{hash: entry.SourceHash, size: entry.SourceSize},
		fileState{hash: entry.TargetHash, size: entry.TargetSize}
}
