package hashutil

import "testing"

func TestAccumulatorReturnsIndependentChunkDigests(t *testing.T) {
	accumulator := NewAccumulator()
	if _, err := accumulator.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	first, chunks, err := accumulator.SumHexAndChunks()
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0] != ChunkDigestBytes([]byte("abc")) {
		t.Fatalf("unexpected chunk digests: %#v", chunks)
	}
	chunks[0] = [32]byte{}
	second, secondChunks, err := accumulator.SumHexAndChunks()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(secondChunks) != 1 || secondChunks[0] != ChunkDigestBytes([]byte("abc")) {
		t.Fatalf("repeated finalization changed: %s/%#v", second, secondChunks)
	}
}
