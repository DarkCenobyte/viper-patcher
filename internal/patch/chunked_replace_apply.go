package patch

import (
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

type chunkedReplaceLayout struct {
	count            uint64
	descriptorOffset uint64
	descriptorLength uint64
	payloadOffset    uint64
	payloadLength    uint64
}

type chunkedApplyDescriptor struct {
	offset           uint64
	size             uint64
	compressedLength uint64
	frameOffset      uint64
	digest           [32]byte
}

type chunkedApplyJob struct {
	index      uint64
	descriptor chunkedApplyDescriptor
}

// inspectChunkedReplace validates the complete descriptor table without
// retaining it. Application reads the tiny table a second time while feeding a
// bounded worker queue, so RAM does not grow with an untrusted descriptor count.
func inspectChunkedReplace(patch *os.File, offset, length, expectedSize uint64) (chunkedReplaceLayout, error) {
	if patch == nil {
		return chunkedReplaceLayout{}, fmt.Errorf("chunked replace patch file is unavailable")
	}
	if offset > math.MaxInt64 || length > math.MaxInt64-offset {
		return chunkedReplaceLayout{}, fmt.Errorf("chunked replace payload exceeds the signed 64-bit file range")
	}
	if length < chunkedHeaderSize {
		return chunkedReplaceLayout{}, fmt.Errorf("truncated chunked replace header")
	}
	reader := io.NewSectionReader(patch, int64(offset), int64(length))
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return chunkedReplaceLayout{}, err
	}
	if magic != chunkedReplaceMagic {
		return chunkedReplaceLayout{}, fmt.Errorf("invalid chunked replace magic")
	}
	var encodedCount uint32
	if err := binary.Read(reader, binary.LittleEndian, &encodedCount); err != nil {
		return chunkedReplaceLayout{}, err
	}
	count := uint64(encodedCount)
	expectedCount := hashutil.ChunkCount(expectedSize)
	if count == 0 || count != expectedCount || count > (length-chunkedHeaderSize)/chunkedDescriptorSize {
		return chunkedReplaceLayout{}, fmt.Errorf("invalid chunked replace descriptor count")
	}

	var payloadLength uint64
	for index := uint64(0); index < count; index++ {
		descriptor, err := readChunkedApplyDescriptor(reader, index, expectedSize)
		if err != nil {
			return chunkedReplaceLayout{}, err
		}
		var ok bool
		payloadLength, ok = checkedAdd(payloadLength, descriptor.compressedLength)
		if !ok {
			return chunkedReplaceLayout{}, fmt.Errorf("chunked replace payload size overflows")
		}
	}
	descriptorLength := count * chunkedDescriptorSize
	headerLength, ok := checkedAdd(chunkedHeaderSize, descriptorLength)
	if !ok {
		return chunkedReplaceLayout{}, fmt.Errorf("chunked replace header size overflows")
	}
	totalLength, ok := checkedAdd(headerLength, payloadLength)
	if !ok || totalLength != length {
		return chunkedReplaceLayout{}, fmt.Errorf("chunked replace payload contains gaps or trailing data")
	}
	descriptorOffset, ok := checkedAdd(offset, chunkedHeaderSize)
	if !ok {
		return chunkedReplaceLayout{}, fmt.Errorf("chunked replace descriptor offset overflows")
	}
	payloadOffset, ok := checkedAdd(offset, headerLength)
	if !ok {
		return chunkedReplaceLayout{}, fmt.Errorf("chunked replace payload offset overflows")
	}
	return chunkedReplaceLayout{
		count:            count,
		descriptorOffset: descriptorOffset,
		descriptorLength: descriptorLength,
		payloadOffset:    payloadOffset,
		payloadLength:    payloadLength,
	}, nil
}

func readChunkedApplyDescriptor(reader io.Reader, index, expectedSize uint64) (chunkedApplyDescriptor, error) {
	var encoded [chunkedDescriptorSize]byte
	if _, err := io.ReadFull(reader, encoded[:]); err != nil {
		return chunkedApplyDescriptor{}, err
	}
	descriptor := chunkedApplyDescriptor{
		offset:           binary.LittleEndian.Uint64(encoded[0:8]),
		size:             binary.LittleEndian.Uint64(encoded[8:16]),
		compressedLength: binary.LittleEndian.Uint64(encoded[16:24]),
	}
	copy(descriptor.digest[:], encoded[24:])

	expectedOffset := index * hashutil.ChunkSize
	expectedChunkSize := hashutil.ChunkSize
	if expectedSize-expectedOffset < expectedChunkSize {
		expectedChunkSize = expectedSize - expectedOffset
	}
	if descriptor.offset != expectedOffset || descriptor.size != expectedChunkSize || descriptor.compressedLength == 0 {
		return chunkedApplyDescriptor{}, fmt.Errorf("invalid chunked replace descriptor %d", index)
	}
	return descriptor, nil
}

