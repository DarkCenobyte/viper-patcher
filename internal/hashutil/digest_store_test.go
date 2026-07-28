package hashutil

import (
	"bytes"
	"math"
	"os"
	"testing"
)

func TestChunkCountDoesNotOverflow(t *testing.T) {
	want := uint64(1 + (math.MaxUint64-1)/ChunkSize)
	if got := ChunkCount(math.MaxUint64); got != want {
		t.Fatalf("ChunkCount(MaxUint64) = %d, want %d", got, want)
	}
}

func TestChunkDigestStoreSpillsWithoutChangingRoot(t *testing.T) {
	size := ChunkSize + 1
	digests := [][32]byte{
		ChunkDigestBytes([]byte("first")),
		ChunkDigestBytes([]byte("second")),
	}
	store, err := newChunkDigestStore(uint64(len(digests)), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spillPath := store.path
	if spillPath == "" {
		t.Fatal("expected a spill file")
	}
	if err := store.Set(1, digests[1]); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(0, digests[0]); err != nil {
		t.Fatal(err)
	}
	got, err := store.Root(size)
	if err != nil {
		t.Fatal(err)
	}
	want := RootFromChunkDigestBytes(size, digests)
	if got != want {
		t.Fatalf("spilled root = %s, want %s", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spillPath); !os.IsNotExist(err) {
		t.Fatalf("spill file still exists after close: %v", err)
	}
}

func TestChunkDigestStoreKeepsCommonPathInMemory(t *testing.T) {
	store, err := NewChunkDigestStore(2)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.memory == nil || store.file != nil {
		t.Fatal("small digest table did not use the direct in-memory path")
	}
}

func TestSizedAccumulatorSpillsWithoutChangingIdentity(t *testing.T) {
	data := bytes.Repeat([]byte("viper"), 4096)
	accumulator, err := newSizedAccumulator(uint64(len(data)), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer accumulator.Close()
	if _, err := accumulator.Write(data); err != nil {
		t.Fatal(err)
	}
	got, err := accumulator.SumHex()
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := Reader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("sized accumulator root = %s, want %s", got, want)
	}
	if err := accumulator.Close(); err != nil {
		t.Fatal(err)
	}
}
