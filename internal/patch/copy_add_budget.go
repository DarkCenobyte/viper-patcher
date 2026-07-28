package patch

import (
	"context"
	"errors"
	"fmt"
)

const (
	copyAddConcurrentIndexMemoryBudget uint64 = 128 << 20
	copyAddIndexBudgetUnit             uint64 = 1 << 20
)

type copyAddIndexBudget struct {
	*memoryBudget
}

func newCopyAddIndexBudget(limit uint64) *copyAddIndexBudget {
	return &copyAddIndexBudget{memoryBudget: newMemoryBudget(limit, copyAddIndexBudgetUnit)}
}

func (budget *copyAddIndexBudget) acquire(ctx context.Context, amount uint64) (func(), error) {
	if budget == nil || amount == 0 {
		return func() {}, nil
	}
	required := copyAddBudgetUnits(amount)
	if required > budget.capacity {
		return nil, fmt.Errorf("copy-add index requires %d bytes, exceeding the %d-byte concurrent budget",
			amount, budget.limitBytes())
	}
	release, err := budget.reserve(ctx, required)
	if errors.Is(err, errMemoryBudgetExceeded) {
		return nil, fmt.Errorf("copy-add index requires %d bytes, exceeding the %d-byte concurrent budget",
			amount, budget.limitBytes())
	}
	return release, err
}

func copyAddBudgetUnits(amount uint64) int {
	return memoryBudgetUnits(amount, copyAddIndexBudgetUnit)
}

func copyAddIndexAllocationBytes(capacity int) uint64 {
	if capacity <= 0 {
		return 0
	}
	return uint64(capacity) * copyAddIndexEntrySize
}
