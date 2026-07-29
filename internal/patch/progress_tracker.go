//go:build ignore

package patch

import (
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type weightedPhase struct {
	weight   float64
	fraction float64
}

type trackedFileProgress struct {
	snapshot weightedPhase
	forward  weightedPhase
	reverse  weightedPhase
	verify   weightedPhase
	apply    weightedPhase
}

type operationProgressTracker struct {
	mutex           sync.Mutex
	callback        progress.Callback
	files           []trackedFileProgress
	totalWeight     float64
	completedWeight float64
	completed       bool
	lastOverall     float64
}

func newCreationProgress(callback progress.Callback, pairs []plannedPair, includeReverse bool) progress.Callback {
	if callback == nil {
		return nil
	}
	tracker := &operationProgressTracker{
		callback: callback,
		files:    make([]trackedFileProgress, len(pairs)),
	}
	for index, pair := range pairs {
		snapshotSize := pair.sourceSize + pair.targetSize
		tracker.files[index].snapshot.weight = progressWeight(snapshotSize)
		tracker.files[index].forward.weight = progressWeight(pair.targetSize)
		if includeReverse {
			tracker.files[index].reverse.weight = progressWeight(pair.sourceSize)
		}
		tracker.totalWeight += tracker.files[index].totalWeight()
	}
	return tracker.report
}

func newApplicationProgress(callback progress.Callback, entries []patchformat.FileEntry, direction Direction) progress.Callback {
	if callback == nil {
		return nil
	}
	tracker := &operationProgressTracker{
		callback: callback,
		files:    make([]trackedFileProgress, len(entries)),
	}
	for index, entry := range entries {
		_, _, _, method, input, output := differential(entry, direction)
		if method != patchformat.MethodSparse {
			tracker.files[index].verify.weight = progressWeight(input.size)
		}
		tracker.files[index].apply.weight = progressWeight(output.size)
		tracker.totalWeight += tracker.files[index].totalWeight()
	}
	return tracker.report
}

func progressWeight(size uint64) float64 {
	if size == 0 {
		return 1
	}
	return float64(size)
}

func eventFraction(event progress.Event) float64 {
	if event.TotalBytes == 0 {
		if event.ProcessedBytes > 0 {
			return 1
		}
		return 0
	}
	fraction := float64(event.ProcessedBytes) / float64(event.TotalBytes)
	if fraction < 0 {
		return 0
	}
	if fraction > 1 {
		return 1
	}
	return fraction
}

func (file *trackedFileProgress) totalWeight() float64 {
	return file.snapshot.weight + file.forward.weight + file.reverse.weight + file.verify.weight + file.apply.weight
}

func (file *trackedFileProgress) completedWeight() float64 {
	return file.snapshot.weight*file.snapshot.fraction +
		file.forward.weight*file.forward.fraction +
		file.reverse.weight*file.reverse.fraction +
		file.verify.weight*file.verify.fraction +
		file.apply.weight*file.apply.fraction
}

func updatePhase(phase *weightedPhase, fraction float64) {
	if fraction > phase.fraction {
		phase.fraction = fraction
	}
}

func completeFile(file *trackedFileProgress) {
	file.snapshot.fraction = 1
	file.forward.fraction = 1
	file.reverse.fraction = 1
	file.verify.fraction = 1
	file.apply.fraction = 1
}

func (tracker *operationProgressTracker) report(event progress.Event) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()

	if event.Stage == progress.StageCompleted {
		tracker.completedWeight = tracker.totalWeight
		tracker.completed = true
	} else if !tracker.completed && event.FileIndex > 0 && event.FileIndex <= len(tracker.files) {
		file := &tracker.files[event.FileIndex-1]
		previousWeight := file.completedWeight()
		fraction := eventFraction(event)
		switch event.Stage {
		case progress.StageSnapshotting:
			updatePhase(&file.snapshot, fraction)
		case progress.StageCompressingForward:
			updatePhase(&file.forward, fraction)
		case progress.StageCompressingReverse:
			updatePhase(&file.reverse, fraction)
		case progress.StageVerifying:
			updatePhase(&file.verify, fraction)
		case progress.StageApplying:
			updatePhase(&file.apply, fraction)
		case progress.StageFilePrepared, progress.StageFileCompleted:
			completeFile(file)
		}
		tracker.completedWeight += file.completedWeight() - previousWeight
	}

	overall := 0.0
	if tracker.totalWeight > 0 {
		overall = tracker.completedWeight / tracker.totalWeight
	}
	if event.Stage == progress.StageCompleted {
		overall = 1
	}
	if overall < tracker.lastOverall {
		overall = tracker.lastOverall
	}
	if overall > 1 {
		overall = 1
	}
	tracker.lastOverall = overall
	event.Overall = overall
	tracker.callback(event)
}
