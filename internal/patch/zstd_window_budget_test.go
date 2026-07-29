//go:build ignore

package patch

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestZstdWindowBudgetWaitsForWholeReservation(t *testing.T) {
	budget := newZstdWindowBudget(4 << 20)
	releaseFirst, err := budget.acquire(context.Background(), 3<<20)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, acquireError := budget.acquire(context.Background(), 2<<20)
		if acquireError != nil {
			acquired <- nil
			return
		}
		acquired <- release
	}()

	deadline := time.Now().Add(time.Second)
	for {
		budget.mu.Lock()
		waiting := len(budget.waiters) == 1
		available := budget.available
		budget.mu.Unlock()
		if waiting {
			if available != 1 {
				t.Fatalf("waiting reservation partially consumed the budget: %d units remain", available)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second reservation did not enter the wait queue")
		}
		runtime.Gosched()
	}
	select {
	case <-acquired:
		t.Fatal("second reservation bypassed the process-wide budget")
	default:
	}
	releaseFirst()
	select {
	case releaseSecond := <-acquired:
		if releaseSecond == nil {
			t.Fatal("second reservation failed")
		}
		releaseSecond()
	case <-time.After(time.Second):
		t.Fatal("second reservation did not resume")
	}
}

func TestZstdWindowBudgetCancellationReturnsReservation(t *testing.T) {
	budget := newZstdWindowBudget(1 << 20)
	release, err := budget.acquire(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := budget.acquire(ctx, 1<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire error = %v, want context cancellation", err)
	}
	release()
	secondRelease, err := budget.acquire(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	secondRelease()
}
