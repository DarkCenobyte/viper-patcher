package patch

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

var copyAddMagic = [8]byte{'V', 'C', 'A', 'D', '\r', '\n', 0x1a, 0x01}

const (
	copyAddOpcodeEnd     byte = 0
	copyAddOpcodeCopy    byte = 1
	copyAddOpcodeAdd     byte = 2
	copyAddChunkMin           = 4 << 10
	copyAddChunkAvg           = 16 << 10
	copyAddChunkMax           = 64 << 10
	copyAddMaxCandidates      = 4
)

type copyAddStats struct {
	copiedBytes  uint64
	expandedSize uint64
}

type contentChunk struct {
	offset uint64
	data   []byte
}

type indexedChunk struct {
	offset uint64
	length uint32
}

type copyAddStreamWriter struct {
	writer       *bufio.Writer
	expandedSize uint64
	pendingAdd   []byte
	copyOffset   uint64
	copyLength   uint64
}

func createCopyAddStream(ctx context.Context, sourcePath, targetPath, outputPath string) (copyAddStats, error) {
	info, err := os.Stat(targetPath)
	if err != nil {
		return copyAddStats{}, fmt.Errorf("inspect copy-add target: %w", err)
	}
	if info.Size() < 0 {
		return copyAddStats{}, fmt.Errorf("copy-add target has an invalid size")
	}
	stats, _, err := createCopyAddStreamOptimized(ctx, sourcePath, targetPath, outputPath, uint64(info.Size()))
	return stats, err
}

func forEachContentChunk(ctx context.Context, file *os.File, callback func(contentChunk) error) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	readBuffer := make([]byte, sparseIOBufferSize)
	chunk := make([]byte, 0, copyAddChunkMax)
	var offset uint64
	var gearHash uint64
	emit := func() error {
		if len(chunk) == 0 {
			return nil
		}
		if err := callback(contentChunk{offset: offset, data: chunk}); err != nil {
			return err
		}
		offset += uint64(len(chunk))
		chunk = chunk[:0]
		gearHash = 0
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readError := file.Read(readBuffer)
		for _, value := range readBuffer[:count] {
			chunk = append(chunk, value)
			gearHash = (gearHash << 1) + copyAddGear(value)
			if len(chunk) < copyAddChunkMin {
				continue
			}
			if len(chunk) < copyAddChunkMax && gearHash&(copyAddChunkAvg-1) != 0 {
				continue
			}
			if err := emit(); err != nil {
				return err
			}
		}
		if readError == io.EOF {
			return emit()
		}
		if readError != nil {
			return readError
		}
	}
}

func copyAddGear(value byte) uint64 {
	x := uint64(value) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (stream *copyAddStreamWriter) add(data []byte) error {
	if err := stream.flushCopy(); err != nil {
		return err
	}
	stream.pendingAdd = append(stream.pendingAdd, data...)
	if len(stream.pendingAdd) >= sparseIOBufferSize {
		return stream.flushAdd()
	}
	return nil
}

func (stream *copyAddStreamWriter) copy(offset, length uint64) error {
	if err := stream.flushAdd(); err != nil {
		return err
	}
	if stream.copyLength > 0 && stream.copyOffset+stream.copyLength == offset {
		stream.copyLength += length
		return nil
	}
	if err := stream.flushCopy(); err != nil {
		return err
	}
	stream.copyOffset = offset
	stream.copyLength = length
	return nil
}

func (stream *copyAddStreamWriter) flushAdd() error {
	if len(stream.pendingAdd) == 0 {
		return nil
	}
	if err := stream.writer.WriteByte(copyAddOpcodeAdd); err != nil {
		return err
	}
	lengthBytes, err := writeUvarint(stream.writer, uint64(len(stream.pendingAdd)))
	if err != nil {
		return err
	}
	if _, err := stream.writer.Write(stream.pendingAdd); err != nil {
		return err
	}
	stream.expandedSize += 1 + uint64(lengthBytes+len(stream.pendingAdd))
	stream.pendingAdd = stream.pendingAdd[:0]
	return nil
}

func (stream *copyAddStreamWriter) flushCopy() error {
	if stream.copyLength == 0 {
		return nil
	}
	if err := stream.writer.WriteByte(copyAddOpcodeCopy); err != nil {
		return err
	}
	offsetBytes, err := writeUvarint(stream.writer, stream.copyOffset)
	if err != nil {
		return err
	}
	lengthBytes, err := writeUvarint(stream.writer, stream.copyLength)
	if err != nil {
		return err
	}
	stream.expandedSize += 1 + uint64(offsetBytes+lengthBytes)
	stream.copyOffset = 0
	stream.copyLength = 0
	return nil
}

func (stream *copyAddStreamWriter) close() error {
	if err := stream.flushAdd(); err != nil {
		return err
	}
	if err := stream.flushCopy(); err != nil {
		return err
	}
	if err := stream.writer.WriteByte(copyAddOpcodeEnd); err != nil {
		return err
	}
	stream.expandedSize++
	return stream.writer.Flush()
}

func writeUvarint(writer io.Writer, value uint64) (int, error) {
	var encoded [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(encoded[:], value)
	_, err := writer.Write(encoded[:count])
	return count, err
}

func applyCopyAddStream(source, operations, output *os.File, expectedSourceSize, expectedTargetSize uint64, expectedSourceHash, expectedTargetHash string, callback progress.Callback, event progress.Event) error {
	if source == nil || operations == nil || output == nil {
		return fmt.Errorf("copy-add application requires source, operations, and output files")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest, size, err := hashutil.Reader(source)
	if err != nil {
		return fmt.Errorf("hash copy-add source: %w", err)
	}
	if digest != expectedSourceHash || size != expectedSourceSize {
		return fmt.Errorf("installed source failed SHA-256 verification")
	}
	if _, err := operations.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return applyCopyAddStreamContext(context.Background(), source, operations, output, expectedSourceSize, expectedTargetSize, expectedTargetHash, callback, event)
}
