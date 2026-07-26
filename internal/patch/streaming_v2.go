package patch

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

var errCopyAddCandidateRejected = errors.New("copy-add candidate rejected")

func createSparseStreamsOptimized(ctx context.Context, sourcePath, targetPath, forwardPath, reversePath string, size uint64) (sparseStats, bool, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return sparseStats{}, false, fmt.Errorf("open sparse source: %w", err)
	}
	defer source.Close()
	target, err := os.Open(targetPath)
	if err != nil {
		return sparseStats{}, false, fmt.Errorf("open sparse target: %w", err)
	}
	defer target.Close()

	forward, err := os.OpenFile(forwardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sparseStats{}, false, fmt.Errorf("create forward sparse stream: %w", err)
	}
	var reverse *os.File
	committed := false
	defer func() {
		_ = forward.Close()
		if reverse != nil {
			_ = reverse.Close()
		}
		if !committed {
			_ = os.Remove(forwardPath)
			if reversePath != "" {
				_ = os.Remove(reversePath)
			}
		}
	}()

	if reversePath != "" {
		reverse, err = os.OpenFile(reversePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return sparseStats{}, false, fmt.Errorf("create reverse sparse stream: %w", err)
		}
	}

	forwardWriter := bufio.NewWriterSize(forward, sparseIOBufferSize)
	if _, err := forwardWriter.Write(sparseMagic[:]); err != nil {
		return sparseStats{}, false, err
	}
	var reverseWriter *bufio.Writer
	if reverse != nil {
		reverseWriter = bufio.NewWriterSize(reverse, sparseIOBufferSize)
		if _, err := reverseWriter.Write(sparseMagic[:]); err != nil {
			return sparseStats{}, false, err
		}
	}

	stats := sparseStats{expandedSize: uint64(len(sparseMagic))}
	if !sparseWorthUsing(stats, size) && size != 0 {
		return stats, false, nil
	}

	sourceBuffer := make([]byte, sparseIOBufferSize)
	targetBuffer := make([]byte, sparseIOBufferSize)
	var absoluteOffset uint64
	var previousEnd uint64
	for {
		if err := ctx.Err(); err != nil {
			return sparseStats{}, false, err
		}
		sourceCount, sourceError := io.ReadFull(source, sourceBuffer)
		targetCount, targetError := io.ReadFull(target, targetBuffer)
		if sourceError == io.ErrUnexpectedEOF {
			sourceError = nil
		}
		if targetError == io.ErrUnexpectedEOF {
			targetError = nil
		}
		if sourceError != nil && sourceError != io.EOF {
			return sparseStats{}, false, fmt.Errorf("read sparse source: %w", sourceError)
		}
		if targetError != nil && targetError != io.EOF {
			return sparseStats{}, false, fmt.Errorf("read sparse target: %w", targetError)
		}
		if sourceCount != targetCount {
			return sparseStats{}, false, fmt.Errorf("sparse method requires equal source and target sizes")
		}
		if sourceCount == 0 {
			break
		}

		for index := 0; index < sourceCount; {
			if sourceBuffer[index] == targetBuffer[index] {
				index++
				continue
			}
			start := index
			for index < sourceCount && sourceBuffer[index] != targetBuffer[index] {
				index++
			}
			length := index - start
			startOffset := absoluteOffset + uint64(start)
			gap := startOffset - previousEnd
			nextStats := stats
			nextStats.changedBytes += uint64(length)
			nextStats.expandedSize += uint64(uvarintLength(gap) + uvarintLength(uint64(length)) + length)
			if !sparseWorthUsing(nextStats, size) {
				return nextStats, false, nil
			}
			if err := writeSparseRecord(forwardWriter, gap, targetBuffer[start:index]); err != nil {
				return sparseStats{}, false, fmt.Errorf("write forward sparse record: %w", err)
			}
			if reverseWriter != nil {
				if err := writeSparseRecord(reverseWriter, gap, sourceBuffer[start:index]); err != nil {
					return sparseStats{}, false, fmt.Errorf("write reverse sparse record: %w", err)
				}
			}
			previousEnd = startOffset + uint64(length)
			stats = nextStats
		}
		absoluteOffset += uint64(sourceCount)
		if sourceError == io.EOF || targetError == io.EOF {
			break
		}
	}

	stats.expandedSize += 2
	if !sparseWorthUsing(stats, size) {
		return stats, false, nil
	}
	if err := writeSparseTerminator(forwardWriter); err != nil {
		return sparseStats{}, false, err
	}
	if err := forwardWriter.Flush(); err != nil {
		return sparseStats{}, false, err
	}
	if err := forward.Close(); err != nil {
		return sparseStats{}, false, err
	}
	if reverseWriter != nil {
		if err := writeSparseTerminator(reverseWriter); err != nil {
			return sparseStats{}, false, err
		}
		if err := reverseWriter.Flush(); err != nil {
			return sparseStats{}, false, err
		}
		if err := reverse.Close(); err != nil {
			return sparseStats{}, false, err
		}
	}
	committed = true
	return stats, true, nil
}

