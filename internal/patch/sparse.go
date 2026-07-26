package patch

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

var sparseMagic = [8]byte{'V', 'S', 'P', 'R', '\r', '\n', 0x1a, 0x01}

const sparseIOBufferSize = 1 << 20

type sparseStats struct {
	changedBytes uint64
	expandedSize uint64
}

func createSparseStreams(sourcePath, targetPath, forwardPath, reversePath string) (sparseStats, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return sparseStats{}, fmt.Errorf("open sparse source: %w", err)
	}
	defer source.Close()
	target, err := os.Open(targetPath)
	if err != nil {
		return sparseStats{}, fmt.Errorf("open sparse target: %w", err)
	}
	defer target.Close()

	forward, err := os.OpenFile(forwardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sparseStats{}, fmt.Errorf("create forward sparse stream: %w", err)
	}
	forwardCommitted := false
	defer func() {
		_ = forward.Close()
		if !forwardCommitted {
			_ = os.Remove(forwardPath)
		}
	}()

	var reverse *os.File
	if reversePath != "" {
		reverse, err = os.OpenFile(reversePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return sparseStats{}, fmt.Errorf("create reverse sparse stream: %w", err)
		}
		defer reverse.Close()
	}

	forwardWriter := bufio.NewWriterSize(forward, sparseIOBufferSize)
	if _, err := forwardWriter.Write(sparseMagic[:]); err != nil {
		return sparseStats{}, err
	}
	var reverseWriter *bufio.Writer
	if reverse != nil {
		reverseWriter = bufio.NewWriterSize(reverse, sparseIOBufferSize)
		if _, err := reverseWriter.Write(sparseMagic[:]); err != nil {
			return sparseStats{}, err
		}
	}

	sourceBuffer := make([]byte, sparseIOBufferSize)
	targetBuffer := make([]byte, sparseIOBufferSize)
	var absoluteOffset uint64
	var previousEnd uint64
	stats := sparseStats{expandedSize: uint64(len(sparseMagic))}

	for {
		sourceCount, sourceError := io.ReadFull(source, sourceBuffer)
		targetCount, targetError := io.ReadFull(target, targetBuffer)
		if sourceError == io.ErrUnexpectedEOF {
			sourceError = nil
		}
		if targetError == io.ErrUnexpectedEOF {
			targetError = nil
		}
		if sourceError != nil && sourceError != io.EOF {
			return sparseStats{}, fmt.Errorf("read sparse source: %w", sourceError)
		}
		if targetError != nil && targetError != io.EOF {
			return sparseStats{}, fmt.Errorf("read sparse target: %w", targetError)
		}
		if sourceCount != targetCount {
			return sparseStats{}, fmt.Errorf("sparse method requires equal source and target sizes")
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
			if err := writeSparseRecord(forwardWriter, gap, targetBuffer[start:index]); err != nil {
				return sparseStats{}, fmt.Errorf("write forward sparse record: %w", err)
			}
			if reverseWriter != nil {
				if err := writeSparseRecord(reverseWriter, gap, sourceBuffer[start:index]); err != nil {
					return sparseStats{}, fmt.Errorf("write reverse sparse record: %w", err)
				}
			}
			previousEnd = startOffset + uint64(length)
			stats.changedBytes += uint64(length)
			stats.expandedSize += uint64(uvarintLength(gap) + uvarintLength(uint64(length)) + length)
		}
		absoluteOffset += uint64(sourceCount)
		if sourceError == io.EOF || targetError == io.EOF {
			break
		}
	}

	if err := writeSparseTerminator(forwardWriter); err != nil {
		return sparseStats{}, err
	}
	stats.expandedSize += 2
	if err := forwardWriter.Flush(); err != nil {
		return sparseStats{}, err
	}
	if err := forward.Close(); err != nil {
		return sparseStats{}, err
	}
	forwardCommitted = true

	if reverseWriter != nil {
		if err := writeSparseTerminator(reverseWriter); err != nil {
			return sparseStats{}, err
		}
		if err := reverseWriter.Flush(); err != nil {
			return sparseStats{}, err
		}
		if err := reverse.Close(); err != nil {
			return sparseStats{}, err
		}
	}
	return stats, nil
}

func writeSparseRecord(writer io.Writer, gap uint64, replacement []byte) error {
	var encoded [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(encoded[:], gap)
	if _, err := writer.Write(encoded[:count]); err != nil {
		return err
	}
	count = binary.PutUvarint(encoded[:], uint64(len(replacement)))
	if _, err := writer.Write(encoded[:count]); err != nil {
		return err
	}
	_, err := writer.Write(replacement)
	return err
}

func writeSparseTerminator(writer io.Writer) error {
	_, err := writer.Write([]byte{0, 0})
	return err
}

func uvarintLength(value uint64) int {
	var encoded [binary.MaxVarintLen64]byte
	return binary.PutUvarint(encoded[:], value)
}

func applySparseStream(source, operations, output *os.File, expectedSize uint64, expectedSourceHash, expectedTargetHash string, callback progress.Callback, event progress.Event) error {
	if source == nil || operations == nil || output == nil {
		return fmt.Errorf("sparse application requires source, operations, and output files")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind sparse source: %w", err)
	}
	if _, err := operations.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind sparse operations: %w", err)
	}

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
			chunk := uint64(len(buffer))
			if length < chunk {
				chunk = length
			}
			if _, err := io.ReadFull(source, buffer[:chunk]); err != nil {
				return err
			}
			if _, err := sourceHash.Write(buffer[:chunk]); err != nil {
				return err
			}
			if _, err := targetHash.Write(buffer[:chunk]); err != nil {
				return err
			}
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
			chunk := uint64(len(buffer))
			if length < chunk {
				chunk = length
			}
			if _, err := io.ReadFull(source, buffer[:chunk]); err != nil {
				return err
			}
			if _, err := sourceHash.Write(buffer[:chunk]); err != nil {
				return err
			}
			sourcePosition += chunk
			length -= chunk
			report(false)
		}
		return nil
	}

	for {
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
		remaining := length
		for remaining > 0 {
			chunk := uint64(len(buffer))
			if remaining < chunk {
				chunk = remaining
			}
			if _, err := io.ReadFull(reader, buffer[:chunk]); err != nil {
				return fmt.Errorf("read sparse replacement bytes: %w", err)
			}
			if _, err := targetHash.Write(buffer[:chunk]); err != nil {
				return err
			}
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

	actualSourceHash := hex.EncodeToString(sourceHash.Sum(nil))
	actualTargetHash := hex.EncodeToString(targetHash.Sum(nil))
	if actualSourceHash != expectedSourceHash {
		return fmt.Errorf("installed source failed SHA-256 verification")
	}
	if actualTargetHash != expectedTargetHash {
		return fmt.Errorf("generated output failed SHA-256 verification")
	}
	return nil
}
