package patch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type ApplyOptions struct {
	PatchPath, Root   string
	Direction         Direction
	ExpectedPatchHash string
	WorkerBudget      int
	Verify            VerifyMode
	Durability        DurabilityMode
	IOProfile         IOProfile
}
type PreparedApplyOptions struct {
	Root         string
	Direction    Direction
	WorkerBudget int
	Verify       VerifyMode
	Durability   DurabilityMode
	IOProfile    IOProfile
}

func Apply(ctx context.Context, patchPath, root string, direction Direction, callback progress.Callback) error {
	return ApplyWithOptions(ctx, ApplyOptions{PatchPath: patchPath, Root: root, Direction: direction}, callback)
}
func ApplyWithOptions(ctx context.Context, options ApplyOptions, callback progress.Callback) error {
	verify, durability, profile, err := normalizeApplyModes(options.Verify, options.Durability, options.IOProfile)
	if err != nil {
		return err
	}
	opened, err := openPatch(options.PatchPath, options.ExpectedPatchHash, options.ExpectedPatchHash != "")
	if err != nil {
		return err
	}
	return applyOpened(ctx, opened, opened.Close, options.Root, options.Direction, options.WorkerBudget, verify, durability, profile, callback)
}
func ApplyPreparedWithOptions(ctx context.Context, prepared *PreparedPatch, options PreparedApplyOptions, callback progress.Callback) error {
	verify, durability, profile, err := normalizeApplyModes(options.Verify, options.Durability, options.IOProfile)
	if err != nil {
		return err
	}
	opened, release, err := prepared.acquire()
	if err != nil {
		return err
	}
	return applyOpened(ctx, opened, release, options.Root, options.Direction, options.WorkerBudget, verify, durability, profile, callback)
}
func normalizeApplyModes(verify VerifyMode, durability DurabilityMode, profile IOProfile) (VerifyMode, DurabilityMode, IOProfile, error) {
	normalizedVerify, err := ParseVerifyMode(string(verify))
	if err != nil {
		return "", "", "", err
	}
	normalizedDurability, err := ParseDurabilityMode(string(durability))
	if err != nil {
		return "", "", "", err
	}
	normalizedProfile, err := ParseIOProfile(string(profile))
	if err != nil {
		return "", "", "", err
	}
	return normalizedVerify, normalizedDurability, normalizedProfile, nil
}

func applyOpened(ctx context.Context, opened *openedPatch, release func() error, rootPath string, direction Direction, workers int, verify VerifyMode, durability DurabilityMode, profile IOProfile, callback progress.Callback) (resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if direction != Forward && direction != Reverse {
		return fmt.Errorf("unsupported direction %q", direction)
	}
	if direction == Reverse && !opened.parsed.Header.Reverse {
		return fmt.Errorf("patch has no reverse data")
	}
	root, err := openInstallationRoot(rootPath)
	if err != nil {
		return errors.Join(err, release())
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close(), release()) }()

	prepared := make([]preparedFile, len(opened.parsed.Header.Files))
	fileWorkers, perFileWorkers := applicationWorkers(profile, effectiveWorkers(workers), len(prepared))
	operationErr := parallelFor(ctx, len(prepared), fileWorkers, func(ctx context.Context, index int) error {
		entry := opened.parsed.Header.Files[index]
		item, err := prepareApplicationFile(ctx, opened, root, entry, direction, perFileWorkers, verify, durability, profile, index, len(prepared), callback)
		if err != nil {
			return fmt.Errorf("prepare %q: %w", entry.Path, err)
		}
		prepared[index] = item
		return nil
	})
	if operationErr != nil {
		for i := range prepared {
			if prepared[i].source != nil {
				_ = prepared[i].source.Close()
			}
			if prepared[i].temp != "" {
				_ = root.root.Remove(prepared[i].temp)
			}
		}
		return operationErr
	}
	progress.Report(callback, progress.Event{Stage: progress.StagePreparing, Overall: .95})
	commitErr := commitPrepared(root, prepared, durability)
	if commitErr != nil && !IsCommittedWarning(commitErr) {
		return commitErr
	}
	for i, item := range prepared {
		progress.Report(callback, progress.Event{FileIndex: i + 1, FileCount: len(prepared), Path: filepath.ToSlash(item.path), Stage: progress.StageFileCompleted, Overall: float64(i+1) / float64(len(prepared))})
	}
	progress.Report(callback, progress.Event{FileIndex: len(prepared), FileCount: len(prepared), Stage: progress.StageCompleted, Overall: 1})
	return commitErr
}

