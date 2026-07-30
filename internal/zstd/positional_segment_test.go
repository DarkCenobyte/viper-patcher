//go:build vipr_legacy_zstd

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

func TestPreparedInputReusesHandleAcrossSegments(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input.bin")
	patchPath := filepath.Join(directory, "input.zst")
	input := bytes.Repeat([]byte("reusable-prepared-input-"), 4096)
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

	prepared, err := PrepareInput(patch)
	if err != nil {
		t.Fatal(err)
	}
	first, err := prepared.Segment(0, uint64(patchInfo.Size()))
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepared.Segment(0, uint64(patchInfo.Size()))
	if err != nil {
		t.Fatal(err)
	}
	if first.input != prepared || second.input != prepared || first.input.handle != second.input.handle {
		t.Fatal("prepared segments did not reuse their positional input")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	windowSize, err := second.WindowSize()
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
	if err := decoder.DecompressPreparedSegmentToWriter(context.Background(), second, &output, uint64(len(input)), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatal("reused prepared input output does not match input")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Segment(0, uint64(patchInfo.Size())); err == nil {
		t.Fatal("closed prepared input accepted a new segment")
	}
	position, err := patch.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if position != 1 {
		t.Fatalf("patch cursor moved to %d", position)
	}
}
