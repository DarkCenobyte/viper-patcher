//go:build ignore

package patch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestZstdWindowBudgetQueuedCancellation(t *testing.T) {
	budget := newZstdWindowBudget(1 << 20)
	releaseFirst, err := budget.acquire(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, acquireError := budget.acquire(ctx, 1<<20)
		if release != nil {
			release()
		}
		done <- acquireError
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		budget.mu.Lock()
		queued := len(budget.waiters) == 1
		budget.mu.Unlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservation did not enter the wait queue")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued acquire error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled reservation did not return")
	}
	releaseFirst()

	budget.mu.Lock()
	waiters := len(budget.waiters)
	available := budget.available
	capacity := budget.capacity
	budget.mu.Unlock()
	if waiters != 0 || available != capacity {
		t.Fatalf("budget after cancellation: waiters=%d available=%d capacity=%d", waiters, available, capacity)
	}
}

func TestZstdWindowBudgetValidationPaths(t *testing.T) {
	var unavailable *zstdWindowBudget
	release, err := unavailable.acquire(context.TODO(), 0)
	if err != nil {
		t.Fatal(err)
	}
	release()

	budget := newZstdWindowBudget(0)
	if budget.capacity != 1 || budget.available != 1 {
		t.Fatalf("minimum budget = %d/%d", budget.available, budget.capacity)
	}
	release, err = budget.acquire(context.TODO(), 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, err := budget.acquire(context.Background(), 2<<20); err == nil {
		t.Fatal("reservation larger than the budget was accepted")
	}
	if units := zstdWindowBudgetUnits((1 << 20) + 1); units != 2 {
		t.Fatalf("rounded budget units = %d", units)
	}
	if limit := processZstdWindowBudgetLimit(); limit < 1<<20 {
		t.Fatalf("process window budget is unexpectedly small: %d", limit)
	}
}

func TestPreparedPatchAccessorValidationPaths(t *testing.T) {
	var unavailable *PreparedPatch
	if unavailable.Path() != "" {
		t.Fatal("nil prepared patch returned a path")
	}
	if _, err := unavailable.Digest(); err == nil {
		t.Fatal("nil prepared patch returned a digest")
	}
	if _, err := unavailable.Parsed(); err == nil {
		t.Fatal("nil prepared patch returned metadata")
	}
	if _, _, err := unavailable.acquire(); err == nil {
		t.Fatal("nil prepared patch was acquired")
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "prepared.vipr")
	writePreparedPatchFixture(t, path)
	prepared, err := Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := prepared.Path(); got != path {
		t.Fatalf("prepared path = %q, want %q", got, path)
	}
	digest, err := prepared.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatalf("prepared digest length = %d", len(digest))
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Digest(); err == nil {
		t.Fatal("closed prepared patch returned a digest")
	}
	if _, err := prepared.Parsed(); err == nil {
		t.Fatal("closed prepared patch returned metadata")
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
}
