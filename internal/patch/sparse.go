package patch

import (
	"context"
	"encoding/binary"
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
	info, err := os.Stat(sourcePath)
	if err != nil {
		return sparseStats{}, fmt.Errorf("inspect sparse source: %w", err)
	}
	if info.Size() < 0 {
		return sparseStats{}, fmt.Errorf("sparse source has an invalid size")
	}
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return sparseStats{}, fmt.Errorf("inspect sparse target: %w", err)
	}
	if targetInfo.Size() != info.Size() {
		return sparseStats{}, fmt.Errorf("sparse method requires equal source and target sizes")
	}
	stats, _, err := createSparseStreamsOptimized(context.Background(), sourcePath, targetPath, forwardPath, reversePath, uint64(info.Size()))
	return stats, err
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
	return applySparseStreamContext(context.Background(), source, operations, output, expectedSize, expectedSourceHash, expectedTargetHash, callback, event)
}
