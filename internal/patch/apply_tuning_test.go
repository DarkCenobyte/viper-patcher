package patch

import (
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
