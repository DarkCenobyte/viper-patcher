package hashutil

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBLAKE3TreeStreamingAndParallelAgree(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), int((ChunkSize*2+12352)/16))
	streamed, size, err := Reader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	parallel, parallelSize, err := FileParallel(context.Background(), file, uint64(len(data)), 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if size != parallelSize || streamed != parallel {
		t.Fatalf("streamed %s/%d parallel %s/%d", streamed, size, parallel, parallelSize)
	}
}

func TestBLAKE3TreeCommitsOrderAndSize(t *testing.T) {
	first := ChunkDigest([]byte("first"))
	second := ChunkDigest([]byte("second"))
	left, err := RootFromChunkDigests(11, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	right, err := RootFromChunkDigests(11, []string{second, first})
	if err != nil {
		t.Fatal(err)
	}
	otherSize, err := RootFromChunkDigests(12, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if left == right || left == otherSize {
		t.Fatal("tree root must commit chunk order and total size")
	}
}

func TestRootRejectsMalformedChunkDigest(t *testing.T) {
	if _, err := RootFromChunkDigests(1, []string{strings.Repeat("z", 64)}); err == nil {
		t.Fatal("expected malformed digest to be rejected")
	}
}

func TestAccumulatorAndParallelErrors(t *testing.T) {
	var unavailable *Accumulator
	if _, err := unavailable.Write([]byte("x")); err == nil {
		t.Fatal("expected nil accumulator write to fail")
	}
	if _, err := unavailable.SumHex(); err == nil {
		t.Fatal("expected nil accumulator sum to fail")
	}
	if _, _, err := FileParallel(context.Background(), nil, 0, 1, nil); err == nil {
		t.Fatal("expected nil file to fail")
	}
}

func TestBLAKE3TreeEmptyFile(t *testing.T) {
	digest, size, err := Reader(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := RootFromChunkDigests(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 || digest != expected {
		t.Fatalf("digest=%s size=%d expected=%s", digest, size, expected)
	}
}

func TestAccumulatorFinalizationIsStable(t *testing.T) {
	accumulator := NewAccumulator()
	if _, err := accumulator.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	first, err := accumulator.SumHex()
	if err != nil {
		t.Fatal(err)
	}
	second, err := accumulator.SumHex()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("repeated sum changed: %s != %s", first, second)
	}
	if _, err := accumulator.Write([]byte("later")); err == nil {
		t.Fatal("write after finalization must fail")
	}
}
