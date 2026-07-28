package patch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

var (
	errCopyAddCandidateRejected   = errors.New("copy-add candidate rejected")
	errCopyAddIndexBudgetExceeded = errors.New("copy-add index memory budget exceeded")
)

func createCopyAddStreamOptimized(ctx context.Context, sourcePath, targetPath, outputPath string, targetSize uint64) (copyAddStats, bool, error) {
	return createCopyAddStreamOptimizedWithBudget(ctx, sourcePath, targetPath, outputPath, targetSize, nil)
}

func createCopyAddStreamOptimizedWithBudget(ctx context.Context, sourcePath, targetPath, outputPath string, targetSize uint64, indexBudget *copyAddIndexBudget) (copyAddStats, bool, error) {
	if targetSize == 0 {
		return copyAddStats{}, false, nil
	}
	expandedLimit, ok := copyAddExpandedLimit(targetSize)
	if !ok {
		return copyAddStats{}, false, nil
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return copyAddStats{}, false, fmt.Errorf("open copy-add source: %w", err)
	}
	defer source.Close()
	sourceInfo, err := source.Stat()
	if err != nil {
		return copyAddStats{}, false, fmt.Errorf("inspect copy-add source: %w", err)
	}
	if sourceInfo.Size() < 0 {
		return copyAddStats{}, false, fmt.Errorf("copy-add source has an invalid size")
	}
	profileSize := targetSize
	if uint64(sourceInfo.Size()) > profileSize {
		profileSize = uint64(sourceInfo.Size())
	}
	profile := copyAddProfileForSize(profileSize)
	if !profile.indexable {
		return copyAddStats{}, false, nil
	}

	indexCapacity := copyAddIndexCapacity(uint64(sourceInfo.Size()), profile)
	releaseIndexBudget, err := indexBudget.acquire(ctx, copyAddIndexAllocationBytes(indexCapacity))
	if err != nil {
		return copyAddStats{}, false, err
	}
	defer releaseIndexBudget()

	index := newCopyAddIndex(indexCapacity)
	if err := forEachContentChunk(ctx, source, profile, func(chunk contentChunk) error {
		digest := hashutil.ChunkDigestBytes(chunk.data)
		return index.add(digest, indexedChunk{offset: chunk.offset, length: uint32(len(chunk.data))})
	}); err != nil {
		if errors.Is(err, errCopyAddIndexBudgetExceeded) {
			return copyAddStats{}, false, nil
		}
		return copyAddStats{}, false, fmt.Errorf("index copy-add source: %w", err)
	}
	index.finalize()

	target, err := os.Open(targetPath)
	if err != nil {
		return copyAddStats{}, false, fmt.Errorf("open copy-add target: %w", err)
	}
	defer target.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return copyAddStats{}, false, fmt.Errorf("create copy-add stream: %w", err)
	}
	committed := false
	defer func() {
		_ = output.Close()
		if !committed {
			_ = os.Remove(outputPath)
		}
	}()

	stream := &copyAddStreamWriter{writer: bufio.NewWriterSize(output, sparseIOBufferSize)}
	if _, err := stream.writer.Write(copyAddMagic[:]); err != nil {
		return copyAddStats{}, false, err
	}
	stream.expandedSize = uint64(len(copyAddMagic))
	compareBuffer := make([]byte, profile.maximum)
	minimumCopied := targetSize / 8
	var processed uint64
	var stats copyAddStats
	iterationError := forEachContentChunk(ctx, target, profile, func(chunk contentChunk) error {
		chunkSize := uint64(len(chunk.data))
		if processed > targetSize || chunkSize > targetSize-processed {
			return fmt.Errorf("copy-add target changed while it was being read")
		}
		processed += chunkSize
		digest := hashutil.ChunkDigestBytes(chunk.data)
		matched := false
		candidates := index.candidates(digest)
		for candidateIndex := 0; candidateIndex < int(candidates.count); candidateIndex++ {
			candidate := candidates.chunks[candidateIndex]
			if int(candidate.length) != len(chunk.data) {
				continue
			}
			if _, err := source.ReadAt(compareBuffer[:len(chunk.data)], int64(candidate.offset)); err != nil {
				return fmt.Errorf("verify copy-add source chunk: %w", err)
			}
			if !bytes.Equal(compareBuffer[:len(chunk.data)], chunk.data) {
				continue
			}
			if err := stream.copy(candidate.offset, chunkSize); err != nil {
				return err
			}
			stats.copiedBytes += chunkSize
			matched = true
			break
		}
		if !matched {
			if err := stream.add(chunk.data); err != nil {
				return err
			}
		}

		estimated, ok := stream.estimatedExpandedSize()
		if !ok || estimated > expandedLimit {
			return errCopyAddCandidateRejected
		}
		remaining := targetSize - processed
		maximumCopied, ok := checkedAdd(stats.copiedBytes, remaining)
		if ok && maximumCopied < minimumCopied {
			return errCopyAddCandidateRejected
		}
		return nil
	})
	if errors.Is(iterationError, errCopyAddCandidateRejected) {
		return stats, false, nil
	}
	if iterationError != nil {
		return copyAddStats{}, false, fmt.Errorf("build copy-add stream: %w", iterationError)
	}
	if processed != targetSize {
		return copyAddStats{}, false, fmt.Errorf("copy-add target changed size while it was being read")
	}
	if err := stream.close(); err != nil {
		return copyAddStats{}, false, err
	}
	if err := output.Close(); err != nil {
		return copyAddStats{}, false, err
	}
	stats.expandedSize = stream.expandedSize
	if !copyAddWorthUsing(stats, targetSize) {
		return stats, false, nil
	}
	committed = true
	return stats, true, nil
}