func createCopyAddStreamOptimized(ctx context.Context, sourcePath, targetPath, outputPath string, targetSize uint64) (copyAddStats, bool, error) {
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

	index := make(map[[32]byte][]indexedChunk)
	if err := forEachContentChunk(ctx, source, func(chunk contentChunk) error {
		digest := sha256.Sum256(chunk.data)
		candidates := index[digest]
		if len(candidates) < copyAddMaxCandidates {
			index[digest] = append(candidates, indexedChunk{offset: chunk.offset, length: uint32(len(chunk.data))})
		}
		return nil
	}); err != nil {
		return copyAddStats{}, false, fmt.Errorf("index copy-add source: %w", err)
	}

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
	compareBuffer := make([]byte, copyAddChunkMax)
	minimumCopied := targetSize / 8
	var processed uint64
	var stats copyAddStats
	iterationError := forEachContentChunk(ctx, target, func(chunk contentChunk) error {
		chunkSize := uint64(len(chunk.data))
		if processed > targetSize || chunkSize > targetSize-processed {
			return fmt.Errorf("copy-add target changed while it was being read")
		}
		processed += chunkSize
		digest := sha256.Sum256(chunk.data)
		matched := false
		for _, candidate := range index[digest] {
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

func applySparseStreamContext(ctx context.Context, source *os.File, operations io.Reader, output *os.File, expectedSize uint64, expectedSourceHash, expectedTargetHash string, callback progress.Callback, event progress.Event) error {
	reader := bufio.NewReaderSize(operations, sparseIOBufferSize)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return fmt.Errorf("read sparse stream magic: %w", err)
	}
	if magic != sparseMagic {
		return fmt.Errorf("invalid sparse stream magic")
	}

	sourceHash := sha256.New()
	targetHash := sha256.New()
	buffer := make([]byte, sparseIOBufferSize)
	var sourcePosition uint64
	lastReported := uint64(0)
	report := func(force bool) {
		if callback == nil {
			return
		}
		if !force && sourcePosition-lastReported < 8<<20 {
			return
		}
		lastReported = sourcePosition
		event.ProcessedBytes = sourcePosition
		event.TotalBytes = expectedSize
		progress.Report(callback, event)
	}
	copyUnchanged := func(length uint64) error {
		for length > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk := uint64(len(buffer))
			if length < chunk {
				chunk = length
			}
			if _, err := io.ReadFull(source, buffer[:chunk]); err != nil {
				return err
			}
			_, _ = sourceHash.Write(buffer[:chunk])
			_, _ = targetHash.Write(buffer[:chunk])
			if _, err := output.Write(buffer[:chunk]); err != nil {
				return err
			}
			sourcePosition += chunk
			length -= chunk
			report(false)
		}
		return nil
	}
	consumeChangedSource := func(length uint64) error {
		for length > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk := uint64(len(buffer))
			if length < chunk {
				chunk = length
			}
			if _, err := io.ReadFull(source, buffer[:chunk]); err != nil {
				return err
			}
			_, _ = sourceHash.Write(buffer[:chunk])
			sourcePosition += chunk
			length -= chunk
			report(false)
		}
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap, err := binary.ReadUvarint(reader)
		if err != nil {
			return fmt.Errorf("read sparse gap: %w", err)
		}
		length, err := binary.ReadUvarint(reader)
		if err != nil {
			return fmt.Errorf("read sparse replacement length: %w", err)
		}
		if length == 0 {
			if gap != 0 {
				return fmt.Errorf("invalid sparse terminator")
			}
			break
		}
		if gap > expectedSize-sourcePosition || length > expectedSize-sourcePosition-gap {
			return fmt.Errorf("sparse operation exceeds expected file size")
		}
		if err := copyUnchanged(gap); err != nil {
			return fmt.Errorf("copy unchanged source bytes: %w", err)
		}
		if err := consumeChangedSource(length); err != nil {
			return fmt.Errorf("consume replaced source bytes: %w", err)
		}
		for remaining := length; remaining > 0; {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk := uint64(len(buffer))
			if remaining < chunk {
				chunk = remaining
			}
			if _, err := io.ReadFull(reader, buffer[:chunk]); err != nil {
				return fmt.Errorf("read sparse replacement bytes: %w", err)
			}
			_, _ = targetHash.Write(buffer[:chunk])
			if _, err := output.Write(buffer[:chunk]); err != nil {
				return fmt.Errorf("write sparse replacement bytes: %w", err)
			}
			remaining -= chunk
		}
	}
	if sourcePosition > expectedSize {
		return fmt.Errorf("sparse source position exceeds expected size")
	}
	if err := copyUnchanged(expectedSize - sourcePosition); err != nil {
		return fmt.Errorf("copy sparse tail: %w", err)
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("sparse stream contains trailing data")
		}
		return fmt.Errorf("inspect sparse stream tail: %w", err)
	}
	report(true)
	if hex.EncodeToString(sourceHash.Sum(nil)) != expectedSourceHash {
		return fmt.Errorf("installed source failed SHA-256 verification")
	}
	if hex.EncodeToString(targetHash.Sum(nil)) != expectedTargetHash {
		return fmt.Errorf("generated output failed SHA-256 verification")
	}
	return nil
}

