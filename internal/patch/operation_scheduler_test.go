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
		return runScheduled(ctx, createWindowTaskCost, func() error {
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

func TestScheduledWorkersUseTightestResourceLimit(t *testing.T) {
	tests := []struct {
		name    string
		profile IOProfile
		cost    taskCost
		want    int
	}{
		{"auto apply", IOAuto, applyLeafTaskCost, 4},
		{"auto create", IOAuto, createWindowTaskCost, 8},
		{"auto snapshot", IOAuto, snapshotTaskCost, 4},
		{"hdd apply", IOHDD, applyLeafTaskCost, 1},
		{"ssd apply", IOSSD, applyLeafTaskCost, 2},
		{"nvme apply", IONVMe, applyLeafTaskCost, 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := withOperationScheduler(context.Background(), test.profile, 16)
			if got := scheduledWorkers(ctx, 16, test.cost); got != test.want {
				t.Fatalf("scheduled workers = %d, want %d", got, test.want)
			}
		})
	}
}
