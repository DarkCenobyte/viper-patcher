package patch

import (
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
)

const (
	intraFileBytesPerWorker uint64 = 32 << 20
	maxIntraFileWorkers            = 8
)

// workerAllocation keeps multiple files moving concurrently while reserving an
// intra-file budget when the global pool is larger than the number of files.
func workerAllocation(totalWorkers, fileCount int) (fileWorkers, perFileWorkers int) {
	if totalWorkers < 1 {
		totalWorkers = 1
	}
	if fileCount < 1 {
		return 1, totalWorkers
	}
	if totalWorkers <= fileCount {
		return totalWorkers, 1
	}
	fileWorkers = (totalWorkers + 1) / 2
	if fileWorkers > fileCount {
		fileWorkers = fileCount
	}
	if fileWorkers < 1 {
		fileWorkers = 1
	}
	perFileWorkers = totalWorkers / fileWorkers
	if perFileWorkers < 1 {
		perFileWorkers = 1
	}
	return fileWorkers, perFileWorkers
}

func adaptiveChunkWorkers(workerBudget int, size uint64) int {
	if workerBudget < 1 {
		workerBudget = 1
	}
	bySize := int((size + intraFileBytesPerWorker - 1) / intraFileBytesPerWorker)
	if bySize < 1 {
		bySize = 1
	}
	if workerBudget > bySize {
		workerBudget = bySize
	}
	if workerBudget > maxIntraFileWorkers {
		workerBudget = maxIntraFileWorkers
	}
	return workerBudget
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

type chunkBuffer [hashutil.ChunkSize]byte

type chunkBufferPool struct {
	pool sync.Pool
}

func newChunkBufferPool() *chunkBufferPool {
	result := &chunkBufferPool{}
	result.pool.New = func() any {
		return new(chunkBuffer)
	}
	return result
}

func (pool *chunkBufferPool) get(length uint64) ([]byte, *chunkBuffer) {
	pooled := pool.pool.Get().(*chunkBuffer)
	return pooled[:int(length)], pooled
}

func (pool *chunkBufferPool) put(pooled *chunkBuffer) {
	pool.pool.Put(pooled)
}
