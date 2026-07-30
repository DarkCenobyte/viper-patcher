package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/buildinfo"
	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type CreateOptions struct {
	Files            []FilePair
	OutputPath       string
	CompressionLevel int
	Comment          string
	CreateReverse    bool
	WorkDirectory    string
	WorkerBudget     int
	MemoryLimit      uint64
	IOProfile        IOProfile
	WindowSize       uint32
	Optimization     OptimizationMode
}

type CreateEstimate struct{ TotalBytes, WorkDirectoryBytes, OutputDirectoryBytes uint64 }

func addEstimatedBytes(total *uint64, value uint64) error {
	sum, carry := bits.Add64(*total, value, 0)
	if carry != 0 {
		return fmt.Errorf("creation size estimate exceeds uint64")
	}
	*total = sum
	return nil
}

func EstimateCreate(options CreateOptions) (CreateEstimate, error) {
	if len(options.Files) == 0 {
		return CreateEstimate{}, fmt.Errorf("at least one file pair is required")
	}
	var source, target uint64
	for _, pair := range options.Files {
		a, err := os.Stat(pair.SourcePath)
		if err != nil {
			return CreateEstimate{}, err
		}
		b, err := os.Stat(pair.TargetPath)
		if err != nil {
			return CreateEstimate{}, err
		}
		if !a.Mode().IsRegular() || !b.Mode().IsRegular() || a.Size() < 0 || b.Size() < 0 {
			return CreateEstimate{}, fmt.Errorf("creation inputs must be regular files")
		}
		if err := addEstimatedBytes(&source, uint64(a.Size())); err != nil {
			return CreateEstimate{}, err
		}
		if err := addEstimatedBytes(&target, uint64(b.Size())); err != nil {
			return CreateEstimate{}, err
		}
	}
	work := source
	if err := addEstimatedBytes(&work, target); err != nil {
		return CreateEstimate{}, err
	}
	output := target
	if err := addEstimatedBytes(&output, target/4); err != nil {
		return CreateEstimate{}, err
	}
	if err := addEstimatedBytes(&output, 64<<20); err != nil {
		return CreateEstimate{}, err
	}
	if options.CreateReverse {
		if err := addEstimatedBytes(&output, source); err != nil {
			return CreateEstimate{}, err
		}
		if err := addEstimatedBytes(&output, source/4); err != nil {
			return CreateEstimate{}, err
		}
	}
	total := work
	if err := addEstimatedBytes(&total, output); err != nil {
		return CreateEstimate{}, err
	}
	return CreateEstimate{total, work, output}, nil
}

type snapshotPair struct {
	path, sourcePath, targetPath string
	sourceSize, targetSize       uint64
}

