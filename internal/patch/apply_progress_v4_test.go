package patch

import (
	"math"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestV4ApplyProgressWeightsFilesAndAdvancesOverall(t *testing.T) {
	var events []progress.Event
	tracker := newApplyProgress(func(event progress.Event) {
		events = append(events, event)
	}, []patchformat.FileEntry{
		{Path: "small.bin", TargetSize: 100},
		{Path: "large.bin", TargetSize: 300},
	}, Forward)

	tracker.callbackForFile(0)(progress.Event{
		FileIndex:      1,
		FileCount:      2,
		Path:           "small.bin",
		ProcessedBytes: 50,
		TotalBytes:     100,
		Stage:          progress.StageApplying,
	})
	tracker.callbackForFile(1)(progress.Event{
		FileIndex:      2,
		FileCount:      2,
		Path:           "large.bin",
		ProcessedBytes: 150,
		TotalBytes:     300,
		Stage:          progress.StageApplying,
	})
	tracker.markPrepared(0, 2, "small.bin")
	tracker.markPrepared(1, 2, "large.bin")

	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4", len(events))
	}
	last := 0.0
	for index, event := range events {
		if event.Overall <= 0 {
			t.Fatalf("event %d overall = %f, want positive progress", index, event.Overall)
		}
		if event.Overall < last {
			t.Fatalf("event %d overall regressed from %f to %f", index, last, event.Overall)
		}
		last = event.Overall
	}
	if math.Abs(events[len(events)-1].Overall-applyPreparationProgressShare) > 1e-12 {
		t.Fatalf("final preparation overall = %f, want %f", events[len(events)-1].Overall, applyPreparationProgressShare)
	}
}

func TestV4ApplyProgressUsesReverseOutputSize(t *testing.T) {
	var actual progress.Event
	tracker := newApplyProgress(func(event progress.Event) {
		actual = event
	}, []patchformat.FileEntry{{SourceSize: 20, TargetSize: 100}}, Reverse)

	tracker.callbackForFile(0)(progress.Event{
		ProcessedBytes: 10,
		TotalBytes:     20,
		Stage:          progress.StageApplying,
	})

	want := applyPreparationProgressShare / 2
	if math.Abs(actual.Overall-want) > 1e-12 {
		t.Fatalf("overall = %f, want %f", actual.Overall, want)
	}
}

func TestV4ApplyProgressCompletesEmptyFiles(t *testing.T) {
	var actual progress.Event
	tracker := newApplyProgress(func(event progress.Event) {
		actual = event
	}, []patchformat.FileEntry{{Path: "empty.bin"}}, Forward)

	tracker.markPrepared(0, 1, "empty.bin")
	if actual.Stage != progress.StageFilePrepared {
		t.Fatalf("stage = %q, want %q", actual.Stage, progress.StageFilePrepared)
	}
	if actual.Overall != applyPreparationProgressShare {
		t.Fatalf("overall = %f, want %f", actual.Overall, applyPreparationProgressShare)
	}
}
