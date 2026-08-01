package patch

import (
	"context"
	"fmt"
	"sync"
)

const (
	applySessionReservation  uint64 = 32 << 20
	createSessionReservation uint64 = 64 << 20
)

type operationKind uint8

const (
	operationApply operationKind = iota
	operationCreate
)

type memoryBudget struct {
	mutex    sync.Mutex
	capacity uint64
	used     uint64
	changed  chan struct{}
}

type memoryLease struct {
	budget *memoryBudget
	bytes  uint64
	once   sync.Once
}

func newMemoryBudget(limit uint64, kind operationKind) *memoryBudget {
	if limit == 0 {
		limit = automaticMemoryLimit(kind)
	}
	return &memoryBudget{capacity: limit, changed: make(chan struct{})}
}

func automaticMemoryLimit(kind operationKind) uint64 {
	if strconvIntSizeRuntime() == 32 {
		if kind == operationCreate {
			return 384 << 20
		}
		return 256 << 20
	}
	if kind == operationCreate {
		return 1536 << 20
	}
	return 1 << 30
}

func strconvIntSizeRuntime() int {
	return 32 << (^uint(0) >> 63)
}

func (budget *memoryBudget) Capacity() uint64 {
	if budget == nil {
		return 0
	}
	return budget.capacity
}

func (budget *memoryBudget) Acquire(ctx context.Context, bytes uint64) (*memoryLease, error) {
	if budget == nil {
		return &memoryLease{}, nil
	}
	if bytes == 0 {
		return &memoryLease{budget: budget}, nil
	}
	if bytes > budget.capacity {
		return nil, fmt.Errorf("memory reservation %d exceeds operation budget %d", bytes, budget.capacity)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		budget.mutex.Lock()
		if bytes <= budget.capacity-budget.used {
			budget.used += bytes
			budget.mutex.Unlock()
			return &memoryLease{budget: budget, bytes: bytes}, nil
		}
		changed := budget.changed
		budget.mutex.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (budget *memoryBudget) TryAcquire(bytes uint64) (*memoryLease, bool) {
	if budget == nil {
		return &memoryLease{}, true
	}
	if bytes > budget.capacity {
		return nil, false
	}
	budget.mutex.Lock()
	defer budget.mutex.Unlock()
	if bytes > budget.capacity-budget.used {
		return nil, false
	}
	budget.used += bytes
	return &memoryLease{budget: budget, bytes: bytes}, true
}

func (lease *memoryLease) Release() {
	if lease == nil || lease.budget == nil {
		return
	}
	lease.once.Do(func() {
		budget := lease.budget
		budget.mutex.Lock()
		if lease.bytes > budget.used {
			budget.used = 0
		} else {
			budget.used -= lease.bytes
		}
		close(budget.changed)
		budget.changed = make(chan struct{})
		budget.mutex.Unlock()
	})
}

func (budget *memoryBudget) LimitWorkers(requested int, perWorker uint64) int {
	requested = max(1, requested)
	if budget == nil || perWorker == 0 {
		return requested
	}
	maximum := int(budget.capacity / perWorker)
	if maximum < 1 {
		maximum = 1
	}
	return min(requested, maximum)
}

func fitApplicationWorkers(budget *memoryBudget, fileWorkers, perFileWorkers int) (int, int) {
	fileWorkers = max(1, fileWorkers)
	perFileWorkers = max(1, perFileWorkers)
	if budget == nil {
		return fileWorkers, perFileWorkers
	}
	maximumSessions := int(budget.Capacity() / applySessionReservation)
	if maximumSessions < 1 {
		maximumSessions = 1
	}
	// Preserve inter-file concurrency first: independent files provide useful
	// parallelism without concentrating every available session on one file.
	// Only reduce file workers when the budget cannot provide even one session
	// per active file, then spend any remaining capacity on intra-file workers.
	fileWorkers = min(fileWorkers, maximumSessions)
	perFileWorkers = min(perFileWorkers, max(1, maximumSessions/fileWorkers))
	return fileWorkers, perFileWorkers
}
