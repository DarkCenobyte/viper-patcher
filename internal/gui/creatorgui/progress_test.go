package creatorgui

import (
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestCreatorProgressPresentation(t *testing.T) {
	event := progress.Event{
		FileIndex:      1,
		FileCount:      2,
		Path:           "bin/game.exe",
		Stage:          progress.StageCompressingForward,
		ProcessedBytes: 50,
		TotalBytes:     100,
	}
	if text := creatorProgressText(event); !strings.Contains(text, "forward differential") || !strings.Contains(text, event.Path) {
		t.Fatalf("unexpected progress text: %s", text)
	}
	if value := creatorOverallProgress(event, false); value != 0.375 {
		t.Fatalf("progress = %v, want 0.375", value)
	}
	if value := creatorOverallProgress(progress.Event{Stage: progress.StageCompleted}, true); value != 1 {
		t.Fatalf("completed progress = %v, want 1", value)
	}
}

func TestIntegerOptions(t *testing.T) {
	options := integerOptions(2, 4)
	if len(options) != 3 || options[0] != "2" || options[2] != "4" {
		t.Fatalf("unexpected options: %v", options)
	}
}
