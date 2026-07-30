package patch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestOperationSchedulerBoundsLeafConcurrency(t *testing.T) {
	ctx := withOperationScheduler(context.Background(), IOHDD, 8)
	var running atomic.Int32
	var peak atomic.Int32
	err := parallelFor(ctx, 8, 8, func(ctx context.Context, _ int) error {
		return runScheduled(ctx, taskCost{CPUUnits: 1, ReadUnits: 1}, func() error {
			value := running.Add(1)
			for {
				old := peak.Load()
				if value <= old || peak.CompareAndSwap(old, value) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			running.Add(-1)
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 1 {
		t.Fatalf("HDD peak concurrency = %d", peak.Load())
	}
}
