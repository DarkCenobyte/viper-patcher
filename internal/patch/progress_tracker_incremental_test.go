package patch

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestProgressTrackerMaintainsIncrementalCompletedWeight(t *testing.T) {
	tracker := &operationProgressTracker{
		callback: func(progress.Event) {},
		files: []trackedFileProgress{{
			snapshot: weightedPhase{weight: 100},
			apply:    weightedPhase{weight: 300},
		}},
		totalWeight: 400,
	}
	tracker.report(progress.Event{FileIndex: 1, Stage: progress.StageSnapshotting, ProcessedBytes: 50, TotalBytes: 100})
	if tracker.completedWeight != 50 {
		t.Fatalf("completed weight = %v, want 50", tracker.completedWeight)
	}
	tracker.report(progress.Event{FileIndex: 1, Stage: progress.StageApplying, ProcessedBytes: 150, TotalBytes: 300})
	if tracker.completedWeight != 200 {
		t.Fatalf("completed weight = %v, want 200", tracker.completedWeight)
	}
	tracker.report(progress.Event{Stage: progress.StageCompleted})
	if tracker.completedWeight != tracker.totalWeight || !tracker.completed {
		t.Fatalf("completed state = (%v, %v), want (%v, true)", tracker.completedWeight, tracker.completed, tracker.totalWeight)
	}
	tracker.report(progress.Event{FileIndex: 1, Stage: progress.StageApplying, ProcessedBytes: 300, TotalBytes: 300})
	if tracker.completedWeight != tracker.totalWeight {
		t.Fatalf("late event changed completed weight to %v", tracker.completedWeight)
	}
}
