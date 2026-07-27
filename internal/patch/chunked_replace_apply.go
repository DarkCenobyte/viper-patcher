package patch

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

// readChunkedReplace parses descriptors and validates canonical chunk
// boundaries before any decompression starts.
func readChunkedReplace(patch *os.File, offset, length, expectedSize uint64) ([]chunkedReplaceDescriptor, uint64, error) {
	if patch == nil {
		return nil, 0, fmt.Errorf("chunked replace patch file is unavailable")
	}
	if length < chunkedHeaderSize {
		return nil, 0, fmt.Errorf("truncated chunked replace header")
	}
	reader := io.NewSectionReader(patch, int64(offset), int64(length))
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return nil, 0, err
	}
	if magic != chunkedReplaceMagic {
		return nil, 0, fmt.Errorf("invalid chunked replace magic")
	}
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return nil, 0, err
	}
	expectedCount := (expectedSize + hashutil.ChunkSize - 1) / hashutil.ChunkSize
	if count == 0 || uint64(count) != expectedCount || uint64(count) > (length-chunkedHeaderSize)/chunkedDescriptorSize {
		return nil, 0, fmt.Errorf("invalid chunked replace descriptor count")
	}

	descriptors := make([]chunkedReplaceDescriptor, int(count))
	var payloadLength uint64
	for index := range descriptors {
		descriptor := &descriptors[index]
		if err := binary.Read(reader, binary.LittleEndian, &descriptor.offset); err != nil {
			return nil, 0, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &descriptor.size); err != nil {
			return nil, 0, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &descriptor.compressedLength); err != nil {
			return nil, 0, err
		}
		var digest [32]byte
		if _, err := io.ReadFull(reader, digest[:]); err != nil {
			return nil, 0, err
		}
		descriptor.digest = hex.EncodeToString(digest[:])

		expectedOffset := uint64(index) * hashutil.ChunkSize
		expectedChunkSize := hashutil.ChunkSize
		if expectedSize-expectedOffset < expectedChunkSize {
			expectedChunkSize = expectedSize - expectedOffset
		}
		if descriptor.offset != expectedOffset || descriptor.size != expectedChunkSize || descriptor.compressedLength == 0 {
			return nil, 0, fmt.Errorf("invalid chunked replace descriptor %d", index)
		}
		var ok bool
		payloadLength, ok = checkedAdd(payloadLength, descriptor.compressedLength)
		if !ok {
			return nil, 0, fmt.Errorf("chunked replace payload size overflows")
		}
	}
	headerLength := chunkedHeaderSize + uint64(count)*chunkedDescriptorSize
	totalLength, ok := checkedAdd(headerLength, payloadLength)
	if !ok || totalLength != length {
		return nil, 0, fmt.Errorf("chunked replace payload contains gaps or trailing data")
	}
	payloadOffset, ok := checkedAdd(offset, headerLength)
	if !ok {
		return nil, 0, fmt.Errorf("chunked replace payload offset overflows")
	}
	return descriptors, payloadOffset, nil
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

func applyChunkedReplace(ctx context.Context, source, patch, output *os.File, patchOffset, patchLength uint64, expectedInput, expectedOutput fileState, workers int, callback progress.Callback, event progress.Event, decoders *decoderPool) error {
	descriptors, payloadOffset, err := readChunkedReplace(patch, patchOffset, patchLength, expectedOutput.size)
	if err != nil {
		return err
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(descriptors) {
		workers = len(descriptors)
	}
	if err := output.Truncate(int64(expectedOutput.size)); err != nil {
		return err
	}

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

	computed := make([][32]byte, len(descriptors))
	payloadOffsets := make([]uint64, len(descriptors))
	var payloadCursor uint64
	for index, descriptor := range descriptors {
		payloadOffsets[index] = payloadCursor
		payloadCursor += descriptor.compressedLength
	}
	var progressMutex sync.Mutex
	var processed uint64
	applyError := parallelFor(workContext, len(descriptors), workers, func(ctx context.Context, index int) error {
		descriptor := descriptors[index]
		decoder := decoders.acquire()
		defer decoders.release(decoder)
		hasher := hashutil.NewChunkHasher()
		writer := &sequentialWriteAt{file: output, offset: int64(descriptor.offset)}
		if err := decoder.DecompressSegmentToWriter(
			ctx,
			patch,
			payloadOffset+payloadOffsets[index],
			descriptor.compressedLength,
			io.MultiWriter(writer, hasher),
			descriptor.size,
			nil,
		); err != nil {
			return err
		}
		if writer.written != descriptor.size {
			return fmt.Errorf("chunk %d has an unexpected size", index)
		}
		sum := hasher.Sum(nil)
		if len(sum) != 32 {
			return fmt.Errorf("chunk %d produced an invalid BLAKE3 digest", index)
		}
		copy(computed[index][:], sum)
		if hex.EncodeToString(computed[index][:]) != descriptor.digest {
			return fmt.Errorf("chunk %d failed BLAKE3 verification", index)
		}
		progressMutex.Lock()
		processed += descriptor.size
		eventCopy := event
		eventCopy.ProcessedBytes = processed
		eventCopy.TotalBytes = expectedOutput.size
		progress.Report(callback, eventCopy)
		progressMutex.Unlock()
		return nil
	})
	if applyError != nil {
		cancel()
	}
	sourceError := <-sourceResult
	if applyError != nil || sourceError != nil {
		return errors.Join(applyError, sourceError)
	}
	root := hashutil.RootFromChunkDigestBytes(expectedOutput.size, computed)
	if root != expectedOutput.hash {
		return fmt.Errorf("generated output failed BLAKE3 tree verification")
	}
	return nil
}
