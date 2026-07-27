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
		Overall:        0.4,
	}
	if text := patcherProgressText(event, patch.Forward); !strings.Contains(text, "forward") || !strings.Contains(text, event.Path) {
		t.Fatalf("unexpected progress text: %s", text)
	}
	if event.Overall != 0.4 {
		t.Fatalf("progress = %v, want 0.4", event.Overall)
	}
	event.Stage = progress.StagePreparing
	if text := patcherProgressText(event, patch.Forward); !strings.Contains(text, "Committing prepared replacements") {
		t.Fatalf("unexpected commit text: %s", text)
	}
}
