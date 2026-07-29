package patch

import (
	"context"
	"runtime"
	"sync"
)

func effectiveWorkers(requested int) int {
	if requested > 0 {
		return requested
	}
	return max(1, runtime.GOMAXPROCS(0))
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
