package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	WindowSize       uint32
	Optimization     OptimizationMode
}

type CreateEstimate struct{ TotalBytes, WorkDirectoryBytes, OutputDirectoryBytes uint64 }

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
		if !a.Mode().IsRegular() || !b.Mode().IsRegular() {
			return CreateEstimate{}, fmt.Errorf("creation inputs must be regular files")
		}
		source += uint64(a.Size())
		target += uint64(b.Size())
	}
	work := source + target
	output := target + target/4 + 64<<20
	if options.CreateReverse {
		output += source + source/4
	}
	return CreateEstimate{work + output, work, output}, nil
}

type snapshotPair struct {
	path, sourcePath, targetPath string
	sourceSize, targetSize       uint64
}

func Create(ctx context.Context, options CreateOptions, callback progress.Callback) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCreateOptions(options); err != nil {
		return err
	}
	if version := nativev4.ZstdVersion(); version != patchformat.SupportedZstdVersion {
		return fmt.Errorf("Viper-Patcher V4 requires libzstd %s, linked version is %s", patchformat.SupportedZstdVersion, version)
	}
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
	snapshots, snapshotErr := createSnapshots(ctx, options.Files, work, callback)
	if snapshotErr != nil {
		_ = os.RemoveAll(work)
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
		_ = os.RemoveAll(work)
	}()
	if err := patchformat.WritePrefix(output, boolFlag(options.CreateReverse)); err != nil {
		return err
	}
	header := patchformat.Header{FormatVersion: patchformat.FormatVersion, CreatedAt: time.Now().UTC(), Creator: patchformat.CreatorInfo{Name: "Viper-Patcher", Version: buildinfo.Version, Commit: buildinfo.Commit, BuildDate: buildinfo.BuildDate}, Comment: options.Comment, HashAlgorithm: patchformat.HashBLAKE3Tree, Compression: patchformat.Compression{Algorithm: patchformat.AlgorithmHybrid, Library: nativev4.ZstdVersion(), Mode: patchformat.CompressionHybrid, Level: options.CompressionLevel}, Reverse: options.CreateReverse, DefaultWindowSize: defaultWindowSize(options.WindowSize), Optimization: options.Optimization, Files: make([]patchformat.FileEntry, len(snapshots))}
	for index := range snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := createFileEntry(ctx, output, snapshots[index], options, effectiveWorkers(options.WorkerBudget), index, len(snapshots), callback)
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
	backup := options.OutputPath + ".viper-backup"
	_ = os.Remove(backup)
	if _, err = os.Stat(options.OutputPath); err == nil {
		if err = os.Rename(options.OutputPath, backup); err != nil {
			return err
		}
	}
	if err = os.Rename(tempName, options.OutputPath); err != nil {
		_ = os.Rename(backup, options.OutputPath)
		return err
	}
	committed = true
	directorySyncError := syncCreatedPatchDirectory(options.OutputPath)
	cleanup := os.Remove(backup)
	if os.IsNotExist(cleanup) {
		cleanup = nil
	}
	progress.Report(callback, progress.Event{FileIndex: len(snapshots), FileCount: len(snapshots), Stage: progress.StageCompleted, Overall: 1})
	return committedWarning("patch creation", directorySyncError, cleanup)
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

func createSnapshots(ctx context.Context, pairs []FilePair, work string, callback progress.Callback) ([]snapshotPair, error) {
	sources := make([]string, len(pairs))
	for i := range pairs {
		sources[i] = pairs[i].SourcePath
	}
	base, err := pathutil.CommonBase(sources)
	if err != nil {
		return nil, err
	}
	result := make([]snapshotPair, len(pairs))
	for i, pair := range pairs {
		relative, err := pathutil.RelativePatchPath(base, pair.SourcePath)
		if err != nil {
			return nil, err
		}
		sourcePath := filepath.Join(work, fmt.Sprintf("%06d-source", i))
		targetPath := filepath.Join(work, fmt.Sprintf("%06d-target", i))
		sourceSize, err := copySnapshot(ctx, pair.SourcePath, sourcePath)
		if err != nil {
			return nil, err
		}
		targetSize, err := copySnapshot(ctx, pair.TargetPath, targetPath)
		if err != nil {
			return nil, err
		}
		result[i] = snapshotPair{relative, sourcePath, targetPath, sourceSize, targetSize}
		progress.Report(callback, progress.Event{FileIndex: i + 1, FileCount: len(pairs), Path: relative, ProcessedBytes: sourceSize + targetSize, TotalBytes: sourceSize + targetSize, Stage: progress.StageSnapshotting})
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

func createFileEntry(ctx context.Context, output *os.File, snapshot snapshotPair, options CreateOptions, workers, index, count int, callback progress.Callback) (patchformat.FileEntry, error) {
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
	sourceRoot, sourceChunks, err := hashSession.HashFileTree(ctx, false, snapshot.sourceSize, patchformat.IdentityChunkSize)
	if err != nil {
		hashSession.Close()
		return patchformat.FileEntry{}, err
	}
	targetRoot, targetChunks, err := hashSession.HashFileTree(ctx, true, snapshot.targetSize, patchformat.IdentityChunkSize)
	hashSession.Close()
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	windowSize := options.WindowSize
	if windowSize == 0 {
		windowSize = automaticWindowSize(max64(snapshot.sourceSize, snapshot.targetSize))
	}
	forward, err := buildWindowSet(ctx, source, target, snapshot.sourceSize, snapshot.targetSize, windowSize, options.CompressionLevel, options.Optimization, workers)
	if err != nil {
		return patchformat.FileEntry{}, err
	}
	if err = writeWindowPayloads(output, forward); err != nil {
		return patchformat.FileEntry{}, err
	}
	var reverse []builtWindow
	if options.CreateReverse {
		reverse, err = buildWindowSet(ctx, target, source, snapshot.targetSize, snapshot.sourceSize, windowSize, options.CompressionLevel, options.Optimization, workers)
		if err != nil {
			return patchformat.FileEntry{}, err
		}
		if err = writeWindowPayloads(output, reverse); err != nil {
			return patchformat.FileEntry{}, err
		}
	}
	progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: snapshot.path, ProcessedBytes: snapshot.targetSize, TotalBytes: snapshot.targetSize, Stage: progress.StageCompressingForward})
	return patchformat.FileEntry{Path: snapshot.path, SourceHash: sourceRoot.Hex(), TargetHash: targetRoot.Hex(), SourceSize: snapshot.sourceSize, TargetSize: snapshot.targetSize, SourceDigest: sourceRoot, TargetDigest: targetRoot, WindowSize: windowSize, SourceChunkSize: uint32(patchformat.IdentityChunkSize), SourceChunks: sourceChunks, TargetChunks: targetChunks, ForwardWindows: descriptors(forward), ReverseWindows: descriptors(reverse)}, nil
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