func streamChunkedApplyDescriptors(ctx context.Context, patch *os.File, layout chunkedReplaceLayout, expectedSize uint64, emit func(chunkedApplyJob) error) error {
	reader := io.NewSectionReader(patch, int64(layout.descriptorOffset), int64(layout.descriptorLength))
	var payloadCursor uint64
	for index := uint64(0); index < layout.count; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		descriptor, err := readChunkedApplyDescriptor(reader, index, expectedSize)
		if err != nil {
			return err
		}
		frameOffset, ok := checkedAdd(layout.payloadOffset, payloadCursor)
		if !ok {
			return fmt.Errorf("chunked replace frame offset overflows")
		}
		descriptor.frameOffset = frameOffset
		if err := emit(chunkedApplyJob{index: index, descriptor: descriptor}); err != nil {
			return err
		}
		payloadCursor, ok = checkedAdd(payloadCursor, descriptor.compressedLength)
		if !ok {
			return fmt.Errorf("chunked replace payload size overflows")
		}
	}
	if payloadCursor != layout.payloadLength {
		return fmt.Errorf("chunked replace descriptor table changed during application")
	}
	return nil
}

type sequentialWriteAt struct {
	file    *os.File
	offset  int64
	written uint64
}

func (writer *sequentialWriteAt) Write(data []byte) (int, error) {
	count, err := writer.file.WriteAt(data, writer.offset+int64(writer.written))
	writer.written += uint64(count)
	return count, err
}

func applyChunkedReplace(ctx context.Context, source, patch, output *os.File, patchOffset, patchLength uint64, expectedInput, expectedOutput fileState, workers int, callback progress.Callback, event progress.Event, decoders *decoderPool) (resultError error) {
	layout, err := inspectChunkedReplace(patch, patchOffset, patchLength, expectedOutput.size)
	if err != nil {
		return err
	}
	if expectedOutput.size > math.MaxInt64 {
		return fmt.Errorf("chunked replace output exceeds the signed 64-bit file range")
	}
	if workers < 1 {
		workers = 1
	}
	if uint64(workers) > layout.count {
		workers = int(layout.count)
	}
	if err := output.Truncate(int64(expectedOutput.size)); err != nil {
		return err
	}
	computed, err := hashutil.NewChunkDigestStore(layout.count)
	if err != nil {
		return err
	}
	defer func() {
		resultError = errors.Join(resultError, computed.Close())
	}()

	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	sourceResult := make(chan error, 1)
	go func() {
		digest, size, hashError := hashutil.FileParallel(workContext, source, expectedInput.size, workers, nil)
		if hashError == nil && (digest != expectedInput.hash || size != expectedInput.size) {
			hashError = fmt.Errorf("source hash or size does not match patch metadata")
		}
		if hashError != nil {
			cancel()
		}
		sourceResult <- hashError
	}()

	jobs := make(chan chunkedApplyJob, workers)
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
				descriptor := job.descriptor
				decoder, releaseDecoder, err := decoders.acquire(workContext, patch, descriptor.frameOffset, descriptor.compressedLength)
				if err != nil {
					recordError(err)
					return
				}
				hasher := hashutil.NewChunkHasher()
				writer := &sequentialWriteAt{file: output, offset: int64(descriptor.offset)}
				err = decoder.DecompressToWriter(
					workContext,
					io.MultiWriter(writer, hasher),
					descriptor.size,
					nil,
				)
				releaseDecoder()
				if err != nil {
					recordError(err)
					return
				}
				if writer.written != descriptor.size {
					recordError(fmt.Errorf("chunk %d has an unexpected size", job.index))
					return
				}
				sum := hasher.Sum(nil)
				if len(sum) != 32 {
					recordError(fmt.Errorf("chunk %d produced an invalid BLAKE3 digest", job.index))
					return
				}
				var digest [32]byte
				copy(digest[:], sum)
				if digest != descriptor.digest {
					recordError(fmt.Errorf("chunk %d failed BLAKE3 verification", job.index))
					return
				}
				if err := computed.Set(job.index, digest); err != nil {
					recordError(err)
					return
				}
				progressMutex.Lock()
				processed += descriptor.size
				eventCopy := event
				eventCopy.ProcessedBytes = processed
				eventCopy.TotalBytes = expectedOutput.size
				progress.Report(callback, eventCopy)
				progressMutex.Unlock()
			}
		}()
	}

	streamError := streamChunkedApplyDescriptors(workContext, patch, layout, expectedOutput.size, func(job chunkedApplyJob) error {
		select {
		case jobs <- job:
			return nil
		case <-workContext.Done():
			return workContext.Err()
		}
	})
	if streamError != nil {
		cancel()
	}
	close(jobs)
	group.Wait()
	sourceError := <-sourceResult
	if firstError != nil || streamError != nil || sourceError != nil {
		return errors.Join(firstError, streamError, sourceError)
	}
	root, err := computed.Root(expectedOutput.size)
	if err != nil {
		return err
	}
	if root != expectedOutput.hash {
		return fmt.Errorf("generated output failed BLAKE3 tree verification")
	}
	return nil
}
