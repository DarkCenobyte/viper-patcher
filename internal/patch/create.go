package patch

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

// CreateOptions configures a VIPR patch creation operation.
type CreateOptions struct {
	Files            []FilePair
	OutputPath       string
	CompressionLevel int
	Comment          string
	CreateReverse    bool
}

// Create generates a VIPR patch atomically from immutable input snapshots.
func Create(ctx context.Context, options CreateOptions, callback progress.Callback) (resultError error) {
	plan, err := createPlanFromOptions(options)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	workDirectory, err := os.MkdirTemp("", "viper-patcher-create-*")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() {
		if cleanupError := os.RemoveAll(workDirectory); cleanupError != nil {
			resultError = errors.Join(resultError, fmt.Errorf("remove creation work directory: %w", cleanupError))
		}
	}()

	snapshots, err := snapshotCreationInputs(ctx, plan, workDirectory, callback)
	if err != nil {
		return err
	}
	header, blobs, err := compressCreationInputs(ctx, options, snapshots, workDirectory, callback)
	if err != nil {
		return err
	}
	if err := assemblePatch(options.OutputPath, header, blobs); err != nil {
		return err
	}
	progress.Report(callback, progress.Event{
		FileIndex: len(options.Files),
		FileCount: len(options.Files),
		Stage:     progress.StageCompleted,
	})
	return nil
}
