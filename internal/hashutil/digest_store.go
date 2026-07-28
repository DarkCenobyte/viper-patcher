package hashutil

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"

	"lukechampine.com/blake3"
)

const (
	chunkDigestSize             uint64 = 32
	inMemoryChunkDigestBudget64 uint64 = 64 << 20
	inMemoryChunkDigestBudget32 uint64 = 16 << 20
	chunkDigestReadBatchDigests uint64 = 4096
)

// ChunkCount returns the number of fixed-size BLAKE3 tree chunks required for
// size without using an overflow-prone round-up expression.
func ChunkCount(size uint64) uint64 {
	if size == 0 {
		return 0
	}
	return 1 + (size-1)/ChunkSize
}

// ChunkDigestStore keeps the common path as one direct in-memory array. When a
// file is so large that the digest array alone would exceed the in-memory
// budget, it transparently spills the 32-byte digests to a private temporary
// file instead of rejecting the logical file size or risking an unbounded heap
// allocation. Set is safe for concurrent calls targeting distinct indexes.
type ChunkDigestStore struct {
	count  uint64
	memory [][32]byte
	file   *os.File
	path   string
}

func NewChunkDigestStore(count uint64) (*ChunkDigestStore, error) {
	return newChunkDigestStore(count, chunkDigestMemoryBudget())
}

func newChunkDigestStore(count, memoryBudget uint64) (*ChunkDigestStore, error) {
	store := &ChunkDigestStore{count: count}
	if count == 0 {
		store.memory = make([][32]byte, 0)
		return store, nil
	}
	if count <= uint64(math.MaxInt) && count <= memoryBudget/chunkDigestSize {
		store.memory = make([][32]byte, int(count))
		return store, nil
	}
	if count > uint64(math.MaxInt64)/chunkDigestSize {
		return nil, fmt.Errorf("BLAKE3 chunk digest storage exceeds the signed 64-bit file range")
	}
	file, err := os.CreateTemp("", ".viper-patcher-digests-*")
	if err != nil {
		return nil, fmt.Errorf("create BLAKE3 digest spill file: %w", err)
	}
	store.file = file
	store.path = file.Name()
	return store, nil
}

func (store *ChunkDigestStore) Set(index uint64, digest [32]byte) error {
	if store == nil {
		return fmt.Errorf("BLAKE3 chunk digest storage is unavailable")
	}
	if index >= store.count {
		return fmt.Errorf("BLAKE3 chunk digest index %d is outside count %d", index, store.count)
	}
	if store.memory != nil {
		store.memory[int(index)] = digest
		return nil
	}
	offset := int64(index * chunkDigestSize)
	written, err := store.file.WriteAt(digest[:], offset)
	if err != nil {
		return fmt.Errorf("write BLAKE3 digest spill file: %w", err)
	}
	if written != len(digest) {
		return io.ErrShortWrite
	}
	return nil
}

func (store *ChunkDigestStore) Root(size uint64) (string, error) {
	if store == nil {
		return "", fmt.Errorf("BLAKE3 chunk digest storage is unavailable")
	}
	if expected := ChunkCount(size); expected != store.count {
		return "", fmt.Errorf("BLAKE3 chunk digest count is %d, expected %d", store.count, expected)
	}
	if store.memory != nil {
		return RootFromChunkDigestBytes(size, store.memory), nil
	}
	return rootFromChunkDigestReader(size, store.count, io.NewSectionReader(store.file, 0, int64(store.count*chunkDigestSize)))
}

func (store *ChunkDigestStore) Close() error {
	if store == nil {
		return nil
	}
	var closeError error
	if store.file != nil {
		closeError = store.file.Close()
		store.file = nil
	}
	var removeError error
	if store.path != "" {
		removeError = os.Remove(store.path)
		if os.IsNotExist(removeError) {
			removeError = nil
		}
		store.path = ""
	}
	return errors.Join(closeError, removeError)
}

func rootFromChunkDigestReader(size, count uint64, reader io.Reader) (string, error) {
	hasher := blake3.New(32, nil)
	_, _ = hasher.Write(treeDomain)
	var encoded [8]byte
	writeUint64 := func(value uint64) {
		binary.LittleEndian.PutUint64(encoded[:], value)
		_, _ = hasher.Write(encoded[:])
	}
	writeUint64(size)
	writeUint64(ChunkSize)
	writeUint64(count)

	batchCount := chunkDigestReadBatchDigests
	if count < batchCount {
		batchCount = count
	}
	buffer := make([]byte, int(batchCount*chunkDigestSize))
	remaining := count
	for remaining > 0 {
		current := remaining
		if current > chunkDigestReadBatchDigests {
			current = chunkDigestReadBatchDigests
		}
		block := buffer[:int(current*chunkDigestSize)]
		if _, err := io.ReadFull(reader, block); err != nil {
			return "", fmt.Errorf("read BLAKE3 digest spill file: %w", err)
		}
		_, _ = hasher.Write(block)
		remaining -= current
	}
	return HashSumHex(hasher), nil
}

func chunkDigestMemoryBudget() uint64 {
	if bits.UintSize == 32 {
		return inMemoryChunkDigestBudget32
	}
	return inMemoryChunkDigestBudget64
}
