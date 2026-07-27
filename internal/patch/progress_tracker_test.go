package patch

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestCreationProgressIsAggregatedAndMonotone(t *testing.T) {
	pairs := []plannedPair{
		{relativePath: "one.bin", sourceSize: 100, targetSize: 100},
		{relativePath: "two.bin", sourceSize: 100, targetSize: 300},
	}
	var values []float64
	callback := newCreationProgress(func(event progress.Event) {
		values = append(values, event.Overall)
	}, pairs, false)
	callback(progress.Event{FileIndex: 2, FileCount: 2, Stage: progress.StageSnapshotting, ProcessedBytes: 200, TotalBytes: 400})
	callback(progress.Event{FileIndex: 1, FileCount: 2, Stage: progress.StageSnapshotting, ProcessedBytes: 10, TotalBytes: 200})
	callback(progress.Event{FileIndex: 2, FileCount: 2, Stage: progress.StageFilePrepared})
	callback(progress.Event{FileIndex: 1, FileCount: 2, Stage: progress.StageFilePrepared})
	callback(progress.Event{Stage: progress.StageCompleted})
	for index := 1; index < len(values); index++ {
		if values[index] < values[index-1] {
			t.Fatalf("progress regressed at %d: %v", index, values)
		}
	}
	if values[len(values)-1] != 1 {
		t.Fatalf("final progress = %v", values[len(values)-1])
	}
}

func TestApplicationProgressWeightsVerificationAndOutput(t *testing.T) {
	entries := []patchformat.FileEntry{{
		SourceSize:    100,
		TargetSize:    300,
		ForwardMethod: patchformat.MethodReplace,
	}}
	var last progress.Event
	callback := newApplicationProgress(func(event progress.Event) { last = event }, entries, Forward)
	callback(progress.Event{FileIndex: 1, FileCount: 1, Stage: progress.StageVerifying, ProcessedBytes: 50, TotalBytes: 100})
	if last.Overall != 0.125 {
		t.Fatalf("verification progress = %v, want 0.125", last.Overall)
	}
	callback(progress.Event{FileIndex: 1, FileCount: 1, Stage: progress.StageApplying, ProcessedBytes: 150, TotalBytes: 300})
	if last.Overall != 0.5 {
		t.Fatalf("combined progress = %v, want 0.5", last.Overall)
	}
}

func TestNilProgressCallbacksRemainNil(t *testing.T) {
	if newCreationProgress(nil, nil, false) != nil {
		t.Fatal("nil creation callback must remain nil")
	}
	if newApplicationProgress(nil, nil, Forward) != nil {
		t.Fatal("nil application callback must remain nil")
	}
}
