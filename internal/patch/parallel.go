//go:build ignore

package patch

import (
	"context"
	"sync"
)

func parallelFor(ctx context.Context, count, workers int, operation func(context.Context, int) error) error {
	if count == 0 {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	if workers > count {
		workers = count
	}

	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var group sync.WaitGroup
	var firstError error
	var errorOnce sync.Once

	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				if workerContext.Err() != nil {
					return
				}
				if err := operation(workerContext, index); err != nil {
					errorOnce.Do(func() {
						firstError = err
						cancel()
					})
					return
				}
			}
		}()
	}

sendLoop:
	for index := range count {
		select {
		case jobs <- index:
		case <-workerContext.Done():
			break sendLoop
		}
	}
	close(jobs)
	group.Wait()
	if firstError != nil {
		return firstError
	}
	return ctx.Err()
}
