package patch

import (
	"runtime"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestApplicationPlanAllocatesOnlyRequiredDecoders(t *testing.T) {
	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	if workers < 2 {
		workers = 2
	}

	t.Run("single replacement", func(t *testing.T) {
		plan := newApplicationPlan(workers, []patchformat.FileEntry{{
			ForwardMethod: patchformat.MethodReplace,
			SourceSize:    1 << 20,
			TargetSize:    1 << 20,
		}}, Forward)
		if plan.decoderCount != 1 {
			t.Fatalf("decoder count = %d, want 1", plan.decoderCount)
		}
	})

	t.Run("single large chunked replacement", func(t *testing.T) {
		plan := newApplicationPlan(workers, []patchformat.FileEntry{{
			ForwardMethod: patchformat.MethodChunkedReplace,
			SourceSize:    1 << 30,
			TargetSize:    1 << 30,
		}}, Forward)
		want := adaptiveChunkWorkers(plan.perFileWorkers, 1<<30)
		if plan.decoderCount != want {
			t.Fatalf("decoder count = %d, want %d", plan.decoderCount, want)
		}
	})

	t.Run("multiple small files", func(t *testing.T) {
		entries := make([]patchformat.FileEntry, workers+4)
		for index := range entries {
			entries[index].ForwardMethod = patchformat.MethodReplace
		}
		plan := newApplicationPlan(workers, entries, Forward)
		if plan.decoderCount != plan.fileWorkers {
			t.Fatalf("decoder count = %d, file workers = %d", plan.decoderCount, plan.fileWorkers)
		}
	})
}
