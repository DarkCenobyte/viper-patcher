package zstd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompressAndDecompressFile(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.bin")
	compressed := filepath.Join(root, "payload.zst")
	output := filepath.Join(root, "output.bin")
	data := []byte(strings.Repeat("standalone-data-", 20000))
	if err := os.WriteFile(input, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var compressionProgress uint64
	if err := CompressFile(input, compressed, 5, func(processed, total uint64) {
		compressionProgress = processed
		if total != uint64(len(data)) {
			t.Errorf("compression total = %d", total)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if compressionProgress != uint64(len(data)) {
		t.Fatalf("compression progress = %d", compressionProgress)
	}
	info, err := os.Stat(compressed)
	if err != nil {
		t.Fatal(err)
	}
	var decompressionProgress uint64
	if err := decompressSegmentToPath(compressed, 0, uint64(info.Size()), output, uint64(len(data)), func(processed, total uint64) {
		decompressionProgress = processed
	}); err != nil {
		t.Fatal(err)
	}
	if decompressionProgress != uint64(len(data)) {
		t.Fatalf("decompression progress = %d", decompressionProgress)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(data) {
		t.Fatal("decompressed output does not match input")
	}
}

func TestCompressFileSegment(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "input.bin")
	compressed := filepath.Join(root, "segment.zst")
	output := filepath.Join(root, "output.bin")
	prefix := []byte(strings.Repeat("prefix-", 5000))
	segment := []byte(strings.Repeat("selected-segment-", 30000))
	suffix := []byte(strings.Repeat("suffix-", 5000))
	data := append(append(append([]byte(nil), prefix...), segment...), suffix...)
	if err := os.WriteFile(inputPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var compressionProgress uint64
	if err := CompressFileSegment(input, uint64(len(prefix)), uint64(len(segment)), compressed, 5, func(processed, total uint64) {
		compressionProgress = processed
		if total != uint64(len(segment)) {
			t.Errorf("segment compression total = %d", total)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if compressionProgress != uint64(len(segment)) {
		t.Fatalf("segment compression progress = %d", compressionProgress)
	}
	if position, err := input.Seek(0, io.SeekCurrent); err != nil || position != 0 {
		t.Fatalf("input cursor changed: position=%d err=%v", position, err)
	}
	info, err := os.Stat(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if err := decompressSegmentToPath(compressed, 0, uint64(info.Size()), output, uint64(len(segment)), nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(segment) {
		t.Fatal("decompressed segment does not match selected input range")
	}
}

func TestCompressionLevelRange(t *testing.T) {
	minimum, maximum := CompressionLevelRange()
	if minimum >= maximum || maximum < 22 {
		t.Fatalf("unexpected range %d..%d", minimum, maximum)
	}
	if Version() != "1.5.7" {
		t.Fatalf("linked zstd version = %q, want 1.5.7", Version())
	}
}

func TestCompressRejectsInvalidLevel(t *testing.T) {
	minimum, _ := CompressionLevelRange()
	if err := CompressFile("unused", "unused", minimum-1, nil); err == nil {
		t.Fatal("expected invalid compression level to be rejected")
	}
}

func TestCompressFileSegmentRejectsInvalidInput(t *testing.T) {
	if err := CompressFileSegment(nil, 0, 1, "unused", 3, nil); err == nil {
		t.Fatal("expected nil segment input to be rejected")
	}
}

func TestDecompressRejectsTruncatedSegment(t *testing.T) {
	root := t.TempDir()
	compressed := filepath.Join(root, "payload.zst")
	output := filepath.Join(root, "output.bin")
	if err := os.WriteFile(compressed, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := decompressSegmentToPath(compressed, 0, 10, output, 1, nil); err == nil {
		t.Fatal("expected truncated segment to be rejected")
	}
}

func TestDecompressStopsAtDeclaredOutputSize(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.bin")
	compressed := filepath.Join(root, "payload.zst")
	output := filepath.Join(root, "output.bin")
	data := []byte(strings.Repeat("decompression-limit-", 600000))
	if err := os.WriteFile(input, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CompressFile(input, compressed, 3, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(compressed)
	if err != nil {
		t.Fatal(err)
	}
	declaredSize := uint64(2 << 20)
	if err := decompressSegmentToPath(compressed, 0, uint64(info.Size()), output, declaredSize, nil); err == nil {
		t.Fatal("expected output-size limit to be enforced")
	}
	outputInfo, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if outputInfo.Size() > int64(declaredSize) {
		t.Fatalf("output size = %d, declared maximum = %d", outputInfo.Size(), declaredSize)
	}
}

func decompressSegmentToPath(compressedPath string, offset, length uint64, outputPath string, expectedOutputSize uint64, callback ProgressFunc) error {
	compressed, err := os.Open(compressedPath)
	if err != nil {
		return err
	}
	defer compressed.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder()
	if err != nil {
		_ = output.Close()
		return err
	}
	operationError := decoder.DecompressSegmentToFile(context.Background(), compressed, offset, length, output, expectedOutputSize, callback, nil)
	return errors.Join(operationError, decoder.Close(), output.Close())
}
