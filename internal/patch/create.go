package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

// CreateOptions configures a VIPR patch creation operation.
type CreateOptions struct {
	Files            []FilePair
	OutputPath       string
	CompressionLevel int
	Comment          string
	CreateReverse    bool
	WorkDirectory    string
	WorkerBudget     int
}

// Create generates a VIPR patch atomically from immutable input snapshots.
func Create(ctx context.Context, options CreateOptions, callback progress.Callback) error {
	plan, err := createPlanFromOptions(options)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	workParent := ""
	if strings.TrimSpace(options.WorkDirectory) != "" {
		workParent, err = resolveWorkDirectory(options.WorkDirectory)
		if err != nil {
			return err
		}
	}
	workDirectory, err := os.MkdirTemp(workParent, "viper-patcher-create-*")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	callback = newCreationProgress(callback, plan.pairs, options.CreateReverse)
	workerBudget := effectiveWorkerBudget(options.WorkerBudget)

	snapshots, operationError := snapshotCreationInputs(ctx, plan, workDirectory, workerBudget, callback)
	if operationError == nil {
		patchHeader, blobs, compressError := compressCreationInputs(ctx, options, snapshots, workDirectory, workerBudget, callback)
		if compressError == nil {
			operationError = assemblePatch(plan.outputPath, patchHeader, blobs)
		} else {
			operationError = compressError
		}
	}

	committed := operationError == nil || IsCommittedWarning(operationError)
	cleanupError := os.RemoveAll(workDirectory)
	if !committed {
		return errors.Join(operationError, wrapJoinedError("remove creation work directory", cleanupError))
	}
	result := committedWarning("patch creation", operationError, wrapJoinedError("remove creation work directory", cleanupError))
	progress.Report(callback, progress.Event{
		FileIndex: len(options.Files),
		FileCount: len(options.Files),
		Stage:     progress.StageCompleted,
	})
	return result
}
