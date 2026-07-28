package patch

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

func writeTestFile(t *testing.T, path string, data []byte) *os.File {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func chunkedContainer(descriptors []chunkedReplaceDescriptor, payload []byte) []byte {
	var buffer bytes.Buffer
	buffer.Write(chunkedReplaceMagic[:])
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(len(descriptors)))
	for _, descriptor := range descriptors {
		_ = writeChunkedDescriptor(&buffer, descriptor)
	}
	buffer.Write(payload)
	return buffer.Bytes()
}

func TestInspectChunkedReplaceRejectsInvalidContainers(t *testing.T) {
	validDigest := strings.Repeat("0", 64)
	tests := []struct {
		name         string
		container    []byte
		expectedSize uint64
	}{
		{name: "truncated", container: []byte("short"), expectedSize: 1},
		{name: "bad magic", container: append([]byte("NOTCHUNK"), 0, 0, 0, 0), expectedSize: 1},
		{name: "zero count", container: chunkedContainer(nil, nil), expectedSize: 1},
		{name: "wrong count", container: chunkedContainer([]chunkedReplaceDescriptor{{offset: 0, size: 1, compressedLength: 1, digest: validDigest}}, []byte{1}), expectedSize: hashutil.ChunkSize + 1},
		{name: "bad offset", container: chunkedContainer([]chunkedReplaceDescriptor{{offset: 1, size: 1, compressedLength: 1, digest: validDigest}}, []byte{1}), expectedSize: 1},
		{name: "zero size", container: chunkedContainer([]chunkedReplaceDescriptor{{offset: 0, size: 0, compressedLength: 1, digest: validDigest}}, []byte{1}), expectedSize: 1},
		{name: "zero compressed", container: chunkedContainer([]chunkedReplaceDescriptor{{offset: 0, size: 1, compressedLength: 0, digest: validDigest}}, nil), expectedSize: 1},
		{name: "trailing payload", container: chunkedContainer([]chunkedReplaceDescriptor{{offset: 0, size: 1, compressedLength: 1, digest: validDigest}}, []byte{1, 2}), expectedSize: 1},
		{
			name: "non-canonical chunk boundary",
			container: chunkedContainer([]chunkedReplaceDescriptor{
				{offset: 0, size: 6 << 20, compressedLength: 1, digest: validDigest},
				{offset: 6 << 20, size: 6 << 20, compressedLength: 1, digest: validDigest},
			}, []byte{1, 2}),
			expectedSize: 12 << 20,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.bin")
			file := writeTestFile(t, path, test.container)
			defer file.Close()
			if _, err := inspectChunkedReplace(file, 0, uint64(len(test.container)), test.expectedSize); err == nil {
				t.Fatal("expected invalid container to be rejected")
			}
		})
	}
	if _, err := inspectChunkedReplace(nil, 0, 0, 0); err == nil {
		t.Fatal("expected nil patch file to be rejected")
	}
}

func TestStreamSparseChunkPlansRejectsInvalidStreams(t *testing.T) {
	validPrefix := append([]byte(nil), sparseMagic[:]...)
	tests := []struct {
		name string
		data []byte
		size uint64
	}{
		{name: "bad magic", data: []byte("NOTSPARS"), size: 1},
		{name: "invalid terminator", data: append(validPrefix, 1, 0), size: 1},
		{name: "out of range", data: append(validPrefix, 2, 1, 0), size: 1},
		{name: "truncated replacement", data: append(validPrefix, 0, 2, 1), size: 2},
		{name: "trailing data", data: append(append(validPrefix, 0, 0), 1), size: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := streamSparseChunkPlans(context.Background(), bytes.NewReader(test.data), test.size, func(uint64, sparseChunkPlan) error { return nil })
			if err == nil {
				t.Fatal("expected invalid sparse stream to be rejected")
			}
		})
	}
}

func TestStandaloneReplaceVerifiesAndAppliesConcurrently(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "source.bin")
	targetPath := filepath.Join(workspace, "target.bin")
	patchPath := filepath.Join(workspace, "target.zst")
	outputPath := filepath.Join(workspace, "output.bin")
	sourceData := bytes.Repeat([]byte("old-data-"), 1<<16)
	targetData := bytes.Repeat([]byte("new-data-"), 1<<16)
	for path, data := range map[string][]byte{sourcePath: sourceData, targetPath: targetData} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := zstd.CompressFile(targetPath, patchPath, 1, nil); err != nil {
		t.Fatal(err)
	}
	patchInfo, err := os.Stat(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	source := writeTestFile(t, sourcePath, sourceData)
	defer source.Close()
	patch := writeTestFile(t, patchPath, mustReadFile(t, patchPath))
	defer patch.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	sourceHash, _, _ := hashutil.Reader(bytes.NewReader(sourceData))
	targetHash, _, _ := hashutil.Reader(bytes.NewReader(targetData))
	decoders, err := newDecoderPool(2, newZstdWindowBudget(processZstdWindowBudgetLimit()))
	if err != nil {
		t.Fatal(err)
	}
	defer decoders.Close()
	if err := applyStandaloneReplaceConcurrent(
		context.Background(), source, patch, output, 0, uint64(patchInfo.Size()),
		fileState{hash: sourceHash, size: uint64(len(sourceData))},
		fileState{hash: targetHash, size: uint64(len(targetData))}, 2, nil, progress.Event{}, decoders,
	); err != nil {
		t.Fatal(err)
	}
	if actual := mustReadFile(t, outputPath); !bytes.Equal(actual, targetData) {
		t.Fatal("standalone replacement output does not match")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
