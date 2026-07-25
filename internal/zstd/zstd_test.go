package zstd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompressAndDecompressFile(t *testing.T) {
	root := t.TempDir()
	reference := filepath.Join(root, "reference.bin")
	target := filepath.Join(root, "target.bin")
	patch := filepath.Join(root, "patch.zst")
	output := filepath.Join(root, "output.bin")
	oldData := []byte(strings.Repeat("old-data-", 20000))
	newData := append([]byte(nil), oldData...)
	copy(newData[1000:], []byte("changed-content-here!"))
	newData = append(newData, []byte("tail")...)
	if err := os.WriteFile(reference, oldData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, newData, 0o644); err != nil {
		t.Fatal(err)
	}
	var compressionProgress uint64
	if err := CompressFile(reference, target, patch, 5, func(processed, total uint64) {
		compressionProgress = processed
		if total != uint64(len(newData)) {
			t.Errorf("compression total = %d", total)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if compressionProgress != uint64(len(newData)) {
		t.Fatalf("compression progress = %d", compressionProgress)
	}
	info, err := os.Stat(patch)
	if err != nil {
		t.Fatal(err)
	}
	var decompressionProgress uint64
	if err := DecompressSegment(reference, patch, 0, uint64(info.Size()), output, uint64(len(newData)), func(processed, total uint64) {
		decompressionProgress = processed
	}); err != nil {
		t.Fatal(err)
	}
	if decompressionProgress != uint64(len(newData)) {
		t.Fatalf("decompression progress = %d", decompressionProgress)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(newData) {
		t.Fatal("decompressed output does not match target")
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
	if err := CompressFile("unused", "unused", "unused", minimum-1, nil); err == nil {
		t.Fatal("expected invalid compression level to be rejected")
	}
}

func TestDecompressRejectsTruncatedSegment(t *testing.T) {
	root := t.TempDir()
	reference := filepath.Join(root, "reference.bin")
	patch := filepath.Join(root, "patch.zst")
	output := filepath.Join(root, "output.bin")
	if err := os.WriteFile(reference, []byte("reference"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patch, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DecompressSegment(reference, patch, 0, 10, output, 1, nil); err == nil {
		t.Fatal("expected truncated segment to be rejected")
	}
}
