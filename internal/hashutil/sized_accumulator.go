//go:build ignore

package hashutil

import (
	"fmt"
	"hash"
	"io"
)

// SizedAccumulator computes a tree identity for an already-known output size.
// Knowing the final chunk count lets it use ChunkDigestStore and therefore spill
// only extreme digest tables without retaining output bytes or imposing a file
// size ratio/limit.
type SizedAccumulator struct {
	expectedSize uint64
	size         uint64
	chunkBytes   uint64
	chunkIndex   uint64
	chunk        hash.Hash
	digests      *ChunkDigestStore
	finalized    bool
	sum          string
	sumError     error
}

func NewSizedAccumulator(expectedSize uint64) (*SizedAccumulator, error) {
	return newSizedAccumulator(expectedSize, chunkDigestMemoryBudget())
}

func newSizedAccumulator(expectedSize, memoryBudget uint64) (*SizedAccumulator, error) {
	digests, err := newChunkDigestStore(ChunkCount(expectedSize), memoryBudget)
	if err != nil {
		return nil, err
	}
	return &SizedAccumulator{
		expectedSize: expectedSize,
		chunk:        NewChunkHasher(),
		digests:      digests,
	}, nil
}

func (accumulator *SizedAccumulator) Write(data []byte) (int, error) {
	if accumulator == nil || accumulator.digests == nil {
		return 0, fmt.Errorf("sized hash accumulator is unavailable")
	}
	if accumulator.finalized {
		return 0, fmt.Errorf("sized hash accumulator is finalized")
	}
	if accumulator.size > accumulator.expectedSize || uint64(len(data)) > accumulator.expectedSize-accumulator.size {
		return 0, fmt.Errorf("hashed output exceeds declared size")
	}
	originalLength := len(data)
	for len(data) > 0 {
		remaining := ChunkSize - accumulator.chunkBytes
		part := uint64(len(data))
		if part > remaining {
			part = remaining
		}
		written, err := accumulator.chunk.Write(data[:int(part)])
		if err != nil {
			return originalLength - len(data) + written, err
		}
		if written != int(part) {
			return originalLength - len(data) + written, io.ErrShortWrite
		}
		accumulator.chunkBytes += part
		accumulator.size += part
		data = data[int(part):]
		if accumulator.chunkBytes == ChunkSize {
			if err := accumulator.flushChunk(); err != nil {
				return originalLength - len(data), err
			}
		}
	}
	return originalLength, nil
}

func (accumulator *SizedAccumulator) flushChunk() error {
	if accumulator.chunkBytes == 0 {
		return nil
	}
	sum := accumulator.chunk.Sum(nil)
	if len(sum) != 32 {
		return fmt.Errorf("unexpected BLAKE3 chunk digest size %d", len(sum))
	}
	var digest [32]byte
	copy(digest[:], sum)
	if err := accumulator.digests.Set(accumulator.chunkIndex, digest); err != nil {
		return err
	}
	accumulator.chunkIndex++
	accumulator.chunk.Reset()
	accumulator.chunkBytes = 0
	return nil
}

func (accumulator *SizedAccumulator) SumHex() (string, error) {
	if accumulator == nil || accumulator.digests == nil {
		return "", fmt.Errorf("sized hash accumulator is unavailable")
	}
	if !accumulator.finalized {
		accumulator.finalized = true
		if accumulator.size != accumulator.expectedSize {
			accumulator.sumError = fmt.Errorf("hashed output size is %d, expected %d", accumulator.size, accumulator.expectedSize)
			return "", accumulator.sumError
		}
		if err := accumulator.flushChunk(); err != nil {
			accumulator.sumError = err
			return "", err
		}
		if accumulator.chunkIndex != ChunkCount(accumulator.expectedSize) {
			accumulator.sumError = fmt.Errorf("hashed output produced an unexpected chunk count")
			return "", accumulator.sumError
		}
		accumulator.sum, accumulator.sumError = accumulator.digests.Root(accumulator.expectedSize)
	}
	return accumulator.sum, accumulator.sumError
}

func (accumulator *SizedAccumulator) Close() error {
	if accumulator == nil || accumulator.digests == nil {
		return nil
	}
	err := accumulator.digests.Close()
	accumulator.digests = nil
	return err
}
