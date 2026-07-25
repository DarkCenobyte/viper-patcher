package hashutil

import (
	"os"
	"path/filepath"
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

func TestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, size, err := File(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 || digest != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("digest = %q, size = %d", digest, size)
	}
}

func TestFileMissing(t *testing.T) {
	if _, _, err := File("does-not-exist"); err == nil {
		t.Fatal("expected an error")
	}
}
