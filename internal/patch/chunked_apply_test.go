package patch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestChunkedReplaceRoundTrip(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "source.bin")
	targetPath := filepath.Join(workspace, "target.bin")
	patchPath := filepath.Join(workspace, "replace.bin")
	outputPath := filepath.Join(workspace, "output.bin")
	sourceData := bytes.Repeat([]byte("old-source-data-"), int((chunkedReplaceThreshold+4096)/16))
	targetData := bytes.Repeat([]byte("new-target-data-"), int((chunkedReplaceThreshold+4096)/16))
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, targetData, 0o600); err != nil {
		t.Fatal(err)
	}

	targetAccumulator := hashutil.NewAccumulator()
	if _, err := targetAccumulator.Write(targetData); err != nil {
		t.Fatal(err)
	}
	_, targetChunkDigests, err := targetAccumulator.SumHexAndChunks()
	if err != nil {
		t.Fatal(err)
	}
	created, err := createChunkedReplace(chunkedReplaceCreationRequest{
		ctx: context.Background(),
		target: fileSnapshot{
			SnapshotPath: targetPath,
			Size:         uint64(len(targetData)),
			ChunkDigests: targetChunkDigests,
		},
		outputPath:       patchPath,
		workDirectory:    workspace,
		compressionLevel: 1,
		workers:          2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.method != patchformat.MethodChunkedReplace {
		t.Fatalf("method=%q", created.method)
	}
	patchFile, err := os.Open(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	defer patchFile.Close()
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceFile.Close()
	outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer outputFile.Close()

	sourceHash, _, err := hashutil.Reader(bytes.NewReader(sourceData))
	if err != nil {
		t.Fatal(err)
	}
	targetHash, _, err := hashutil.Reader(bytes.NewReader(targetData))
	if err != nil {
		t.Fatal(err)
	}
	decoders, err := newDecoderPool(2)
	if err != nil {
		t.Fatal(err)
	}
	defer decoders.Close()
	if err := applyChunkedReplace(
		context.Background(),
		sourceFile,
		patchFile,
		outputFile,
		0,
		created.compressedSize,
		fileState{hash: sourceHash, size: uint64(len(sourceData))},
		fileState{hash: targetHash, size: uint64(len(targetData))},
		2,
		nil,
		progress.Event{},
		decoders,
	); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, targetData) {
		t.Fatal("chunked output does not match target")
	}
}

func TestSparseParallelRoundTrip(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "source.bin")
	targetPath := filepath.Join(workspace, "target.bin")
	operationsPath := filepath.Join(workspace, "sparse.bin")
	outputPath := filepath.Join(workspace, "output.bin")
	sourceData := bytes.Repeat([]byte("0123456789abcdef"), int((hashutil.ChunkSize+4096)/16))
	targetData := append([]byte(nil), sourceData...)
	for index := 17; index < len(targetData); index += 1000 {
		targetData[index] ^= 0x5a
	}
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, targetData, 0o600); err != nil {
		t.Fatal(err)
	}
	_, usable, err := createSparseStreamsOptimized(context.Background(), sourcePath, targetPath, operationsPath, "", uint64(len(targetData)))
	if err != nil {
		t.Fatal(err)
	}
	if !usable {
		t.Fatal("expected sparse representation")
	}
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceFile.Close()
	operations, err := os.Open(operationsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer operations.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	sourceHash, _, _ := hashutil.Reader(bytes.NewReader(sourceData))
	targetHash, _, _ := hashutil.Reader(bytes.NewReader(targetData))
	if err := applySparseStreamParallel(context.Background(), sourceFile, operations, output, uint64(len(sourceData)), sourceHash, targetHash, 2, nil, progress.Event{}); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, targetData) {
		t.Fatal("sparse output does not match target")
	}
}