func Create(ctx context.Context, options CreateOptions, callback progress.Callback) error {
	if ctx == nil {
		ctx = context.Background()
	}
	callback = progress.Serialize(callback)
	if err := validateCreateOptions(options); err != nil {
		return err
	}
	if version := nativev4.ZstdVersion(); version != patchformat.SupportedZstdVersion {
		return fmt.Errorf("Viper-Patcher V4 requires libzstd %s, linked version is %s", patchformat.SupportedZstdVersion, version)
	}
	resources := newMemoryBudget(options.MemoryLimit, operationCreate)
	createWorkers := resources.LimitWorkers(effectiveWorkers(options.WorkerBudget), createSessionReservation)
	ctx = withOperationScheduler(ctx, options.IOProfile, createWorkers)
	workParent := options.WorkDirectory
	if workParent != "" {
		absolute, err := filepath.Abs(workParent)
		if err != nil {
			return err
		}
		workParent = absolute
	}
	work, err := os.MkdirTemp(workParent, "viper-v4-create-*")
	if err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	snapshots, snapshotErr := createSnapshots(ctx, options.Files, work, createWorkers, callback)
	if snapshotErr != nil {
		return snapshotErr
	}
	outputDirectory := filepath.Dir(options.OutputPath)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	output, err := os.CreateTemp(outputDirectory, ".viper-v4-*.tmp")
	if err != nil {
		return err
	}
	tempName := output.Name()
	committed := false
	defer func() {
		_ = output.Close()
		if !committed {
			_ = os.Remove(tempName)
		}
	}()
	if err := patchformat.WritePrefix(output, boolFlag(options.CreateReverse)); err != nil {
		return err
	}
	header := patchformat.Header{FormatVersion: patchformat.FormatVersion, CreatedAt: time.Now().UTC(), Creator: patchformat.CreatorInfo{Name: "Viper-Patcher", Version: buildinfo.Version, Commit: buildinfo.Commit, BuildDate: buildinfo.BuildDate}, Comment: options.Comment, HashAlgorithm: patchformat.HashBLAKE3Tree, Compression: patchformat.Compression{Algorithm: patchformat.AlgorithmHybrid, Library: nativev4.ZstdVersion(), Mode: patchformat.CompressionHybrid, Level: options.CompressionLevel}, Reverse: options.CreateReverse, DefaultWindowSize: defaultWindowSize(options.WindowSize), Optimization: options.Optimization, Files: make([]patchformat.FileEntry, len(snapshots))}
	for index := range snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := createFileEntry(ctx, output, snapshots[index], options, createWorkers, resources, index, len(snapshots), callback)
		if err != nil {
			return fmt.Errorf("create V4 file %q: %w", snapshots[index].path, err)
		}
		header.Files[index] = entry
	}
	indexOffset, err := output.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	encoded, err := patchformat.EncodeIndex(header)
	if err != nil {
		return err
	}
	digest, err := nativev4.HashBytes(encoded)
	if err != nil {
		return err
	}
	if _, err = output.Write(encoded); err != nil {
		return err
	}
	if err = patchformat.WriteFooter(output, uint64(indexOffset), uint64(len(encoded)), digest, boolFlag(options.CreateReverse)); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return fmt.Errorf("sync V4 patch: %w", err)
	}
	if err = output.Close(); err != nil {
		return err
	}
	published, publishErr := publishCreatedPatch(tempName, options.OutputPath)
	committed = published
	if publishErr != nil && !IsCommittedWarning(publishErr) {
		return publishErr
	}
	progress.Report(callback, progress.Event{FileIndex: len(snapshots), FileCount: len(snapshots), Stage: progress.StageCompleted, Overall: 1})
	return publishErr
}
func boolFlag(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}
func defaultWindowSize(value uint32) uint32 {
	if value != 0 {
		return value
	}
	return 1 << 20
}
func validateCreateOptions(options CreateOptions) error {
	if len(options.Files) == 0 {
		return fmt.Errorf("at least one file pair is required")
	}
	if options.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}
	if options.CompressionLevel < -131072 || options.CompressionLevel > 22 {
		return fmt.Errorf("invalid zstd compression level")
	}
	if options.Optimization > OptimizePatchSize {
		return fmt.Errorf("invalid optimization mode")
	}
	for _, pair := range options.Files {
		if pair.SourcePath == "" || pair.TargetPath == "" {
			return fmt.Errorf("source and target paths are required")
		}
	}
	return nil
}