func nativeIOProfile(profile IOProfile) nativev4.IOProfile {
	switch profile {
	case IOHDD:
		return nativev4.IOHDD
	case IOSSD:
		return nativev4.IOSSD
	case IONVMe:
		return nativev4.IONVMe
	default:
		return nativev4.IOAuto
	}
}

func applicationWorkers(profile IOProfile, requested, fileCount int) (fileWorkers, perFileWorkers int) {
	if requested < 1 {
		requested = 1
	}
	switch profile {
	case IOHDD:
		// Avoid seek amplification and competing positional reads on rotational media.
		return 1, 1
	case IOSSD:
		requested = min(requested, 8)
	case IOAuto, IONVMe:
		// Auto follows the process-aware worker budget; NVMe deliberately keeps it.
	default:
		requested = min(requested, 4)
	}
	if fileCount <= 1 {
		return 1, requested
	}
	if requested <= fileCount {
		return requested, 1
	}
	fileWorkers = min(fileCount, max(1, requested/2))
	perFileWorkers = max(1, requested/fileWorkers)
	return fileWorkers, perFileWorkers
}

func directionData(entry patchformat.FileEntry, direction Direction) (inputSize, outputSize uint64, inputRoot, outputRoot patchformat.Digest, inputChunks, outputChunks []patchformat.Digest, windows []patchformat.WindowDescriptor) {
	if direction == Reverse {
		return entry.TargetSize, entry.SourceSize, entry.TargetDigest, entry.SourceDigest, entry.TargetChunks, entry.SourceChunks, entry.ReverseWindows
	}
	return entry.SourceSize, entry.TargetSize, entry.SourceDigest, entry.TargetDigest, entry.SourceChunks, entry.TargetChunks, entry.ForwardWindows
}

func prepareApplicationFile(ctx context.Context, opened *openedPatch, root *installationRoot, entry patchformat.FileEntry, direction Direction, workerBudget int, verify VerifyMode, durability DurabilityMode, profile IOProfile, index, count int, callback progress.Callback) (preparedFile, error) {
	source, identity, targetName, err := root.openStableRegularFile(entry.Path)
	if err != nil {
		return preparedFile{}, err
	}
	inputSize, outputSize, inputRoot, _, inputChunks, outputChunks, windows := directionData(entry, direction)
	if identity.Size() < 0 || uint64(identity.Size()) != inputSize {
		source.Close()
		return preparedFile{}, fmt.Errorf("installed file has wrong size")
	}
	verification := nativev4.NewSourceVerification(inputChunks, false)
	sourceStrictlyVerified := false
	if verify == VerifyStrict {
		session, sessionErr := nativev4.NewSession(source, nil, nil)
		if sessionErr != nil {
			source.Close()
			return preparedFile{}, sessionErr
		}
		actual, _, hashErr := session.HashFileTree(ctx, false, inputSize, patchformat.IdentityChunkSize)
		session.Close()
		if hashErr != nil {
			source.Close()
			return preparedFile{}, hashErr
		}
		if actual != inputRoot {
			source.Close()
			return preparedFile{}, fmt.Errorf("source BLAKE3 tree mismatch")
		}
		verification = nativev4.NewSourceVerification(inputChunks, true)
		sourceStrictlyVerified = true
	} else if verify == VerifyOutput {
		verification = nil
	}

	output, temporaryName, err := createRootTemp(root.root, filepath.Dir(targetName), ".viper-v4-output-")
	if err != nil {
		source.Close()
		return preparedFile{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = output.Close()
			_ = root.root.Remove(temporaryName)
		}
	}()

	patchSession, err := nativev4.NewSessionWithProfile(source, opened.file, output, nativeIOProfile(profile))
	if err != nil {
		source.Close()
		return preparedFile{}, err
	}
	defer patchSession.Close()

	cloned := false
	if cloneWorthTrying(windows, inputSize, outputSize) {
		cloneErr := patchSession.CloneOutput(outputSize)
		if cloneErr == nil {
			cloned = true
		} else if !nativev4.IsUnsupported(cloneErr) {
			source.Close()
			return preparedFile{}, cloneErr
		}
	}
	if cloned && !sourceStrictlyVerified {
		// A clone inherits every unchanged byte from the installed source. Verify
		// that source once, then skip all SAME windows without rereading them.
		actual, _, hashErr := patchSession.HashFileTree(ctx, false, inputSize, patchformat.IdentityChunkSize)
		if hashErr != nil {
			source.Close()
			return preparedFile{}, hashErr
		}
		if actual != inputRoot {
			source.Close()
			return preparedFile{}, fmt.Errorf("source BLAKE3 tree mismatch")
		}
		verification = nativev4.NewSourceVerification(inputChunks, true)
		sourceStrictlyVerified = true
	}
	if !cloned {
		if err = patchSession.SetOutputSize(outputSize); err != nil {
			source.Close()
			return preparedFile{}, err
		}
	}
	if cloned {
		err = applyClonedWindows(ctx, patchSession, windows, inputSize, verification, workerBudget, index, count, entry.Path, callback)
	} else {
		err = applyWindowGroups(ctx, patchSession, windows, inputSize, outputSize, outputChunks, verification, workerBudget, index, count, entry.Path, callback)
	}
	if err != nil {
		source.Close()
		return preparedFile{}, err
	}
	if err = output.Chmod(identity.Mode().Perm()); err != nil {
		source.Close()
		return preparedFile{}, err
	}
	if durability == DurabilityDurable {
		if err = patchSession.FlushOutput(); err != nil {
			source.Close()
			return preparedFile{}, err
		}
	}
	if err = output.Close(); err != nil {
		source.Close()
		return preparedFile{}, err
	}
	cleanup = false
	return preparedFile{path: targetName, temp: temporaryName, source: source, identity: identity}, nil
}

