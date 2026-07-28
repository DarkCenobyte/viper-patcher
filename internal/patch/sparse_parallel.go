package patch

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type sparseChunkRecord struct {
	offset     uint32
	dataOffset uint32
	length     uint32
}

type sparseChunkPlan struct {
	records []sparseChunkRecord
	data    []byte
}

type sparseChunkJob struct {
	index uint64
	plan  sparseChunkPlan
}

// streamSparseChunkPlans validates a sparse stream and emits one bounded plan
// per fixed BLAKE3 chunk. At most the worker queue and active jobs retain
// replacement data, so memory use does not grow with the complete file size.
func streamSparseChunkPlans(ctx context.Context, reader io.Reader, expectedSize uint64, emit func(uint64, sparseChunkPlan) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	buffered := bufio.NewReaderSize(reader, sparseIOBufferSize)
	var magic [8]byte
	if _, err := io.ReadFull(buffered, magic[:]); err != nil {
		return fmt.Errorf("read sparse stream magic: %w", err)
	}
	if magic != sparseMagic {
		return fmt.Errorf("invalid sparse stream magic")
	}

	chunkCount := hashutil.ChunkCount(expectedSize)
	nextChunk := uint64(0)
	current := sparseChunkPlan{}
	emitCurrent := func() error {
		if nextChunk >= chunkCount {
			return fmt.Errorf("sparse operation exceeds expected file size")
		}
		if err := emit(nextChunk, current); err != nil {
			return err
		}
		nextChunk++
		current = sparseChunkPlan{}
		return nil
	}
	emitUntil := func(target uint64) error {
		for nextChunk < target {
			if err := emitCurrent(); err != nil {
				return err
			}
		}
		return nil
	}

	var position uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap, err := binary.ReadUvarint(buffered)
		if err != nil {
			return fmt.Errorf("read sparse gap: %w", err)
		}
		length, err := binary.ReadUvarint(buffered)
		if err != nil {
			return fmt.Errorf("read sparse replacement length: %w", err)
		}
		if length == 0 {
			if gap != 0 {
				return fmt.Errorf("invalid sparse terminator")
			}
			break
		}
		if position > expectedSize || gap > expectedSize-position || length > expectedSize-position-gap {
			return fmt.Errorf("sparse operation exceeds expected file size")
		}
		position += gap
		if err := emitUntil(position / hashutil.ChunkSize); err != nil {
			return err
		}

		for remaining := length; remaining > 0; {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunkIndex := position / hashutil.ChunkSize
			if chunkIndex != nextChunk || chunkIndex >= chunkCount {
				return fmt.Errorf("sparse operation exceeds expected file size")
			}
			withinChunk := position % hashutil.ChunkSize
			part := hashutil.ChunkSize - withinChunk
			if remaining < part {
				part = remaining
			}
			dataOffset := len(current.data)
			current.data = append(current.data, make([]byte, int(part))...)
			if _, err := io.ReadFull(buffered, current.data[dataOffset:]); err != nil {
				return fmt.Errorf("read sparse replacement bytes: %w", err)
			}
			current.records = append(current.records, sparseChunkRecord{
				offset:     uint32(withinChunk),
				dataOffset: uint32(dataOffset),
				length:     uint32(part),
			})
			position += part
			remaining -= part
			if position%hashutil.ChunkSize == 0 {
				if err := emitCurrent(); err != nil {
					return err
				}
			}
		}
	}
	if _, err := buffered.ReadByte(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("sparse stream contains trailing data")
		}
		return fmt.Errorf("inspect sparse stream tail: %w", err)
	}
	for nextChunk < chunkCount {
		if err := emitCurrent(); err != nil {
			return err
		}
	}
	return nil
}

