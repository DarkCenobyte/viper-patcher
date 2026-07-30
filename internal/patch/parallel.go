package patch

import (
	"context"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/workerbudget"
)

func effectiveWorkers(requested int) int {
	return workerbudget.Effective(requested)
}

func parallelFor(ctx context.Context, count, workers int, fn func(context.Context, int) error) error {
	if count == 0 {
		return nil
	}
	workers = min(max(1, workers), count)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if err := fn(workCtx, index); err != nil {
					once.Do(func() { first = err; cancel() })
					return
				}
			}
		}()
	}
send:
	for i := 0; i < count; i++ {
		select {
		case jobs <- i:
		case <-workCtx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
