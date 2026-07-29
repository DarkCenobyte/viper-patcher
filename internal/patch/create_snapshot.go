//go:build ignore

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

func snapshotCreationInputs(ctx context.Context, plan createPlan, workDirectory string, workerBudget int, callback progress.Callback) ([]creationSnapshot, error) {
	snapshots := make([]creationSnapshot, len(plan.pairs))
	err := parallelFor(ctx, len(plan.pairs), workerBudget, func(ctx context.Context, index int) error {
		pair := plan.pairs[index]
		if err := ctx.Err(); err != nil {
			return err
		}
		total, ok := checkedAdd(pair.sourceSize, pair.targetSize)
		if !ok {
			return fmt.Errorf("snapshot progress size overflows for %q", pair.relativePath)
		}
		report := func(processed uint64) {
			progress.Report(callback, progress.Event{
				FileIndex:      index + 1,
				FileCount:      len(plan.pairs),
				Path:           pair.relativePath,
				Stage:          progress.StageSnapshotting,
				ProcessedBytes: processed,
				TotalBytes:     total,
			})
		}
		report(0)
		source, err := snapshotRegularFile(ctx, pair.sourcePath, filepath.Join(workDirectory, fmt.Sprintf("%06d.source", index)), report)
		if err != nil {
			return fmt.Errorf("snapshot source file %q: %w", pair.relativePath, err)
		}
		target, err := snapshotRegularFile(ctx, pair.targetPath, filepath.Join(workDirectory, fmt.Sprintf("%06d.target", index)), func(processed uint64) {
			report(pair.sourceSize + processed)
		})
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
