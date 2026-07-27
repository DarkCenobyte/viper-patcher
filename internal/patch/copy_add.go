package patch

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"os"
)

var copyAddMagic = [8]byte{'V', 'C', 'A', 'D', '\r', '\n', 0x1a, 0x01}

const (
	copyAddOpcodeEnd  byte = 0
	copyAddOpcodeCopy byte = 1
	copyAddOpcodeAdd  byte = 2

	copyAddChunkDefaultMin = 4 << 10
	copyAddChunkDefaultAvg = 16 << 10
	copyAddChunkDefaultMax = 64 << 10
	copyAddChunkLargestAvg = 4 << 20
	copyAddChunkLargestMax = 16 << 20

	copyAddMaxCandidates            = 4
	copyAddIndexMemoryBudget uint64 = 64 << 20
)

var copyAddGearTable = func() [256]uint64 {
	var table [256]uint64
	for value := range table {
		table[value] = mixCopyAddGear(byte(value))
	}
	return table
}()

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

type indexedChunkCandidates struct {
	count  uint8
	chunks [copyAddMaxCandidates]indexedChunk
}

func (candidates *indexedChunkCandidates) add(chunk indexedChunk) {
	if candidates.count >= copyAddMaxCandidates {
		return
	}
	candidates.chunks[candidates.count] = chunk
	candidates.count++
}

type copyAddChunkProfile struct {
	minimum         int
	average         int
	maximum         int
	maxIndexEntries int
	indexable       bool
}

// copyAddProfileForSize keeps the compact source index within an exact backing
// array budget. Small and medium files retain the original 4/16/64 KiB chunking,
// while very large files progressively use coarser content-defined chunks.
func copyAddProfileForSize(size uint64) copyAddChunkProfile {
	maxEntries := int(copyAddIndexMemoryBudget / copyAddIndexEntrySize)
	average := copyAddChunkDefaultAvg
	indexable := maxEntries > 1
	if indexable && size > 0 {
		// Content-defined chunks may be as small as average/4. Reserve one
		// entry for the final short chunk and choose the average from that
		// worst-case count instead of an estimated map-entry cost.
		entryBudget := uint64(maxEntries - 1)
		requiredMinimum := size / entryBudget
		if size%entryBudget != 0 {
			requiredMinimum++
		}
		requiredAverage := requiredMinimum * 4
		if requiredMinimum > copyAddChunkLargestAvg/4 || requiredAverage > copyAddChunkLargestAvg {
			indexable = false
		}
		for uint64(average) < requiredAverage && average < copyAddChunkLargestAvg {
			average <<= 1
		}
	}
	if average > copyAddChunkLargestAvg {
		average = copyAddChunkLargestAvg
	}
	minimum := average / 4
	if minimum < copyAddChunkDefaultMin {
		minimum = copyAddChunkDefaultMin
	}
	maximum := average * 4
	if maximum < copyAddChunkDefaultMax {
		maximum = copyAddChunkDefaultMax
	}
	if maximum > copyAddChunkLargestMax {
		maximum = copyAddChunkLargestMax
	}
	return copyAddChunkProfile{
		minimum:         minimum,
		average:         average,
		maximum:         maximum,
		maxIndexEntries: maxEntries,
		indexable:       indexable,
	}
}

func copyAddIndexCapacity(size uint64, profile copyAddChunkProfile) int {
	if !profile.indexable || profile.minimum <= 0 || profile.maxIndexEntries <= 0 || size == 0 {
		return 0
	}
	count := size/uint64(profile.minimum) + 1
	if count > uint64(profile.maxIndexEntries) {
		count = uint64(profile.maxIndexEntries)
	}
	return int(count)
}

type copyAddStreamWriter struct {
	writer       *bufio.Writer
	expandedSize uint64
	pendingAdd   []byte
	copyOffset   uint64
	copyLength   uint64
}

func forEachContentChunk(ctx context.Context, file *os.File, profile copyAddChunkProfile, callback func(contentChunk) error) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	readBuffer := make([]byte, sparseIOBufferSize)
	chunk := make([]byte, 0, profile.maximum)
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
			gearHash = (gearHash << 1) + copyAddGearTable[value]
			if len(chunk) < profile.minimum {
				continue
			}
			if len(chunk) < profile.maximum && gearHash&uint64(profile.average-1) != 0 {
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

func mixCopyAddGear(value byte) uint64 {
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
