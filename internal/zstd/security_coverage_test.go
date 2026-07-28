package zstd

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPositionalReadHandleValidation(t *testing.T) {
	if _, _, err := acquirePositionalReadHandle(nil); err == nil {
		t.Fatal("nil positional input was accepted")
	}
	path := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(path, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	handle, release, err := acquirePositionalReadHandle(file)
	if err != nil {
		t.Fatal(err)
	}
	if handle == 0 {
		t.Fatal("positional input returned an invalid handle")
	}
	release()
}

func TestFrameWindowSizeValidationPaths(t *testing.T) {
	if _, err := FrameWindowSize(nil, 0, 1); err == nil {
		t.Fatal("nil frame file was accepted")
	}
	path := filepath.Join(t.TempDir(), "frame.zst")
	if err := os.WriteFile(path, []byte("not-zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := FrameWindowSize(file, 0, 0); err == nil {
		t.Fatal("empty frame was accepted")
	}
	if _, err := FrameWindowSize(file, uint64(math.MaxInt64)+1, 1); err == nil {
		t.Fatal("frame offset outside the signed range was accepted")
	}
	if _, err := FrameWindowSize(file, uint64(math.MaxInt64), 2); err == nil {
		t.Fatal("frame range overflow was accepted")
	}
	if _, err := FrameWindowSize(file, 0, 8); err == nil {
		t.Fatal("invalid zstd frame was accepted")
	}
}

func TestDecoderWindowLimitValidationPaths(t *testing.T) {
	var unavailable *Decoder
	if err := unavailable.SetWindowLimit(1); err == nil {
		t.Fatal("nil decoder accepted a window limit")
	}
	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	limit := DecoderWindowLimit()
	if err := decoder.SetWindowLimit(0); err == nil {
		t.Fatal("zero decoder window was accepted")
	}
	if err := decoder.SetWindowLimit(limit + 1); err == nil {
		t.Fatal("oversized decoder window was accepted")
	}
	if err := decoder.SetWindowLimit(limit); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := decoder.SetWindowLimit(limit); err == nil {
		t.Fatal("closed decoder accepted a window limit")
	}
}
