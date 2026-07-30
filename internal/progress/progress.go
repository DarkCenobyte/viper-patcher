package progress

import (
	"math"
	"sync"
)

// Stage identifies one phase of a patch creation or application operation.
type Stage string

const (
	StagePreparing          Stage = "preparing"
	StageSnapshotting       Stage = "snapshotting"
	StageCompressingForward Stage = "compressing-forward"
	StageCompressingReverse Stage = "compressing-reverse"
	StageApplying           Stage = "applying"
	StageVerifying          Stage = "verifying"
	StageFilePrepared       Stage = "file-prepared"
	StageFileCompleted      Stage = "file-completed"
	StageCompleted          Stage = "completed"
)

// Event reports progress for one file in a multi-file operation.
type Event struct {
	FileIndex      int
	FileCount      int
	Path           string
	ProcessedBytes uint64
	TotalBytes     uint64
	Stage          Stage
	// Overall is the monotonic weighted progress of the complete operation in
	// the inclusive range 0..1.
	Overall float64
}

// Callback receives serialized progress events.
type Callback func(Event)

// Serialize enforces the Callback contract for producers that report from
// several workers. It also clamps invalid values and prevents Overall from
// moving backwards when independently completed work arrives out of order.
func Serialize(callback Callback) Callback {
	if callback == nil {
		return nil
	}
	var mutex sync.Mutex
	lastOverall := 0.0
	return func(event Event) {
		mutex.Lock()
		defer mutex.Unlock()
		if math.IsNaN(event.Overall) || math.IsInf(event.Overall, 0) {
			event.Overall = lastOverall
		}
		if event.Overall < 0 {
			event.Overall = 0
		} else if event.Overall > 1 {
			event.Overall = 1
		}
		if event.Overall < lastOverall {
			event.Overall = lastOverall
		} else {
			lastOverall = event.Overall
		}
		callback(event)
	}
}

// Report invokes callback when it is not nil.
func Report(callback Callback, event Event) {
	if callback != nil {
		callback(event)
	}
}
