package hashutil

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestV4HashWrappers(t *testing.T) {
	data := make([]byte, ChunkSize+123)
	for index := range data {
		data[index] = byte(index*31 + index/251)
	}
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	root, size, err := File(context.Background(), file, uint64(len(data)))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if size != uint64(len(data)) || len(root) != 64 {
		t.Fatalf("root=%q size=%d", root, size)
	}

	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rootWithChunks, chunks, err := FileWithChunks(context.Background(), file, uint64(len(data)))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if rootWithChunks != root || len(chunks) != 2 {
		t.Fatalf("root=%q chunks=%d, want root=%q chunks=2", rootWithChunks, len(chunks), root)
	}
	assembled, err := Root(uint64(len(data)), chunks)
	if err != nil {
		t.Fatal(err)
	}
	if assembled != root {
		t.Fatalf("assembled root = %q, want %q", assembled, root)
	}

	first, err := Bytes(data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bytes(append([]byte(nil), data...))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first[:], second[:]) {
		t.Fatal("byte hash is not deterministic")
	}
	if empty, err := Bytes(nil); err != nil || empty == first {
		t.Fatalf("empty hash = %x, err=%v", empty, err)
	}
}

func TestV4HashWrapperErrors(t *testing.T) {
	if _, err := Root(1, nil); err == nil {
		t.Fatal("Root accepted a missing digest table")
	}

	path := filepath.Join(t.TempDir(), "closed.bin")
	if err := os.WriteFile(path, []byte("closed"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := File(context.Background(), file, 6); err == nil {
		t.Fatal("File accepted a closed file")
	}
	if _, _, err := FileWithChunks(context.Background(), file, 6); err == nil {
		t.Fatal("FileWithChunks accepted a closed file")
	}

}
