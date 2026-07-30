//go:build vipr_legacy_zstd

package zstd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameWindowSizeReadsCompressedHeader(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input.bin")
	framePath := filepath.Join(directory, "frame.zst")
	if err := os.WriteFile(inputPath, bytes.Repeat([]byte("frame-window-"), 1<<12), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CompressFile(inputPath, framePath, 1, nil); err != nil {
		t.Fatal(err)
	}
	frame, err := os.Open(framePath)
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	info, err := frame.Stat()
	if err != nil {
		t.Fatal(err)
	}
	window, err := FrameWindowSize(frame, 0, uint64(info.Size()))
	if err != nil {
		t.Fatal(err)
	}
	if window == 0 || window > DecoderWindowLimit() {
		t.Fatalf("frame window = %d, decoder limit = %d", window, DecoderWindowLimit())
	}
}

func TestFrameWindowSizeRejectsWindowAboveDefaultLimit(t *testing.T) {
	frame := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x89, 0x01, 0x00, 0x00}
	path := filepath.Join(t.TempDir(), "oversized-window.zst")
	if err := os.WriteFile(path, frame, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := FrameWindowSize(file, 0, uint64(len(frame))); err == nil || !strings.Contains(strings.ToLower(err.Error()), "window") {
		t.Fatalf("unexpected oversized-window error: %v", err)
	}
}
