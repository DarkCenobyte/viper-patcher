package patch

import (
	"context"
	"fmt"
)

type schedulerContextKey struct{}

type taskCost struct {
	CPUUnits   uint64
	ReadUnits  uint64
	WriteUnits uint64
}

type operationScheduler struct {
	cpu   *memoryBudget
	read  *memoryBudget
	write *memoryBudget
}

func withOperationScheduler(ctx context.Context, profile IOProfile, workers int) context.Context {
	workers = max(1, workers)
	readUnits, writeUnits := 4, 2
	switch profile {
	case IOHDD:
		readUnits, writeUnits = 1, 1
	case IOSSD:
		readUnits, writeUnits = min(workers, 4), min(workers, 2)
	case IONVMe:
		readUnits, writeUnits = min(workers, 8), min(workers, 4)
	}
	scheduler := &operationScheduler{
		cpu:   newMemoryBudget(uint64(workers), operationApply),
		read:  newMemoryBudget(uint64(max(1, readUnits)), operationApply),
		write: newMemoryBudget(uint64(max(1, writeUnits)), operationApply),
	}
	return context.WithValue(ctx, schedulerContextKey{}, scheduler)
}

func schedulerFromContext(ctx context.Context) *operationScheduler {
	if ctx == nil {
		return nil
	}
	scheduler, _ := ctx.Value(schedulerContextKey{}).(*operationScheduler)
	return scheduler
}

func runScheduled(ctx context.Context, cost taskCost, operation func() error) error {
	scheduler := schedulerFromContext(ctx)
	if scheduler == nil {
		return operation()
	}
	cpu, err := acquireUnits(ctx, scheduler.cpu, cost.CPUUnits)
	if err != nil {
		return err
	}
	defer cpu.Release()
	read, err := acquireUnits(ctx, scheduler.read, cost.ReadUnits)
	if err != nil {
		return err
	}
	defer read.Release()
	write, err := acquireUnits(ctx, scheduler.write, cost.WriteUnits)
	if err != nil {
		return err
	}
	defer write.Release()
	return operation()
}

func acquireUnits(ctx context.Context, budget *memoryBudget, units uint64) (*memoryLease, error) {
	if units == 0 {
		return &memoryLease{}, nil
	}
	if budget == nil {
		return nil, fmt.Errorf("scheduler budget is unavailable")
	}
	return budget.Acquire(ctx, units)
}
