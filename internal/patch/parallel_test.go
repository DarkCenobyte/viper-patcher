package patch

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestParallelForBoundaryAndFailureCases(t *testing.T) {
	if err := parallelFor(context.Background(), 0, 0, func(context.Context, int) error {
		t.Fatal("operation must not run for an empty workload")
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var mutex sync.Mutex
	seen := make(map[int]bool)
	if err := parallelFor(context.Background(), 3, 99, func(_ context.Context, index int) error {
		mutex.Lock()
		seen[index] = true
		mutex.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("processed indexes = %#v", seen)
	}

	expected := errors.New("worker failed")
	if err := parallelFor(context.Background(), 4, 0, func(_ context.Context, index int) error {
		if index == 1 {
			return expected
		}
		return nil
	}); !errors.Is(err, expected) {
		t.Fatalf("error = %v, want %v", err, expected)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := parallelFor(ctx, 1, 1, func(context.Context, int) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}
