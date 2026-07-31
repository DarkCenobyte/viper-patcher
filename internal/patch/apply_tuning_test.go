package patch

import (
	"context"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestPlannedApplicationWorkersUseActualGroupCount(t *testing.T) {
	windows := []patchformat.WindowDescriptor{{
		OutputOffset: 0,
		OutputSize:   1 << 20,
		Kind:         patchformat.WindowReplaceRaw,
	}}
	if got := plannedApplicationWorkers(16, windows, 1<<20); got != 1 {
		t.Fatalf("planned workers = %d, want 1", got)
	}
	if got := plannedApplicationWorkers(16, nil, 0); got != 0 {
		t.Fatalf("zero-output planned workers = %d, want 0", got)
	}
}

func TestPlannedApplicationConcurrencySeparatesCoordinatorsAndLeaves(t *testing.T) {
	ctx := withOperationScheduler(context.Background(), IOAuto, 16)
	resources := newMemoryBudget(1<<30, operationApply)

	fileWorkers, perFileWorkers := plannedApplicationConcurrency(
		ctx,
		IOAuto,
		16,
		64,
		resources,
	)
	if fileWorkers != 16 || perFileWorkers != 1 {
		t.Fatalf(
			"many-file concurrency = (%d, %d), want (16, 1)",
			fileWorkers,
			perFileWorkers,
		)
	}

	fileWorkers, perFileWorkers = plannedApplicationConcurrency(
		ctx,
		IOAuto,
		16,
		1,
		resources,
	)
	if fileWorkers != 1 || perFileWorkers != 4 {
		t.Fatalf(
			"single-file concurrency = (%d, %d), want (1, 4)",
			fileWorkers,
			perFileWorkers,
		)
	}
}
