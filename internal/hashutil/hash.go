package hashutil

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"sync"

	"lukechampine.com/blake3"
)

// ChunkSize is part of the blake3-tree-v1 file identity definition.
const ChunkSize uint64 = 8 << 20

var treeDomain = []byte("VIPR-BLAKE3-TREE-V1\x00")

// Accumulator computes the VIPR BLAKE3 tree identity while accepting a normal
// streaming io.Writer interface. It hashes each file chunk incrementally and
// retains only the completed 32-byte chunk digests.
type Accumulator struct {
	chunk      hash.Hash
	chunkBytes uint64
	chunks     [][32]byte
	size       uint64
	finalized  bool
	sum        string
	sumError   error
}

func NewAccumulator() *Accumulator {
	return &Accumulator{chunk: NewChunkHasher()}
}

func (accumulator *Accumulator) Write(data []byte) (int, error) {
	if accumulator == nil {
		return 0, fmt.Errorf("hash accumulator is unavailable")
	}
	if accumulator.finalized {
		return 0, fmt.Errorf("hash accumulator is finalized")
	}
	originalLength := len(data)
	for len(data) > 0 {
		remaining := ChunkSize - accumulator.chunkBytes
		part := uint64(len(data))
		if part > remaining {
			part = remaining
		}
		written, err := accumulator.chunk.Write(data[:int(part)])
		if err != nil {
			return originalLength - len(data) + written, err
		}
		if written != int(part) {
			return originalLength - len(data) + written, io.ErrShortWrite
		}
		accumulator.chunkBytes += part
		accumulator.size += part
		data = data[int(part):]
		if accumulator.chunkBytes == ChunkSize {
			if err := accumulator.flushChunk(); err != nil {
				return originalLength - len(data), err
			}
		}
	}
	return originalLength, nil
}

func (accumulator *Accumulator) flushChunk() error {
	if accumulator.chunkBytes == 0 {
		return nil
	}
	sum := accumulator.chunk.Sum(nil)
	if len(sum) != 32 {
		return fmt.Errorf("unexpected BLAKE3 chunk digest size %d", len(sum))
	}
	var digest [32]byte
	copy(digest[:], sum)
	accumulator.chunks = append(accumulator.chunks, digest)
	accumulator.chunk.Reset()
	accumulator.chunkBytes = 0
	return nil
}

func (accumulator *Accumulator) SumHex() (string, error) {
	sum, _, err := accumulator.SumHexAndChunks()
	return sum, err
}

// SumHexAndChunks finalizes the tree identity and returns an independent copy
// of the fixed-size chunk digests used to build it.
func (accumulator *Accumulator) SumHexAndChunks() (string, [][32]byte, error) {
	if accumulator == nil {
		return "", nil, fmt.Errorf("hash accumulator is unavailable")
	}
	if !accumulator.finalized {
		accumulator.finalized = true
		if err := accumulator.flushChunk(); err != nil {
			accumulator.sumError = err
			return "", nil, err
		}
		accumulator.sum = RootFromChunkDigestBytes(accumulator.size, accumulator.chunks)
	}
	chunks := append([][32]byte(nil), accumulator.chunks...)
	return accumulator.sum, chunks, accumulator.sumError
}

func NewChunkHasher() hash.Hash {
	return blake3.New(32, nil)
}

func NewFingerprintHasher() hash.Hash {
	return blake3.New(32, nil)
}

func HashSumHex(hasher hash.Hash) string {
	return hex.EncodeToString(hasher.Sum(nil))
}

func ChunkDigestBytes(data []byte) [32]byte {
	return blake3.Sum256(data)
}

func ChunkDigest(data []byte) string {
	digest := ChunkDigestBytes(data)
	return hex.EncodeToString(digest[:])
}

// RootFromChunkDigestBytes creates the domain-separated 256-bit identity stored
// in VIPR v3 headers. Chunk order, total file size, and the fixed chunk size are
// committed into the root.
func RootFromChunkDigestBytes(size uint64, chunks [][32]byte) string {
	hasher := blake3.New(32, nil)
	_, _ = hasher.Write(treeDomain)
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], size)
	_, _ = hasher.Write(encoded[:])
	binary.LittleEndian.PutUint64(encoded[:], ChunkSize)
	_, _ = hasher.Write(encoded[:])
	binary.LittleEndian.PutUint64(encoded[:], uint64(len(chunks)))
	_, _ = hasher.Write(encoded[:])
	for _, digest := range chunks {
		_, _ = hasher.Write(digest[:])
	}
	return HashSumHex(hasher)
}

// RootFromChunkDigests validates hexadecimal chunk digests before creating the
// same root as RootFromChunkDigestBytes.
func RootFromChunkDigests(size uint64, chunks []string) (string, error) {
	decoded := make([][32]byte, len(chunks))
	for index, value := range chunks {
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 {
			return "", fmt.Errorf("invalid BLAKE3 chunk digest %q", value)
		}
		copy(decoded[index][:], digest)
	}
	return RootFromChunkDigestBytes(size, decoded), nil
}

// Reader returns the VIPR BLAKE3 tree digest and byte count read from reader.
func Reader(reader io.Reader) (string, uint64, error) {
	accumulator := NewAccumulator()
	size, err := io.Copy(accumulator, reader)
	if err != nil {
		return "", 0, err
	}
	digest, err := accumulator.SumHex()
	return digest, uint64(size), err
}

// FileParallel calculates a VIPR file identity using independent fixed-size reads.
func FileParallel(ctx context.Context, file *os.File, size uint64, workers int, onProgress func(uint64)) (digest string, readSize uint64, resultError error) {
	if file == nil {
		return "", 0, fmt.Errorf("hash input file is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if size > math.MaxInt64 {
		return "", 0, fmt.Errorf("hash input exceeds the signed 64-bit file range")
	}
	chunkCount := ChunkCount(size)
	if chunkCount == 0 {
		return RootFromChunkDigestBytes(0, nil), 0, nil
	}
	if workers < 1 {
		workers = 1
	}
	if uint64(workers) > chunkCount {
		workers = int(chunkCount)
	}

	digests, err := NewChunkDigestStore(chunkCount)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		resultError = errors.Join(resultError, digests.Close())
	}()

	jobs := make(chan uint64)
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var group sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	var progressMutex sync.Mutex
	var processed uint64

	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			buffer := make([]byte, int(ChunkSize))
			for index := range jobs {
				if err := workerContext.Err(); err != nil {
					return
				}
				offset := index * ChunkSize
				length := ChunkSize
				if size-offset < length {
					length = size - offset
				}
				chunk := buffer[:int(length)]
				if _, err := io.ReadFull(io.NewSectionReader(file, int64(offset), int64(length)), chunk); err != nil {
					errorOnce.Do(func() {
						firstError = err
						cancel()
					})
					return
				}
				if err := digests.Set(index, ChunkDigestBytes(chunk)); err != nil {
					errorOnce.Do(func() {
						firstError = err
						cancel()
					})
					return
				}
				if onProgress != nil {
					progressMutex.Lock()
					processed += length
					onProgress(processed)
					progressMutex.Unlock()
				}
			}
		}()
	}

sendLoop:
	for index := uint64(0); index < chunkCount; index++ {
		select {
		case jobs <- index:
		case <-workerContext.Done():
			break sendLoop
		}
	}
	close(jobs)
	group.Wait()
	if firstError != nil {
		return "", 0, firstError
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	root, err := digests.Root(size)
	if err != nil {
		return "", 0, err
	}
	return root, size, nil
}
