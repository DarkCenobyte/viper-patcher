//go:build ignore

package hashutil

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestReader(t *testing.T) {
	digest, size, err := Reader(strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 {
		t.Fatalf("size = %d, want 3", size)
	}
	expected, err := RootFromChunkDigests(3, []string{ChunkDigest([]byte("abc"))})
	if err != nil {
		t.Fatal(err)
	}
	if digest != expected {
		t.Fatalf("digest = %s, want %s", digest, expected)
	}
}

func TestChunkDigestBytesMatchesEncodedDigest(t *testing.T) {
	data := []byte("copy-add-index")
	digest := ChunkDigestBytes(data)
	actual := hex.EncodeToString(digest[:])
	expected := ChunkDigest(data)
	if actual != expected {
		t.Fatalf("digest = %s, want %s", actual, expected)
	}
}