func copyAddExpandedLimit(targetSize uint64) (uint64, bool) {
	if targetSize == 0 {
		return 0, false
	}
	return checkedAdd(targetSize-targetSize/4, 1<<20)
}

func (stream *copyAddStreamWriter) estimatedExpandedSize() (uint64, bool) {
	total := stream.expandedSize
	var ok bool
	if len(stream.pendingAdd) > 0 {
		total, ok = checkedAddMany(total, 1, uint64(uvarintLength(uint64(len(stream.pendingAdd)))), uint64(len(stream.pendingAdd)))
		if !ok {
			return 0, false
		}
	}
	if stream.copyLength > 0 {
		total, ok = checkedAddMany(total, 1, uint64(uvarintLength(stream.copyOffset)), uint64(uvarintLength(stream.copyLength)))
		if !ok {
			return 0, false
		}
	}
	return checkedAdd(total, 1)
}

func checkedAddMany(values ...uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		var ok bool
		total, ok = checkedAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

// applyCopyAddStreamContext executes one validated COPY/ADD instruction stream.
func applyCopyAddStreamContext(ctx context.Context, source *os.File, operations io.Reader, output *os.File, expectedSourceSize, expectedTargetSize uint64, expectedTargetHash string, callback progress.Callback, event progress.Event) (resultError error) {
	reader := bufio.NewReaderSize(operations, sparseIOBufferSize)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return fmt.Errorf("read copy-add stream magic: %w", err)
	}
	if magic != copyAddMagic {
		return fmt.Errorf("invalid copy-add stream magic")
	}

	outputHash, err := hashutil.NewSizedAccumulator(expectedTargetSize)
	if err != nil {
		return err
	}
	defer func() {
		resultError = errors.Join(resultError, outputHash.Close())
	}()
	buffer := make([]byte, sparseIOBufferSize)
	var produced uint64
	lastReported := uint64(0)
	report := func(force bool) {
		if callback == nil {
			return
		}
		if !force && produced-lastReported < 8<<20 {
			return
		}
		lastReported = produced
		event.ProcessedBytes = produced
		event.TotalBytes = expectedTargetSize
		progress.Report(callback, event)
	}
	writeBlock := func(block []byte) error {
		if produced > expectedTargetSize || uint64(len(block)) > expectedTargetSize-produced {
			return fmt.Errorf("copy-add output exceeds declared size")
		}
		if _, err := output.Write(block); err != nil {
			return err
		}
		if _, err := outputHash.Write(block); err != nil {
			return err
		}
		produced += uint64(len(block))
		report(false)
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		opcode, err := reader.ReadByte()
		if err != nil {
			return fmt.Errorf("read copy-add opcode: %w", err)
		}
		switch opcode {
		case copyAddOpcodeEnd:
			if produced != expectedTargetSize {
				return fmt.Errorf("copy-add output size is %d, expected %d", produced, expectedTargetSize)
			}
			if _, err := reader.ReadByte(); err != io.EOF {
				if err == nil {
					return fmt.Errorf("copy-add stream contains trailing data")
				}
				return fmt.Errorf("inspect copy-add stream tail: %w", err)
			}
			report(true)
			actualHash, err := outputHash.SumHex()
			if err != nil {
				return err
			}
			if actualHash != expectedTargetHash {
				return fmt.Errorf("generated output failed BLAKE3 tree verification")
			}
			return nil
		case copyAddOpcodeCopy:
			offset, err := binary.ReadUvarint(reader)
			if err != nil {
				return fmt.Errorf("read copy offset: %w", err)
			}
			length, err := binary.ReadUvarint(reader)
			if err != nil {
				return fmt.Errorf("read copy length: %w", err)
			}
			if length == 0 || offset > expectedSourceSize || length > expectedSourceSize-offset {
				return fmt.Errorf("copy-add COPY range exceeds source size")
			}
			for copied := uint64(0); copied < length; {
				if err := ctx.Err(); err != nil {
					return err
				}
				chunk := uint64(len(buffer))
				if length-copied < chunk {
					chunk = length - copied
				}
				if _, err := source.ReadAt(buffer[:chunk], int64(offset+copied)); err != nil {
					return fmt.Errorf("read COPY source range: %w", err)
				}
				if err := writeBlock(buffer[:chunk]); err != nil {
					return fmt.Errorf("write COPY output: %w", err)
				}
				copied += chunk
			}
		case copyAddOpcodeAdd:
			length, err := binary.ReadUvarint(reader)
			if err != nil {
				return fmt.Errorf("read ADD length: %w", err)
			}
			if length == 0 || length > expectedTargetSize-produced {
				return fmt.Errorf("copy-add ADD range exceeds target size")
			}
			for added := uint64(0); added < length; {
				if err := ctx.Err(); err != nil {
					return err
				}
				chunk := uint64(len(buffer))
				if length-added < chunk {
					chunk = length - added
				}
				if _, err := io.ReadFull(reader, buffer[:chunk]); err != nil {
					return fmt.Errorf("read ADD payload: %w", err)
				}
				if err := writeBlock(buffer[:chunk]); err != nil {
					return fmt.Errorf("write ADD output: %w", err)
				}
				added += chunk
			}
		default:
			return fmt.Errorf("unsupported copy-add opcode %d", opcode)
		}
	}
}
