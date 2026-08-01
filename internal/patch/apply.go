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
	MemoryLimit       uint64
	Verify            VerifyMode
	Durability        DurabilityMode
	IOProfile         IOProfile
}
type PreparedApplyOptions struct {
	Root         string
	Direction    Direction
	WorkerBudget int
	MemoryLimit  uint64
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
	return applyOpened(ctx, opened, opened.Close, options.Root, options.Direction, options.WorkerBudget, options.MemoryLimit, verify, durability, profile, callback)
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
	return applyOpened(ctx, opened, release, options.Root, options.Direction, options.WorkerBudget, options.MemoryLimit, verify, durability, profile, callback)
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

func applyOpened(ctx context.Context, opened *openedPatch, release func() error, rootPath string, direction Direction, workers int, memoryLimit uint64, verify VerifyMode, durability DurabilityMode, profile IOProfile, callback progress.Callback) (resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callback = progress.Serialize(callback)
	requestedWorkers := effectiveWorkers(workers)
	ctx = withOperationScheduler(ctx, profile, requestedWorkers)
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
	if err := recoverApplyTransactions(root, durability); err != nil {
		return errors.Join(err, root.Close(), release())
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close(), release()) }()

	token := nativev4.NewCancelToken(ctx)
	defer token.Close()
	prepared := make([]preparedFile, len(opened.parsed.Header.Files))
	applyProgress := newApplyProgress(callback, opened.parsed.Header.Files, direction)
	resources := newMemoryBudget(memoryLimit, operationApply)
	fileWorkers, perFileWorkers := plannedApplicationConcurrency(
		ctx,
		profile,
		requestedWorkers,
		len(prepared),
		resources,
	)
	operationErr := parallelFor(ctx, len(prepared), fileWorkers, func(ctx context.Context, index int) error {
		entry := opened.parsed.Header.Files[index]
		fileCallback := applyProgress.callbackForFile(index)
		item, err := prepareApplicationFile(ctx, token, opened, root, entry, direction, perFileWorkers, resources, verify, durability, profile, index, len(prepared), fileCallback)
		if err != nil {
			return fmt.Errorf("prepare %q: %w", entry.Path, err)
		}
		prepared[index] = item
		applyProgress.markPrepared(index, len(prepared), entry.Path)
		return nil
	})
	if operationErr != nil {
		for i := range prepared {
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
		progress.Report(callback, progress.Event{FileIndex: i + 1, FileCount: len(prepared), Path: filepath.ToSlash(item.path), Stage: progress.StageFileCompleted, Overall: .95 + .05*float64(i+1)/float64(len(prepared))})
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

func plannedApplicationConcurrency(
	ctx context.Context,
	profile IOProfile,
	requested, fileCount int,
	resources *memoryBudget,
) (fileWorkers, perFileWorkers int) {
	// File coordinators perform opening, validation, temporary-file setup, and
	// other preparation outside the leaf scheduler. Keep their concurrency tied
	// to the process worker budget, while separately limiting each file's native
	// leaf pool to the scheduler's actual CPU/read/write capacity.
	fileWorkers, perFileWorkers = applicationWorkers(profile, requested, fileCount)
	perFileWorkers = scheduledWorkers(ctx, perFileWorkers, applyLeafTaskCost)
	return fitApplicationWorkers(resources, fileWorkers, perFileWorkers)
}

func directionData(entry patchformat.FileEntry, direction Direction) (inputSize, outputSize uint64, inputRoot, outputRoot patchformat.Digest, inputChunks, outputChunks []patchformat.Digest, windows []patchformat.WindowDescriptor) {
	if direction == Reverse {
		return entry.TargetSize, entry.SourceSize, entry.TargetDigest, entry.SourceDigest, entry.TargetChunks, entry.SourceChunks, entry.ReverseWindows
	}
	return entry.SourceSize, entry.TargetSize, entry.SourceDigest, entry.TargetDigest, entry.SourceChunks, entry.TargetChunks, entry.ForwardWindows
}

func prepareApplicationFile(
	ctx context.Context,
	token *nativev4.CancelToken,
	opened *openedPatch,
	root *installationRoot,
	entry patchformat.FileEntry,
	direction Direction,
	workerBudget int,
	resources *memoryBudget,
	verify VerifyMode,
	durability DurabilityMode,
	profile IOProfile,
	index, count int,
	callback progress.Callback,
) (preparedFile, error) {
	inputSize, outputSize, inputRoot, _, inputChunks, outputChunks, windows :=
		directionData(entry, direction)
	plannedWorkers := plannedApplicationWorkers(workerBudget, windows, outputSize)
	reservation := uint64(max(1, plannedWorkers)) * applySessionReservation
	operationLease, err := resources.Acquire(ctx, reservation)
	if err != nil {
		return preparedFile{}, err
	}
	defer operationLease.Release()

	source, identity, targetName, err := root.openStableRegularFile(entry.Path)
	if err != nil {
		return preparedFile{}, err
	}
	defer source.Close()

	if identity.Size() < 0 || uint64(identity.Size()) != inputSize {
		return preparedFile{}, fmt.Errorf("installed file has wrong size")
	}
	if err := validateInstalledMetadata(identity.Mode()); err != nil {
		return preparedFile{}, fmt.Errorf("validate installed metadata: %w", err)
	}

	verification := nativev4.NewSourceVerification(inputChunks, false)
	var sourceCacheLease *memoryLease

	// Close the final verification object before releasing the memory-budget
	// reservation associated with its optional source cache.
	defer func() {
		if verification != nil {
			verification.Close()
		}
		if sourceCacheLease != nil {
			sourceCacheLease.Release()
		}
	}()

	sourceStrictlyVerified := false

	if verify == VerifyStrict {
		session, sessionErr := nativev4.NewSession(source, nil, nil)
		if sessionErr != nil {
			return preparedFile{}, sessionErr
		}

		actual, _, hashErr := session.HashFileTreeWithToken(
			token,
			false,
			inputSize,
			patchformat.IdentityChunkSize,
		)
		closeErr := session.Close()

		if hashErr != nil {
			return preparedFile{}, errors.Join(hashErr, closeErr)
		}
		if closeErr != nil {
			return preparedFile{}, closeErr
		}
		if actual != inputRoot {
			return preparedFile{}, fmt.Errorf("source BLAKE3 tree mismatch")
		}

		verification.Close()
		verification = nativev4.NewSourceVerification(inputChunks, true)
		sourceStrictlyVerified = true
	} else if verify == VerifyOutput {
		verification.Close()
		verification = nil
	}

	if !cloneWorthTrying(windows, inputSize, outputSize) &&
		sourceCacheWorthTrying(profile, windows, inputSize) {
		cacheLease, acquired := resources.TryAcquire(inputSize)
		if acquired {
			if verification == nil {
				verification = nativev4.NewSourceVerification(nil, true)
			}

			cacheErr := verification.LoadSource(
				ctx,
				source,
				inputSize,
				verify == VerifyReferenced,
			)
			if cacheErr != nil {
				cacheLease.Release()
				if !nativev4.IsMemoryLimit(cacheErr) {
					return preparedFile{}, cacheErr
				}
			} else {
				sourceCacheLease = cacheLease
			}
		}
	}

	output, temporaryName, err := createRootTemp(
		root.root,
		filepath.Dir(targetName),
		".viper-v4-output-",
	)
	if err != nil {
		return preparedFile{}, err
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = output.Close()
			_ = root.root.Remove(temporaryName)
		}
	}()

	patchSession, err := nativev4.NewSessionWithProfile(
		source,
		opened.file,
		output,
		nativeIOProfile(profile),
	)
	if err != nil {
		return preparedFile{}, err
	}
	defer func() {
		if patchSession != nil {
			_ = patchSession.Close()
		}
	}()

	cloned := false
	if cloneWorthTrying(windows, inputSize, outputSize) {
		cloneErr := patchSession.CloneOutput(outputSize)
		if cloneErr == nil {
			cloned = true
		} else if !nativev4.IsUnsupported(cloneErr) {
			return preparedFile{}, cloneErr
		}
	}

	if cloned && !sourceStrictlyVerified {
		// A clone inherits every unchanged byte from the installed source.
		// Verify that source once, then skip all SAME windows without
		// rereading them.
		actual, _, hashErr := patchSession.HashFileTreeWithToken(
			token,
			false,
			inputSize,
			patchformat.IdentityChunkSize,
		)
		if hashErr != nil {
			return preparedFile{}, hashErr
		}
		if actual != inputRoot {
			return preparedFile{}, fmt.Errorf("source BLAKE3 tree mismatch")
		}

		if verification != nil {
			verification.Close()
		}
		verification = nativev4.NewSourceVerification(inputChunks, true)
		sourceStrictlyVerified = true
	}

	if !cloned {
		err = patchSession.SetOutputSize(
			outputSize,
			shouldPreallocateOutput(durability, profile),
		)
		if err != nil {
			return preparedFile{}, err
		}
	}

	workCount := applicationWorkCount(cloned, windows, outputSize)
	var pool *nativev4.SessionPool
	closePool := func(cause error) error {
		if pool == nil {
			return cause
		}
		closeErr := pool.Close()
		pool = nil
		return errors.Join(cause, closeErr)
	}
	if workCount != 0 {
		poolSize := min(plannedWorkers, workCount)

		pool, err = nativev4.NewSessionPoolWithInitial(
			patchSession,
			poolSize,
			source,
			opened.file,
			output,
			nativeIOProfile(profile),
		)
		// The pool owns the initial session even when construction fails.
		patchSession = nil
		if err != nil {
			return preparedFile{}, err
		}

		if cloned {
			err = applyClonedWindows(
				ctx,
				token,
				pool,
				windows,
				inputSize,
				verification,
				poolSize,
				index,
				count,
				entry.Path,
				callback,
			)
		} else {
			err = applyWindowGroups(
				ctx,
				token,
				pool,
				windows,
				inputSize,
				outputSize,
				outputChunks,
				verification,
				poolSize,
				index,
				count,
				entry.Path,
				callback,
			)
		}
	}

	if err != nil {
		return preparedFile{}, closePool(err)
	}

	if err = output.Chmod(targetPermissions(identity.Mode())); err != nil {
		return preparedFile{}, closePool(err)
	}

	if durability == DurabilityDurable {
		if pool == nil {
			err = patchSession.FlushOutput()
		} else {
			flushSession, acquireErr := pool.Acquire(context.Background())
			if acquireErr != nil {
				err = acquireErr
			} else {
				err = flushSession.FlushOutput()
				pool.Release(flushSession)
			}
		}
		if err != nil {
			return preparedFile{}, closePool(err)
		}
	}

	if err = closePool(nil); err != nil {
		return preparedFile{}, err
	}

	if err = output.Close(); err != nil {
		return preparedFile{}, err
	}

	cleanup = false

	return preparedFile{
		path:     targetName,
		temp:     temporaryName,
		identity: identity,
	}, nil
}

func sourceCacheWorthTrying(
	profile IOProfile,
	windows []patchformat.WindowDescriptor,
	inputSize uint64,
) bool {
	// Loading the complete source image is intentionally limited to HDD mode.
	// On SSD/NVMe the v0.6.0 benchmark showed that the eager serial read costs
	// more wall time than the positional-I/O path despite reducing syscalls.
	if profile != IOHDD {
		return false
	}

	maximum := uint64(256 << 20)
	if strconvIntSizeRuntime() == 32 {
		maximum = 64 << 20
	}
	if inputSize == 0 || inputSize > maximum {
		return false
	}

	threshold := min(inputSize, max(uint64(8<<20), inputSize/4))
	var referenced uint64
	for _, window := range windows {
		switch window.Kind {
		case patchformat.WindowSame,
			patchformat.WindowCopy,
			patchformat.WindowDeltaRaw,
			patchformat.WindowDeltaZstd:
			if referenced >= threshold {
				return true
			}
			size := uint64(window.SourceSize)
			if size >= threshold-referenced {
				return true
			}
			referenced += size
		}
	}
	return false
}

func shouldPreallocateOutput(durability DurabilityMode, profile IOProfile) bool {
	return durability == DurabilityDurable && (profile == IOSSD || profile == IONVMe)
}

func applicationWorkCount(cloned bool, windows []patchformat.WindowDescriptor, outputSize uint64) int {
	if cloned {
		count := 0
		for _, window := range windows {
			if window.Kind != patchformat.WindowSame {
				count++
			}
		}
		return count
	}
	return len(groupWindows(windows, outputSize))
}

func plannedApplicationWorkers(requested int, windows []patchformat.WindowDescriptor, outputSize uint64) int {
	workCount := applicationWorkCount(false, windows, outputSize)
	if workCount == 0 {
		return 0
	}
	return min(max(1, requested), workCount)
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
func applyClonedWindows(ctx context.Context, token *nativev4.CancelToken, pool *nativev4.SessionPool, windows []patchformat.WindowDescriptor, sourceSize uint64, verification *nativev4.SourceVerification, workers, index, count int, path string, callback progress.Callback) error {
	changed := make([]int, 0, len(windows))
	var unchanged uint64
	totalBytes := sumWindowBytes(windows)
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
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: path, ProcessedBytes: unchanged, TotalBytes: totalBytes, Stage: progress.StageApplying})
	}
	return parallelFor(ctx, len(changed), workers, func(ctx context.Context, job int) error {
		return runScheduled(ctx, applyLeafTaskCost, func() error {
			i := changed[job]
			session, err := pool.Acquire(ctx)
			if err != nil {
				return err
			}
			defer pool.Release(session)
			_, err = session.ApplyChangedWindowWithToken(token, windows[i], sourceSize, verification)
			if err == nil {
				done := completed.Add(uint64(windows[i].OutputSize))
				progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: path, ProcessedBytes: done, TotalBytes: totalBytes, Stage: progress.StageApplying})
			}
			return err
		})
	})
}
func applyWindowGroups(ctx context.Context, token *nativev4.CancelToken, pool *nativev4.SessionPool, windows []patchformat.WindowDescriptor, sourceSize, outputSize uint64, outputChunks []patchformat.Digest, verification *nativev4.SourceVerification, workers, index, count int, path string, callback progress.Callback) error {
	if outputSize == 0 {
		return nil
	}
	groups := groupWindows(windows, outputSize)
	if len(groups) != len(outputChunks) {
		return fmt.Errorf("output digest group count mismatch")
	}
	var completed atomicCounter
	return parallelFor(ctx, len(groups), workers, func(ctx context.Context, i int) error {
		return runScheduled(ctx, applyLeafTaskCost, func() error {
			group := groups[i]
			session, err := pool.Acquire(ctx)
			if err != nil {
				return err
			}
			defer pool.Release(session)
			_, err = session.ApplyGroupWithToken(token, windows[group.first:group.last], group.offset, group.size, sourceSize, verification, outputChunks[i])
			if err == nil {
				done := completed.Add(uint64(group.size))
				progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: count, Path: path, ProcessedBytes: done, TotalBytes: outputSize, Stage: progress.StageApplying})
			}
			return err
		})
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
