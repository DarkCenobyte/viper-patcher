//go:build ignore

package hashutil

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkDigestStoreValidationPaths(t *testing.T) {
	if got := ChunkCount(0); got != 0 {
		t.Fatalf("ChunkCount(0) = %d", got)
	}
	if got := ChunkCount(1); got != 1 {
		t.Fatalf("ChunkCount(1) = %d", got)
	}

	var unavailable *ChunkDigestStore
	if err := unavailable.Set(0, [32]byte{}); err == nil {
		t.Fatal("nil digest store accepted Set")
	}
	if _, err := unavailable.Root(0); err == nil {
		t.Fatal("nil digest store accepted Root")
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}

	empty, err := newChunkDigestStore(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if empty.memory == nil || len(empty.memory) != 0 {
		t.Fatal("empty digest store did not use an empty in-memory slice")
	}
	if err := empty.Set(0, [32]byte{}); err == nil {
		t.Fatal("empty digest store accepted an out-of-range index")
	}
	if _, err := empty.Root(1); err == nil {
		t.Fatal("digest store accepted a mismatched file size")
	}
	root, err := empty.Root(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := RootFromChunkDigestBytes(0, nil); root != want {
		t.Fatalf("empty root = %s, want %s", root, want)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := newChunkDigestStore(1, chunkDigestSize)
	if err != nil {
		t.Fatal(err)
	}
	digest := ChunkDigestBytes([]byte("digest"))
	if err := store.Set(0, digest); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(1, digest); err == nil {
		t.Fatal("digest store accepted an out-of-range index")
	}
	root, err = store.Root(1)
	if err != nil {
		t.Fatal(err)
	}
	if want := RootFromChunkDigestBytes(1, [][32]byte{digest}); root != want {
		t.Fatalf("in-memory root = %s, want %s", root, want)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	tooLarge := uint64(math.MaxInt64)/chunkDigestSize + 1
	if _, err := newChunkDigestStore(tooLarge, 0); err == nil {
		t.Fatal("oversized digest spill was accepted")
	}
	if _, err := rootFromChunkDigestReader(ChunkSize, 1, bytes.NewReader(nil)); err == nil {
		t.Fatal("truncated digest reader was accepted")
	}
}

func TestSizedAccumulatorValidationPaths(t *testing.T) {
	var unavailable *SizedAccumulator
	if _, err := unavailable.Write(nil); err == nil {
		t.Fatal("nil sized accumulator accepted Write")
	}
	if _, err := unavailable.SumHex(); err == nil {
		t.Fatal("nil sized accumulator accepted SumHex")
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}

	empty, err := NewSizedAccumulator(0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := empty.SumHex()
	if err != nil {
		t.Fatal(err)
	}
	if want := RootFromChunkDigestBytes(0, nil); root != want {
		t.Fatalf("empty sized root = %s, want %s", root, want)
	}
	if second, err := empty.SumHex(); err != nil || second != root {
		t.Fatalf("second empty sum = %q, %v", second, err)
	}
	if _, err := empty.Write([]byte{1}); err == nil {
		t.Fatal("finalized sized accumulator accepted Write")
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}

	accumulator, err := NewSizedAccumulator(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accumulator.Write([]byte("four")); err == nil {
		t.Fatal("sized accumulator accepted output beyond its declared size")
	}
	if written, err := accumulator.Write([]byte("ab")); err != nil || written != 2 {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if _, err := accumulator.SumHex(); err == nil {
		t.Fatal("undersized accumulated output was accepted")
	}
	if _, err := accumulator.Write([]byte("c")); err == nil {
		t.Fatal("failed finalized accumulator accepted Write")
	}
	if _, err := accumulator.SumHex(); err == nil {
		t.Fatal("failed finalized accumulator lost its error")
	}
	if err := accumulator.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := accumulator.SumHex(); err == nil {
		t.Fatal("closed sized accumulator accepted SumHex")
	}

	fingerprint := NewFingerprintHasher()
	_, _ = fingerprint.Write([]byte("fingerprint"))
	if len(fingerprint.Sum(nil)) != 32 {
		t.Fatal("fingerprint hasher returned an unexpected digest size")
	}
}

func TestFileParallelValidationPaths(t *testing.T) {
	if _, _, err := FileParallel(context.Background(), nil, 0, 1, nil); err == nil {
		t.Fatal("nil file was accepted")
	}

	path := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, _, err := FileParallel(context.Background(), file, uint64(math.MaxInt64)+1, 1, nil); err == nil {
		t.Fatal("signed-range overflow was accepted")
	}
	emptyRoot, size, err := FileParallel(context.TODO(), file, 0, 0, nil)
	if err != nil || size != 0 || emptyRoot != RootFromChunkDigestBytes(0, nil) {
		t.Fatalf("empty FileParallel = %q, %d, %v", emptyRoot, size, err)
	}
	if _, _, err := FileParallel(context.Background(), file, 2, 4, nil); err == nil {
		t.Fatal("truncated file was accepted")
	}
}
