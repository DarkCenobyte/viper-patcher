//go:build ignore

package patch

import (
	"context"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func verifySourceForDecode(ctx context.Context, source *os.File, expected fileState, workers int, callback progress.Callback, event progress.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workers < 1 {
		workers = 1
	}

	event.Stage = progress.StageVerifying
	event.ProcessedBytes = 0
	event.TotalBytes = expected.size
	progress.Report(callback, event)

	lastReported := uint64(0)
	digest, size, err := hashutil.FileParallel(ctx, source, expected.size, workers, func(processed uint64) {
		if processed != expected.size && processed-lastReported < 8<<20 {
			return
		}
		lastReported = processed
		event.ProcessedBytes = processed
		progress.Report(callback, event)
	})
	if err != nil {
		return err
	}
	if lastReported != size {
		event.ProcessedBytes = size
		progress.Report(callback, event)
	}
	if digest != expected.hash || size != expected.size {
		return fmt.Errorf("source BLAKE3 tree or size does not match patch metadata")
	}
	return nil
}
