//go:build vipr_legacy_zstd

package zstd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

const testLargePositionalOffset int64 = (2 << 30) + (64 << 10) + 123

func TestPositionalSegmentsBeyondTwoGiB(t *testing.T) {
	directory := t.TempDir()
	payload := bytes.Repeat([]byte("viper-large-offset-"), 4096)

	sourcePath := filepath.Join(directory, "large-source.bin")
	source, err := os.OpenFile(sourcePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if _, err := source.WriteAt(payload, testLargePositionalOffset); err != nil {
		t.Fatal(err)
	}
	if err := source.Sync(); err != nil {
		t.Fatal(err)
	}

	compressedPath := filepath.Join(directory, "large-segment.zst")
	if err := CompressFileSegment(
		source,
		uint64(testLargePositionalOffset),
		uint64(len(payload)),
		compressedPath,
		3,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) == 0 {
		t.Fatal("compressed segment is empty")
	}

	patchPath := filepath.Join(directory, "large-patch.bin")
	patch, err := os.OpenFile(patchPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = patch.Close() })
	if _, err := patch.WriteAt(compressed, testLargePositionalOffset); err != nil {
		t.Fatal(err)
	}
	if err := patch.Sync(); err != nil {
		t.Fatal(err)
	}

	windowSize, err := FrameWindowSize(
		patch,
		uint64(testLargePositionalOffset),
		uint64(len(compressed)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if windowSize == 0 {
		t.Fatal("frame window size is zero")
	}

	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = decoder.Close() })
	var output bytes.Buffer
	if err := decoder.DecompressSegmentToWriter(
		context.Background(),
		patch,
		uint64(testLargePositionalOffset),
		uint64(len(compressed)),
		&output,
		uint64(len(payload)),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), payload) {
		t.Fatal("large-offset segment round trip mismatch")
	}
}
