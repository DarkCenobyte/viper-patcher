package zstd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedSegmentPreservesPatchCursor(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input.bin")
	patchPath := filepath.Join(directory, "input.zst")
	input := bytes.Repeat([]byte("prepared-segment-"), 4096)
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CompressFile(inputPath, patchPath, 1, nil); err != nil {
		t.Fatal(err)
	}
	patchInfo, err := os.Stat(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := os.Open(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	defer patch.Close()
	if _, err := patch.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	segment, err := PrepareSegment(patch, 0, uint64(patchInfo.Size()))
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	windowSize, err := segment.WindowSize()
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if err := decoder.SetWindowLimit(windowSize); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := decoder.DecompressPreparedSegmentToWriter(context.Background(), segment, &output, uint64(len(input)), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatal("prepared segment output does not match input")
	}
	position, err := patch.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if position != 1 {
		t.Fatalf("patch cursor moved to %d", position)
	}
}