func cloneWorthTrying(windows []patchformat.WindowDescriptor, inputSize, outputSize uint64) bool {
	if inputSize != outputSize || outputSize < 1<<20 {
		return false
	}
	var same uint64
	for _, window := range windows {
		if window.Kind == patchformat.WindowSame {
			same += uint64(window.OutputSize)
		}
	}
	return same*100 >= outputSize*90
}
func applyClonedWindows(ctx context.Context, session *nativev4.Session, windows []patchformat.WindowDescriptor, sourceSize uint64, verification *nativev4.SourceVerification, workers, index, count int, path string, callback progress.Callback) error {
	changed := make([]int, 0, len(windows))
	var unchanged uint64
	for i := range windows {
		if windows[i].Kind == patchformat.WindowSame {
			unchanged += uint64(windows[i].OutputSize)
			continue
		}
		changed = append(changed, i)
	}
	var completed atomicCounter
	completed.value.Store(unchanged)
	if unchanged != 0 {
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: path, ProcessedBytes: unchanged, TotalBytes: sumWindowBytes(windows), Stage: progress.StageApplying})
	}
	return parallelFor(ctx, len(changed), workers, func(ctx context.Context, job int) error {
		i := changed[job]
		_, err := session.ApplyChangedWindow(ctx, windows[i], sourceSize, verification)
		if err == nil {
			done := completed.Add(uint64(windows[i].OutputSize))
			progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: path, ProcessedBytes: done, TotalBytes: sumWindowBytes(windows), Stage: progress.StageApplying})
		}
		return err
	})
}
func applyWindowGroups(ctx context.Context, session *nativev4.Session, windows []patchformat.WindowDescriptor, sourceSize, outputSize uint64, outputChunks []patchformat.Digest, verification *nativev4.SourceVerification, workers, index, count int, path string, callback progress.Callback) error {
	if outputSize == 0 {
		return nil
	}
	groups := groupWindows(windows, outputSize)
	if len(groups) != len(outputChunks) {
		return fmt.Errorf("output digest group count mismatch")
	}
	var completed atomicCounter
	return parallelFor(ctx, len(groups), workers, func(ctx context.Context, i int) error {
		group := groups[i]
		_, err := session.ApplyGroup(ctx, windows[group.first:group.last], group.offset, group.size, sourceSize, verification, outputChunks[i])
		if err == nil {
			done := completed.Add(uint64(group.size))
			progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: path, ProcessedBytes: done, TotalBytes: outputSize, Stage: progress.StageApplying})
		}
		return err
	})
}

type windowGroup struct {
	first, last int
	offset      uint64
	size        uint32
}

func groupWindows(windows []patchformat.WindowDescriptor, outputSize uint64) []windowGroup {
	if outputSize == 0 {
		return nil
	}
	var groups []windowGroup
	first := 0
	for chunk := uint64(0); chunk < outputSize; chunk += patchformat.IdentityChunkSize {
		end := min(chunk+patchformat.IdentityChunkSize, outputSize)
		last := first
		for last < len(windows) && windows[last].OutputOffset < uint64(end) {
			last++
		}
		groups = append(groups, windowGroup{first, last, chunk, uint32(end - chunk)})
		first = last
	}
	return groups
}
func sumWindowBytes(windows []patchformat.WindowDescriptor) uint64 {
	var total uint64
	for _, window := range windows {
		total += uint64(window.OutputSize)
	}
	return total
}

type atomicCounter struct{ value atomic.Uint64 }

func (c *atomicCounter) Add(value uint64) uint64 { return c.value.Add(value) }
