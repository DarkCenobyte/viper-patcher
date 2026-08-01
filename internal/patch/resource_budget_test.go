package patch

import (
	"context"
	"testing"
	"time"
)

func TestMemoryBudgetAcquiresAtomically(t *testing.T) {
	budget := newMemoryBudget(128, operationApply)
	first, err := budget.Acquire(context.Background(), 96)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := budget.Acquire(ctx, 64); err == nil {
		t.Fatal("oversubscribed reservation unexpectedly succeeded")
	}
	first.Release()
	second, err := budget.Acquire(context.Background(), 64)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
}

func TestMemoryBudgetRejectsImpossibleReservation(t *testing.T) {
	budget := newMemoryBudget(64, operationApply)
	if _, err := budget.Acquire(context.Background(), 65); err == nil {
		t.Fatal("impossible reservation accepted")
	}
}

func TestFitApplicationWorkersStaysWithinBudget(t *testing.T) {
	tests := []struct {
		name                        string
		maximumSessions             int
		fileWorkers, perFileWorkers int
		wantFiles, wantPerFile      int
	}{
		{"reduce intra-file first", 4, 4, 4, 4, 1},
		{"clamp files to capacity", 4, 8, 2, 4, 1},
		{"use remaining per-file capacity", 6, 3, 4, 3, 2},
		{"keep an already fitting plan", 8, 2, 3, 2, 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			budget := newMemoryBudget(uint64(test.maximumSessions)*applySessionReservation, operationApply)
			files, perFile := fitApplicationWorkers(budget, test.fileWorkers, test.perFileWorkers)
			if files*perFile > test.maximumSessions {
				t.Fatalf("plan uses %d sessions, budget allows %d", files*perFile, test.maximumSessions)
			}
			if files != test.wantFiles || perFile != test.wantPerFile {
				t.Fatalf("plan = (%d, %d), want (%d, %d)", files, perFile, test.wantFiles, test.wantPerFile)
			}
		})
	}
}
