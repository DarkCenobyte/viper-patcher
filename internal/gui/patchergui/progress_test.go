package patchergui

import (
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestPatcherValidationPresentation(t *testing.T) {
	result := patch.ValidationResult{
		State:  patch.StateInvalidFiles,
		Issues: []patch.FileIssue{{Path: "bin/game.exe", Reason: patch.IssueHashMismatch}},
	}
	text := patcherValidationText(result)
	if !strings.Contains(text, "bin/game.exe") || !strings.Contains(text, string(patch.IssueHashMismatch)) {
		t.Fatalf("unexpected validation text: %s", text)
	}
}

func TestPatcherProgressPresentation(t *testing.T) {
	event := progress.Event{
		FileIndex:      2,
		FileCount:      4,
		Path:           "data/assets.bin",
		Stage:          progress.StageApplying,
		ProcessedBytes: 50,
		TotalBytes:     100,
	}
	if text := patcherProgressText(event, patch.Forward); !strings.Contains(text, "forward") || !strings.Contains(text, event.Path) {
		t.Fatalf("unexpected progress text: %s", text)
	}
	if value := patcherOverallProgress(event); value != 0.4 {
		t.Fatalf("progress = %v, want 0.4", value)
	}
	event.Stage = progress.StageVerifying
	if value := patcherOverallProgress(event); value != 0.275 {
		t.Fatalf("verification progress = %v, want 0.275", value)
	}
	if value := patcherOverallProgress(progress.Event{Stage: progress.StageCompleted}); value != 1 {
		t.Fatalf("completed progress = %v, want 1", value)
	}
}
