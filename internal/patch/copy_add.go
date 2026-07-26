package patch

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	source, err := os.Open(sourcePath)
	if err != nil {
		return copyAddStats{}, fmt.Errorf("open copy-add source: %w", err)
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
		return copyAddStats{}, fmt.Errorf("index copy-add source: %w", err)
	}

	target, err := os.Open(targetPath)
	if err != nil {
		return copyAddStats{}, fmt.Errorf("open copy-add target: %w", err)
	}
	defer target.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return copyAddStats{}, fmt.Errorf("create copy-add stream: %w", err)
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
		return copyAddStats{}, err
	}
	stream.expandedSize = uint64(len(copyAddMagic))
	compareBuffer := make([]byte, copyAddChunkMax)
	var stats copyAddStats
	if err := forEachContentChunk(ctx, target, func(chunk contentChunk) error {
		digest := sha256.Sum256(chunk.data)
		for _, candidate := range index[digest] {
			if int(candidate.length) != len(chunk.data) {
				continue
			}
			if _, err := source.ReadAt(compareBuffer[:len(chunk.data)], int64(candidate.offset)); err != nil {
				return fmt.Errorf("verify copy-add source chunk: %w", err)
			}
			if !equalBytes(compareBuffer[:len(chunk.data)], chunk.data) {
				continue
			}
			if err := stream.copy(candidate.offset, uint64(len(chunk.data))); err != nil {
				return err
			}
			stats.copiedBytes += uint64(len(chunk.data))
			return nil
		}
		return stream.add(chunk.data)
	}); err != nil {
		return copyAddStats{}, fmt.Errorf("build copy-add stream: %w", err)
	}
	if err := stream.close(); err != nil {
		return copyAddStats{}, err
	}
	if err := output.Close(); err != nil {
		return copyAddStats{}, err
	}
	committed = true
	stats.expandedSize = stream.expandedSize
	return stats, nil
}

func forEachContentChunk(ctx context.Context, file *os.File, callback func(contentChunk) error) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, sparseIOBufferSize)
	chunk := make([]byte, 0, copyAddChunkMax)
	var offset uint64
	var gearHash uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := reader.ReadByte()
		if err == io.EOF {
			if len(chunk) > 0 {
				if err := callback(contentChunk{offset: offset, data: chunk}); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		chunk = append(chunk, value)
		gearHash = (gearHash << 1) + copyAddGear(value)
		if len(chunk) < copyAddChunkMin {
			continue
		}
		if len(chunk) < copyAddChunkMax && gearHash&(copyAddChunkAvg-1) != 0 {
			continue
		}
		if err := callback(contentChunk{offset: offset, data: chunk}); err != nil {
			return err
		}
		offset += uint64(len(chunk))
		chunk = make([]byte, 0, copyAddChunkMax)
		gearHash = 0
	}
}

func copyAddGear(value byte) uint64 {
	x := uint64(value) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
		if _, err := outputHash.Write(block); err != nil {
			return err
		}
		produced += uint64(len(block))
		report(false)
		return nil
	}

	for {
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
