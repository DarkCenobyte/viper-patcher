package patch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCopyAddIndexBudgetCancellation(t *testing.T) {
	budget := newCopyAddIndexBudget(copyAddIndexBudgetUnit)
	release, err := budget.acquire(context.Background(), copyAddIndexBudgetUnit)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := budget.acquire(ctx, copyAddIndexBudgetUnit); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire error = %v, want context cancellation", err)
	}
	release()

	release, err = budget.acquire(context.Background(), copyAddIndexBudgetUnit)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCopyAddIndexBudgetDoesNotPartiallyReserve(t *testing.T) {
	budget := newCopyAddIndexBudget(3 * copyAddIndexBudgetUnit)
	releaseTwo, err := budget.acquire(context.Background(), 2*copyAddIndexBudgetUnit)
	if err != nil {
		t.Fatal(err)
	}

	acquiredTwo := make(chan func(), 1)
	go func() {
		release, acquireErr := budget.acquire(context.Background(), 2*copyAddIndexBudgetUnit)
		if acquireErr != nil {
			return
		}
		acquiredTwo <- release
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		budget.mu.Lock()
		queued := len(budget.waiters) == 1
		available := budget.available
		budget.mu.Unlock()
		if queued {
			if available != 1 {
				t.Fatalf("available units = %d, want 1 while a two-unit request waits", available)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("two-unit acquisition was not queued")
		}
		time.Sleep(time.Millisecond)
	}

	releaseOne, err := budget.acquire(context.Background(), copyAddIndexBudgetUnit)
	if err != nil {
		t.Fatal(err)
	}
	releaseOne()
	releaseTwo()

	select {
	case release := <-acquiredTwo:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("queued two-unit acquisition did not complete")
	}
}

func TestCopyAddIndexBudgetMultiUnitProgress(t *testing.T) {
	budget := newCopyAddIndexBudget(3 * copyAddIndexBudgetUnit)
	acquired := make(chan func(), 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			release, err := budget.acquire(context.Background(), 2*copyAddIndexBudgetUnit)
			if err == nil {
				acquired <- release
			}
		}()
	}
	close(start)
	var first func()
	select {
	case first = <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("first multi-unit acquisition did not complete")
	}
	first()
	select {
	case second := <-acquired:
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("second multi-unit acquisition did not complete")
	}
}

func TestCopyAddIndexAllocationBytes(t *testing.T) {
	if bytes := copyAddIndexAllocationBytes(0); bytes != 0 {
		t.Fatalf("zero-capacity allocation = %d", bytes)
	}
	if bytes := copyAddIndexAllocationBytes(2); bytes != 2*copyAddIndexEntrySize {
		t.Fatalf("two-entry allocation = %d", bytes)
	}
}