func applySparseStreamParallel(ctx context.Context, source *os.File, operations io.Reader, output *os.File, expectedSize uint64, expectedSourceHash, expectedTargetHash string, workers int, callback progress.Callback, event progress.Event) (resultError error) {
	if source == nil || operations == nil || output == nil {
		return fmt.Errorf("sparse application requires source, operations, and output files")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if expectedSize > math.MaxInt64 {
		return fmt.Errorf("sparse output exceeds the signed 64-bit file range")
	}
	chunkCount := hashutil.ChunkCount(expectedSize)
	if workers < 1 {
		workers = 1
	}
	if chunkCount > 0 && uint64(workers) > chunkCount {
		workers = int(chunkCount)
	}
	if err := output.Truncate(int64(expectedSize)); err != nil {
		return err
	}

	if chunkCount == 0 {
		parseError := streamSparseChunkPlans(ctx, operations, expectedSize, func(uint64, sparseChunkPlan) error {
			return fmt.Errorf("empty sparse output unexpectedly contains a chunk")
		})
		if parseError != nil {
			return parseError
		}
		root := hashutil.RootFromChunkDigestBytes(0, nil)
		if root != expectedSourceHash {
			return fmt.Errorf("installed source failed BLAKE3 tree verification")
		}
		if root != expectedTargetHash {
			return fmt.Errorf("generated output failed BLAKE3 tree verification")
		}
		return nil
	}

	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan sparseChunkJob, workers)
	sourceDigests, err := hashutil.NewChunkDigestStore(chunkCount)
	if err != nil {
		return err
	}
	defer func() {
		resultError = errors.Join(resultError, sourceDigests.Close())
	}()
	targetDigests, err := hashutil.NewChunkDigestStore(chunkCount)
	if err != nil {
		return err
	}
	defer func() {
		resultError = errors.Join(resultError, targetDigests.Close())
	}()
	buffers := newChunkBufferPool()
	var group sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	var progressMutex sync.Mutex
	var processed uint64

	recordError := func(err error) {
		errorOnce.Do(func() {
			firstError = err
			cancel()
		})
	}
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for job := range jobs {
				if err := workContext.Err(); err != nil {
					return
				}
				offset := job.index * hashutil.ChunkSize
				length := hashutil.ChunkSize
				if expectedSize-offset < length {
					length = expectedSize - offset
				}
				buffer, pooled := buffers.get(length)
				if _, err := io.ReadFull(io.NewSectionReader(source, int64(offset), int64(length)), buffer); err != nil {
					buffers.put(pooled)
					recordError(fmt.Errorf("read sparse source chunk %d: %w", job.index, err))
					return
				}
				if err := sourceDigests.Set(job.index, hashutil.ChunkDigestBytes(buffer)); err != nil {
					buffers.put(pooled)
					recordError(err)
					return
				}
				for _, record := range job.plan.records {
					start := uint64(record.offset)
					end := start + uint64(record.length)
					dataStart := uint64(record.dataOffset)
					dataEnd := dataStart + uint64(record.length)
					if end > uint64(len(buffer)) || dataEnd > uint64(len(job.plan.data)) {
						buffers.put(pooled)
						recordError(fmt.Errorf("sparse replacement exceeds chunk %d", job.index))
						return
					}
					copy(buffer[int(start):int(end)], job.plan.data[int(dataStart):int(dataEnd)])
				}
				if err := targetDigests.Set(job.index, hashutil.ChunkDigestBytes(buffer)); err != nil {
					buffers.put(pooled)
					recordError(err)
					return
				}
				if _, err := output.WriteAt(buffer, int64(offset)); err != nil {
					buffers.put(pooled)
					recordError(fmt.Errorf("write sparse output chunk %d: %w", job.index, err))
					return
				}
				buffers.put(pooled)

				progressMutex.Lock()
				processed += length
				eventCopy := event
				eventCopy.ProcessedBytes = processed
				eventCopy.TotalBytes = expectedSize
				progress.Report(callback, eventCopy)
				progressMutex.Unlock()
			}
		}()
	}

	parseError := streamSparseChunkPlans(workContext, operations, expectedSize, func(index uint64, plan sparseChunkPlan) error {
		select {
		case jobs <- sparseChunkJob{index: index, plan: plan}:
			return nil
		case <-workContext.Done():
			return workContext.Err()
		}
	})
	if parseError != nil {
		cancel()
	}
	close(jobs)
	group.Wait()
	if firstError != nil {
		return firstError
	}
	if parseError != nil {
		return parseError
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	sourceRoot, err := sourceDigests.Root(expectedSize)
	if err != nil {
		return err
	}
	targetRoot, err := targetDigests.Root(expectedSize)
	if err != nil {
		return err
	}
	if sourceRoot != expectedSourceHash {
		return fmt.Errorf("installed source failed BLAKE3 tree verification")
	}
	if targetRoot != expectedTargetHash {
		return fmt.Errorf("generated output failed BLAKE3 tree verification")
	}
	return nil
}