func applyCopyAddStreamContext(ctx context.Context, source *os.File, operations io.Reader, output *os.File, expectedSourceSize, expectedTargetSize uint64, expectedTargetHash string, callback progress.Callback, event progress.Event) error {
	reader := bufio.NewReaderSize(operations, sparseIOBufferSize)
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return fmt.Errorf("read copy-add stream magic: %w", err)
	}
	if magic != copyAddMagic {
		return fmt.Errorf("invalid copy-add stream magic")
	}

	outputHash := sha256.New()
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
		_, _ = outputHash.Write(block)
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
			if hex.EncodeToString(outputHash.Sum(nil)) != expectedTargetHash {
				return fmt.Errorf("generated output failed SHA-256 verification")
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

const instructionMemoryThreshold uint64 = 2 << 20

func applyCompressedInstructionStream(ctx context.Context, decoder *zstd.Decoder, patch *os.File, offset, length, expandedLength uint64, apply func(io.Reader) error) error {
	if expandedLength <= instructionMemoryThreshold {
		var buffer bytes.Buffer
		buffer.Grow(int(expandedLength))
		if err := decoder.DecompressSegmentToWriter(ctx, nil, patch, offset, length, &buffer, expandedLength, nil); err != nil {
			return err
		}
		return apply(bytes.NewReader(buffer.Bytes()))
	}

	decodeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	decodeDone := make(chan error, 1)
	go func() {
		err := decoder.DecompressSegmentToWriter(decodeContext, nil, patch, offset, length, writer, expandedLength, nil)
		_ = writer.CloseWithError(err)
		decodeDone <- err
	}()

	applyError := apply(reader)
	if applyError != nil {
		cancel()
		_ = reader.CloseWithError(applyError)
	} else {
		_ = reader.Close()
	}
	decodeError := <-decodeDone
	if applyError == nil {
		return decodeError
	}
	if decodeError == nil || errors.Is(decodeError, context.Canceled) || errors.Is(decodeError, io.ErrClosedPipe) || errors.Is(applyError, decodeError) {
		return applyError
	}
	return errors.Join(applyError, decodeError)
}

func hashReaderContext(ctx context.Context, reader io.Reader, onProgress func(uint64)) (string, uint64, error) {
	hash := sha256.New()
	buffer := make([]byte, sparseIOBufferSize)
	var size uint64
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		count, err := reader.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
			size += uint64(count)
			if onProgress != nil {
				onProgress(size)
			}
		}
		if err == io.EOF {
			return hex.EncodeToString(hash.Sum(nil)), size, nil
		}
		if err != nil {
			return "", 0, err
		}
	}
}

func verifySourceForDecode(ctx context.Context, source *os.File, expected fileState, callback progress.Callback, event progress.Event) error {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	event.Stage = progress.StageVerifying
	event.ProcessedBytes = 0
	event.TotalBytes = expected.size
	progress.Report(callback, event)
	lastReported := uint64(0)
	digest, size, err := hashReaderContext(ctx, source, func(processed uint64) {
		if processed != expected.size && processed-lastReported < 8<<20 {
			return
		}
		lastReported = processed
		event.ProcessedBytes = processed
		progress.Report(callback, event)
	})
	if err != nil {
		return err
	}
	if digest != expected.hash || size != expected.size {
		return fmt.Errorf("source SHA-256 or size does not match patch metadata")
	}
	_, err = source.Seek(0, io.SeekStart)
	return err
}
