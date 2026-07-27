package patchergui

import (
	"fmt"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func patcherValidationText(result patch.ValidationResult) string {
	switch result.State {
	case patch.StateBidirectionalReady:
		return "Ready: both forward and reverse directions are valid."
	case patch.StateForwardReady:
		return "Ready to apply the forward patch."
	case patch.StateReverseReady:
		return "Ready to apply the reverse patch."
	case patch.StateMissingFiles:
		if len(result.Missing) == 0 {
			return "Blocked: one or more required files are missing."
		}
		return fmt.Sprintf("Blocked: %d required file(s) are missing. First: %s", len(result.Missing), result.Missing[0])
	case patch.StateMixedFiles:
		return "Blocked: the folder contains a mixture of source and target file states."
	case patch.StateInvalidFiles:
		if len(result.Issues) == 0 {
			return "Blocked: one or more files do not match the patch."
		}
		return fmt.Sprintf("Blocked: %d file(s) do not match. First: %s (%s)", len(result.Issues), result.Issues[0].Path, result.Issues[0].Reason)
	default:
		return "Blocked: unknown preflight state."
	}
}

func patcherProgressText(event progress.Event, direction patch.Direction) string {
	switch event.Stage {
	case progress.StagePreparing:
		return fmt.Sprintf("Committing prepared replacements for the %s patch...", direction)
	case progress.StageApplying:
		return fmt.Sprintf("[%d/%d] Applying %s patch: %s", event.FileIndex, event.FileCount, direction, event.Path)
	case progress.StageVerifying:
		return fmt.Sprintf("[%d/%d] Verifying installed file: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageFilePrepared:
		return fmt.Sprintf("[%d/%d] Prepared replacement: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageFileCompleted:
		return fmt.Sprintf("[%d/%d] Committed replacement: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageCompleted:
		return fmt.Sprintf("Finalizing %s patch...", direction)
	default:
		return string(event.Stage)
	}
}
