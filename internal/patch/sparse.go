//go:build ignore

package patch

import (
	"encoding/binary"
	"io"
)

var sparseMagic = [8]byte{'V', 'S', 'P', 'R', '\r', '\n', 0x1a, 0x01}

const sparseIOBufferSize = 1 << 20

type sparseStats struct {
	changedBytes uint64
	expandedSize uint64
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
