package patch

import (
	"context"
	"fmt"
	"sync"
)

const (
	copyAddConcurrentIndexMemoryBudget uint64 = 128 << 20
	copyAddIndexBudgetUnit             uint64 = 1 << 20
)

type copyAddIndexBudgetWaiter struct {
	required int
	ready    chan struct{}
	granted  bool
}

type copyAddIndexBudget struct {
	mu        sync.Mutex
	capacity  int
	available int
	waiters   []*copyAddIndexBudgetWaiter
}

func newCopyAddIndexBudget(limit uint64) *copyAddIndexBudget {
	units := copyAddBudgetUnits(limit)
	if units < 1 {
		units = 1
	}
	return &copyAddIndexBudget{capacity: units, available: units}
}

func (budget *copyAddIndexBudget) acquire(ctx context.Context, amount uint64) (func(), error) {
	if budget == nil || amount == 0 {
		return func() {}, nil
	}
	required := copyAddBudgetUnits(amount)
	if required > budget.capacity {
		return nil, fmt.Errorf("copy-add index requires %d bytes, exceeding the %d-byte concurrent budget",
			amount, uint64(budget.capacity)*copyAddIndexBudgetUnit)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	budget.mu.Lock()
	if required <= budget.available && len(budget.waiters) == 0 {
		budget.available -= required
		budget.mu.Unlock()
		return budget.release(required), nil
	}
	waiter := &copyAddIndexBudgetWaiter{required: required, ready: make(chan struct{})}
	budget.waiters = append(budget.waiters, waiter)
	budget.grantWaitersLocked()
	budget.mu.Unlock()

	select {
	case <-waiter.ready:
		return budget.release(required), nil
	case <-ctx.Done():
		budget.mu.Lock()
		if waiter.granted {
			budget.available += required
		} else {
			budget.removeWaiterLocked(waiter)
		}
		budget.grantWaitersLocked()
		budget.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (budget *copyAddIndexBudget) release(required int) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			budget.mu.Lock()
			budget.available += required
			budget.grantWaitersLocked()
			budget.mu.Unlock()
		})
	}
}

// grantWaitersLocked grants every request that currently fits without reserving
// a partial request. Smaller requests may pass a blocked larger request so the
// shared memory budget does not sit idle unnecessarily.
func (budget *copyAddIndexBudget) grantWaitersLocked() {
	for index := 0; index < len(budget.waiters); {
		waiter := budget.waiters[index]
		if waiter.required > budget.available {
			index++
			continue
		}
		budget.available -= waiter.required
		waiter.granted = true
		close(waiter.ready)
		budget.waiters = append(budget.waiters[:index], budget.waiters[index+1:]...)
	}
}

func (budget *copyAddIndexBudget) removeWaiterLocked(waiter *copyAddIndexBudgetWaiter) {
	for index, candidate := range budget.waiters {
		if candidate != waiter {
			continue
		}
		budget.waiters = append(budget.waiters[:index], budget.waiters[index+1:]...)
		return
	}
}

func copyAddBudgetUnits(amount uint64) int {
	units := amount / copyAddIndexBudgetUnit
	if amount%copyAddIndexBudgetUnit != 0 {
		units++
	}
	return int(units)
}

func copyAddIndexAllocationBytes(capacity int) uint64 {
	if capacity <= 0 {
		return 0
	}
	return uint64(capacity) * copyAddIndexEntrySize
}
