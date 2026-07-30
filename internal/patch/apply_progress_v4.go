package patch

import (
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

const applyPreparationProgressShare = 0.95

type applyProgress struct {
	mutex     sync.Mutex
	callback  progress.Callback
	weights   []float64
	completed []float64
	total     float64
	totalDone float64
}

func newApplyProgress(callback progress.Callback, files []patchformat.FileEntry, direction Direction) *applyProgress {
	if callback == nil {
		return nil
	}
	tracker := &applyProgress{
		callback:  callback,
		weights:   make([]float64, len(files)),
		completed: make([]float64, len(files)),
	}
	for index := range files {
		_, outputSize, _, _, _, _, _ := directionData(files[index], direction)
		weight := float64(outputSize)
		if weight < 1 {
			// Empty files still represent preparation and commit work.
			weight = 1
		}
		tracker.weights[index] = weight
		tracker.total += weight
	}
	return tracker
}

func (tracker *applyProgress) callbackForFile(index int) progress.Callback {
	if tracker == nil || tracker.callback == nil {
		return nil
	}
	return func(event progress.Event) {
		tracker.mutex.Lock()
		defer tracker.mutex.Unlock()

		tracker.updateFileLocked(index, event.ProcessedBytes, event.TotalBytes)
		event.Overall = tracker.overallLocked()
		tracker.callback(event)
	}
}

func (tracker *applyProgress) markPrepared(index, count int, path string) {
	if tracker == nil || tracker.callback == nil {
		return
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()

	tracker.completeFileLocked(index)
	tracker.callback(progress.Event{
		FileIndex: index + 1,
		FileCount: count,
		Path:      path,
		Stage:     progress.StageFilePrepared,
		Overall:   tracker.overallLocked(),
	})
}

func (tracker *applyProgress) updateFileLocked(index int, processed, total uint64) {
	if index < 0 || index >= len(tracker.weights) || total == 0 {
		return
	}
	ratio := float64(processed) / float64(total)
	if ratio > 1 {
		ratio = 1
	}
	completed := tracker.weights[index] * ratio
	if completed <= tracker.completed[index] {
		return
	}
	tracker.totalDone += completed - tracker.completed[index]
	tracker.completed[index] = completed
}

func (tracker *applyProgress) completeFileLocked(index int) {
	if index < 0 || index >= len(tracker.weights) {
		return
	}
	if tracker.completed[index] >= tracker.weights[index] {
		return
	}
	tracker.totalDone += tracker.weights[index] - tracker.completed[index]
	tracker.completed[index] = tracker.weights[index]
}

func (tracker *applyProgress) overallLocked() float64 {
	if tracker.total <= 0 {
		return applyPreparationProgressShare
	}
	overall := applyPreparationProgressShare * tracker.totalDone / tracker.total
	if overall < 0 {
		return 0
	}
	if overall > applyPreparationProgressShare {
		return applyPreparationProgressShare
	}
	return overall
}
