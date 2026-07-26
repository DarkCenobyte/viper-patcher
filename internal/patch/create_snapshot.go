package patch

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type creationSnapshot struct {
	pair   plannedPair
	source fileSnapshot
	target fileSnapshot
}

func snapshotCreationInputs(ctx context.Context, plan createPlan, workDirectory string, parallelism int, callback progress.Callback) ([]creationSnapshot, error) {
	snapshots := make([]creationSnapshot, len(plan.pairs))
	err := parallelFor(ctx, len(plan.pairs), parallelism, func(ctx context.Context, index int) error {
		pair := plan.pairs[index]
		if err := ctx.Err(); err != nil {
			return err
		}
		progress.Report(callback, progress.Event{
			FileIndex: index + 1,
			FileCount: len(plan.pairs),
			Path:      pair.relativePath,
			Stage:     progress.StageSnapshotting,
		})
		source, err := snapshotRegularFile(pair.sourcePath, filepath.Join(workDirectory, fmt.Sprintf("%06d.source", index)))
		if err != nil {
			return fmt.Errorf("snapshot source file %q: %w", pair.relativePath, err)
		}
		target, err := snapshotRegularFile(pair.targetPath, filepath.Join(workDirectory, fmt.Sprintf("%06d.target", index)))
		if err != nil {
			return fmt.Errorf("snapshot target file %q: %w", pair.relativePath, err)
		}
		snapshots[index] = creationSnapshot{pair: pair, source: source, target: target}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}
