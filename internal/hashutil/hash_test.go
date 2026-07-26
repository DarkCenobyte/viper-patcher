package hashutil

import (
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
	const expected = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if digest != expected {
		t.Fatalf("digest = %s, want %s", digest, expected)
	}
}