func createSnapshots(ctx context.Context, pairs []FilePair, work string, workers int, callback progress.Callback) ([]snapshotPair, error) {
	sources := make([]string, len(pairs))
	for i := range pairs {
		sources[i] = pairs[i].SourcePath
	}
	base, err := pathutil.CommonBase(sources)
	if err != nil {
		return nil, err
	}
	result := make([]snapshotPair, len(pairs))
	workers = min(max(1, workers), len(pairs))
	err = parallelFor(ctx, len(pairs), workers, func(ctx context.Context, i int) error {
		return runScheduled(ctx, taskCost{ReadUnits: 1, WriteUnits: 1}, func() error {
			pair := pairs[i]
			relative, err := pathutil.RelativePatchPath(base, pair.SourcePath)
			if err != nil {
				return err
			}
			sourcePath := filepath.Join(work, fmt.Sprintf("%06d-source", i))
			targetPath := filepath.Join(work, fmt.Sprintf("%06d-target", i))
			sourceSize, err := copySnapshot(ctx, pair.SourcePath, sourcePath)
			if err != nil {
				return err
			}
			targetSize, err := copySnapshot(ctx, pair.TargetPath, targetPath)
			if err != nil {
				return err
			}
			result[i] = snapshotPair{relative, sourcePath, targetPath, sourceSize, targetSize}
			progress.Report(callback, progress.Event{FileIndex: i + 1, FileCount: len(pairs), Path: relative, ProcessedBytes: sourceSize + targetSize, TotalBytes: sourceSize + targetSize, Stage: progress.StageSnapshotting})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func copySnapshot(ctx context.Context, sourcePath, targetPath string) (uint64, error) {
	source, identity, err := openStableRegular(sourcePath)
	if err != nil {
		return 0, err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	cloneSession, cloneErr := nativev4.NewSession(source, nil, target)
	if cloneErr == nil {
		cloneErr = cloneSession.CloneOutput(uint64(identity.Size()))
		_ = cloneSession.Close()
	}
	if cloneErr == nil {
		if err := target.Close(); err != nil {
			return 0, err
		}
		if err := stableUnchanged(source, sourcePath, identity); err != nil {
			return 0, err
		}
		return uint64(identity.Size()), nil
	}
	if !nativev4.IsUnsupported(cloneErr) {
		target.Close()
		return 0, cloneErr
	}
	buffer := make([]byte, 1<<20)
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			target.Close()
			return 0, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := target.Write(buffer[:count])
			if writeErr != nil || written != count {
				target.Close()
				return 0, errors.Join(writeErr, io.ErrShortWrite)
			}
			total += uint64(count)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			target.Close()
			return 0, readErr
		}
	}
	if err := target.Close(); err != nil {
		return 0, err
	}
	if err := stableUnchanged(source, sourcePath, identity); err != nil {
		return 0, err
	}
	return total, nil
}

func createFileEntry(ctx context.Context, output *os.File, snapshot snapshotPair, options CreateOptions, workers int, resources *memoryBudget, index, count int, callback progress.Callback) (patchformat.FileEntry, error) {
	lease, err := resources.Acquire(ctx, uint64(workers)*createSessionReservation)
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	defer lease.Release()
	source, err := os.Open(snapshot.sourcePath)
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	defer source.Close()
	target, err := os.Open(snapshot.targetPath)
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	defer target.Close()
	hashSession, err := nativev4.NewSession(source, target, nil)
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	token := nativev4.NewCancelToken(ctx)
	defer token.Close()
	sourceRoot, sourceChunks, err := hashSession.HashFileTreeWithToken(token, false, snapshot.sourceSize, patchformat.IdentityChunkSize)
	if err != nil {
		hashSession.Close()
		return patchformat.FileEntry{}, err
	}
	targetRoot, targetChunks, err := hashSession.HashFileTreeWithToken(token, true, snapshot.targetSize, patchformat.IdentityChunkSize)
	hashSession.Close()
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	windowSize := options.WindowSize
	if windowSize == 0 {
		windowSize = automaticWindowSize(max64(snapshot.sourceSize, snapshot.targetSize))
	}
	forward, err := buildWindowSetToOutput(ctx, output, source, target, snapshot.sourceSize, snapshot.targetSize, windowSize, options.CompressionLevel, options.Optimization, workers)
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	var reverse []patchformat.WindowDescriptor
	if options.CreateReverse {
		reverse, err = buildWindowSetToOutput(ctx, output, target, source, snapshot.targetSize, snapshot.sourceSize, windowSize, options.CompressionLevel, options.Optimization, workers)
		if err != nil {
			return patchformat.FileEntry{}, err
		}
	}
	progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: snapshot.path, ProcessedBytes: snapshot.targetSize, TotalBytes: snapshot.targetSize, Stage: progress.StageCompressingForward})
	return patchformat.FileEntry{Path: snapshot.path, SourceHash: sourceRoot.Hex(), TargetHash: targetRoot.Hex(), SourceSize: snapshot.sourceSize, TargetSize: snapshot.targetSize, SourceDigest: sourceRoot, TargetDigest: targetRoot, WindowSize: windowSize, SourceChunkSize: uint32(patchformat.IdentityChunkSize), SourceChunks: sourceChunks, TargetChunks: targetChunks, ForwardWindows: forward, ReverseWindows: reverse}, nil
}

type windowBuildResult struct {
	index    int
	borrowed *nativev4.BorrowedWindow
	ack      chan struct{}
	err      error
}

func releaseWindowBuildResult(result windowBuildResult) {
	if result.borrowed == nil {
		return
	}
	result.borrowed.Release()
	result.ack <- struct{}{}
}

func buildWindowSetToOutput(ctx context.Context, output, source, target *os.File, sourceSize, targetSize uint64, windowSize uint32, level int, mode OptimizationMode, workers int) ([]patchformat.WindowDescriptor, error) {
	if targetSize == 0 {
		return nil, nil
	}
	count := int((targetSize + uint64(windowSize) - 1) / uint64(windowSize))
	workers = min(max(workers, 1), count)
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	token := nativev4.NewCancelToken(operationCtx)
	defer token.Close()

	sessions := make([]*nativev4.Session, 0, workers)
	for range workers {
		session, err := nativev4.NewSession(source, target, output)
		if err != nil {
			for _, existing := range sessions {
				_ = existing.Close()
			}
			return nil, err
		}
		sessions = append(sessions, session)
	}
	defer func() {
		for _, session := range sessions {
			_ = session.Close()
		}
	}()

	jobs := make(chan int, workers*2)
	results := make(chan windowBuildResult, workers)
	var group sync.WaitGroup
	for _, session := range sessions {
		group.Add(1)
		go func(session *nativev4.Session) {
			defer group.Done()
			ack := make(chan struct{})
			for index := range jobs {
				offset := uint64(index) * uint64(windowSize)
				size := windowSize
				if remaining := targetSize - offset; remaining < uint64(size) {
					size = uint32(remaining)
				}
				var borrowed *nativev4.BorrowedWindow
				err := runScheduled(operationCtx, taskCost{CPUUnits: 1, ReadUnits: 1}, func() error {
					var buildErr error
					borrowed, buildErr = session.BuildWindowBorrowed(token, sourceSize, targetSize, offset, size, windowSize, level, mode)
					return buildErr
				})
				result := windowBuildResult{index: index, borrowed: borrowed, err: err}
				if borrowed != nil {
					result.ack = ack
				}
				results <- result
				if result.ack != nil {
					<-result.ack
				}
				if err != nil {
					return
				}
			}
		}(session)
	}
	go func() {
		defer close(jobs)
		for index := range count {
			select {
			case jobs <- index:
			case <-operationCtx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(results)
	}()

	payloadOffset, err := output.Seek(0, io.SeekCurrent)
	if err != nil {
		cancel()
		for result := range results {
			releaseWindowBuildResult(result)
		}
		return nil, err
	}
	descriptors := make([]patchformat.WindowDescriptor, count)
	pending := make(map[int]windowBuildResult, workers)
	next := 0
	var firstErr error
	for result := range results {
		if firstErr != nil {
			releaseWindowBuildResult(result)
			continue
		}
		if result.err != nil {
			firstErr = result.err
			cancel()
			releaseWindowBuildResult(result)
			for pendingIndex, ready := range pending {
				releaseWindowBuildResult(ready)
				delete(pending, pendingIndex)
			}
			continue
		}
		pending[result.index] = result
		for {
			ready, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			descriptor := ready.borrowed.Descriptor
			if descriptor.PayloadSize != 0 {
				descriptor.PayloadOffset = uint64(payloadOffset)
				if err := ready.borrowed.WritePayloadAt(uint64(payloadOffset)); err != nil {
					firstErr = err
					cancel()
				} else {
					payloadOffset += int64(descriptor.PayloadSize)
				}
			}
			if firstErr == nil {
				descriptors[next] = descriptor
				next++
			}
			releaseWindowBuildResult(ready)
			if firstErr != nil {
				for pendingIndex, pendingResult := range pending {
					releaseWindowBuildResult(pendingResult)
					delete(pending, pendingIndex)
				}
				break
			}
		}
	}
	for _, ready := range pending {
		releaseWindowBuildResult(ready)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if next != count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("V4 window pipeline completed %d of %d windows", next, count)
	}
	if _, err := output.Seek(payloadOffset, io.SeekStart); err != nil {
		return nil, err
	}
	return descriptors, nil
}

type builtWindow struct {
	descriptor patchformat.WindowDescriptor
	payload    []byte
}

func buildWindowSet(ctx context.Context, source, target *os.File, sourceSize, targetSize uint64, windowSize uint32, level int, mode OptimizationMode, workers int) ([]builtWindow, error) {
	if targetSize == 0 {
		return nil, nil
	}
	count := int((targetSize + uint64(windowSize) - 1) / uint64(windowSize))
	result := make([]builtWindow, count)
	err := parallelFor(ctx, count, workers, func(ctx context.Context, index int) error {
		offset := uint64(index) * uint64(windowSize)
		size := windowSize
		if remaining := targetSize - offset; remaining < uint64(size) {
			size = uint32(remaining)
		}
		session, err := nativev4.NewSession(source, target, nil)
		if err != nil {
			return err
		}
		defer session.Close()
		built, err := session.BuildWindow(ctx, sourceSize, targetSize, offset, size, windowSize, level, mode)
		if err != nil {
			return err
		}
		result[index] = builtWindow{built.Descriptor, built.Payload}
		return nil
	})
	return result, err
}
func writeWindowPayloads(output *os.File, windows []builtWindow) error {
	for i := range windows {
		if len(windows[i].payload) == 0 {
			continue
		}
		offset, err := output.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		windows[i].descriptor.PayloadOffset = uint64(offset)
		if _, err = output.Write(windows[i].payload); err != nil {
			return err
		}
	}
	return nil
}
func descriptors(values []builtWindow) []patchformat.WindowDescriptor {
	result := make([]patchformat.WindowDescriptor, len(values))
	for i := range values {
		result[i] = values[i].descriptor
	}
	return result
}
