//go:build ignore

package patch

import (
	"runtime"
	"testing"
)

func TestEffectiveWorkerBudgetAutomatic(t *testing.T) {
	workers := effectiveWorkerBudget(0)
	if workers < 1 || workers > runtime.NumCPU() {
		t.Fatalf("automatic worker budget = %d, logical CPUs = %d", workers, runtime.NumCPU())
	}
	if explicit := effectiveWorkerBudget(1); explicit != 1 {
		t.Fatalf("explicit worker budget = %d", explicit)
	}
}
