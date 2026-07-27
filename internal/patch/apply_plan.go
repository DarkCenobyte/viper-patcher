package patch

import (
	"runtime"
	"sort"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type applicationPlan struct {
	fileWorkers    int
	perFileWorkers int
	decoderCount   int
}

func newApplicationPlan(requestedWorkers int, entries []patchformat.FileEntry, direction Direction) applicationPlan {
	workerBudget := requestedWorkers
	if workerBudget <= 0 {
		workerBudget = runtime.NumCPU()
	}
	if workerBudget > runtime.NumCPU() {
		workerBudget = runtime.NumCPU()
	}
	if workerBudget < 1 {
		workerBudget = 1
	}

	fileWorkers, perFileWorkers := workerAllocation(workerBudget, len(entries))
	return applicationPlan{
		fileWorkers:    fileWorkers,
		perFileWorkers: perFileWorkers,
		decoderCount:   applicationDecoderCount(entries, direction, fileWorkers, perFileWorkers),
	}
}

func applicationDecoderCount(entries []patchformat.FileEntry, direction Direction, fileWorkers, perFileWorkers int) int {
	if len(entries) == 0 {
		return 1
	}

	demands := make([]int, len(entries))
	for index, entry := range entries {
		_, _, _, method, input, output := differential(entry, direction)
		demand := 1
		if method == patchformat.MethodChunkedReplace {
			demand = adaptiveChunkWorkers(perFileWorkers, maxUint64(input.size, output.size))
		}
		demands[index] = demand
	}
	sort.Sort(sort.Reverse(sort.IntSlice(demands)))

	if fileWorkers > len(demands) {
		fileWorkers = len(demands)
	}
	decoderCount := 0
	for _, demand := range demands[:fileWorkers] {
		decoderCount += demand
	}
	if decoderCount < 1 {
		return 1
	}
	return decoderCount
}

type fileState struct {
	hash string
	size uint64
}

func differential(entry patchformat.FileEntry, direction Direction) (offset, length, expandedLength uint64, method string, input, output fileState) {
	if direction == Reverse {
		return entry.ReverseOffset, entry.ReverseLength, entry.ReverseExpandedLength, entry.ReverseMethod,
			fileState{hash: entry.TargetHash, size: entry.TargetSize},
			fileState{hash: entry.SourceHash, size: entry.SourceSize}
	}
	return entry.ForwardOffset, entry.ForwardLength, entry.ForwardExpandedLength, entry.ForwardMethod,
		fileState{hash: entry.SourceHash, size: entry.SourceSize},
		fileState{hash: entry.TargetHash, size: entry.TargetSize}
}
