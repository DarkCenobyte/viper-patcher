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
	budget := newMemoryBudget(4*applySessionReservation, operationApply)
	files, perFile := fitApplicationWorkers(budget, 4, 4)
	if files*perFile > 4 {
		t.Fatalf("plan uses %d sessions", files*perFile)
	}
	if files != 4 || perFile != 1 {
		t.Fatalf("plan = (%d, %d), want (4, 1)", files, perFile)
	}
}
