//go:build ignore

package patch

import (
	"context"
	"errors"
	"math"
	"sync"
)

var errMemoryBudgetExceeded = errors.New("memory budget request exceeds capacity")

type budgetWaiter struct {
	required int
	ready    chan struct{}
	granted  bool
}

type memoryBudget struct {
	mu        sync.Mutex
	unit      uint64
	capacity  int
	available int
	waiters   []*budgetWaiter
}

func newMemoryBudget(limit, unit uint64) *memoryBudget {
	if unit == 0 {
		unit = 1
	}
	units := memoryBudgetUnits(limit, unit)
	if units < 1 {
		units = 1
	}
	return &memoryBudget{unit: unit, capacity: units, available: units}
}

func (budget *memoryBudget) limitBytes() uint64 {
	if budget == nil || budget.capacity <= 0 {
		return 0
	}
	capacity := uint64(budget.capacity)
	if capacity > math.MaxUint64/budget.unit {
		return math.MaxUint64
	}
	return capacity * budget.unit
}

func (budget *memoryBudget) reserve(ctx context.Context, required int) (func(), error) {
	if budget == nil || required <= 0 {
		return func() {}, nil
	}
	if required > budget.capacity {
		return nil, errMemoryBudgetExceeded
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	budget.mu.Lock()
	if required <= budget.available && len(budget.waiters) == 0 {
		budget.available -= required
		budget.mu.Unlock()
		return budget.release(required), nil
	}
	waiter := &budgetWaiter{required: required, ready: make(chan struct{})}
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

func (budget *memoryBudget) release(required int) func() {
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
func (budget *memoryBudget) grantWaitersLocked() {
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

func (budget *memoryBudget) removeWaiterLocked(waiter *budgetWaiter) {
	for index, candidate := range budget.waiters {
		if candidate == waiter {
			budget.waiters = append(budget.waiters[:index], budget.waiters[index+1:]...)
			return
		}
	}
}

func memoryBudgetUnits(amount, unit uint64) int {
	if amount == 0 {
		return 0
	}
	units := 1 + (amount-1)/unit
	if units > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(units)
}
