package patch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type countingReader struct {
	reader *bytes.Reader
	read   int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.read += count
	return count, err
}

func TestDecodeAndHashPatchStopsAfterStructuralError(t *testing.T) {
	contents := append([]byte("NOTVIPR!"), bytes.Repeat([]byte{0x5a}, 8<<20)...)
	reader := &countingReader{reader: bytes.NewReader(contents)}
	if _, _, err := decodeAndHashPatch(reader); err == nil {
		t.Fatal("expected invalid patch magic to be rejected")
	}
	const structuralPrefixSize = 8
	if reader.read != structuralPrefixSize {
		t.Fatalf("read %d bytes after a structural error, want %d", reader.read, structuralPrefixSize)
	}
}

func TestCopyAddGearTableMatchesMixer(t *testing.T) {
	for value := range 256 {
		if got, want := copyAddGearTable[value], mixCopyAddGear(byte(value)); got != want {
			t.Fatalf("gear value %d = %d, want %d", value, got, want)
		}
	}
}

func TestContentChunkingPreservesDataAndOffsets(t *testing.T) {
	data := make([]byte, 3*copyAddChunkDefaultMax+12345)
	for index := range data {
		data[index] = byte((index*131 + index/17) & 0xff)
	}
	path := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var rebuilt []byte
	var expectedOffset uint64
	profile := copyAddProfileForSize(uint64(len(data)))
	if err := forEachContentChunk(context.Background(), file, profile, func(chunk contentChunk) error {
		if chunk.offset != expectedOffset {
			t.Fatalf("chunk offset = %d, want %d", chunk.offset, expectedOffset)
		}
		if len(chunk.data) > profile.maximum {
			t.Fatalf("chunk length = %d, maximum = %d", len(chunk.data), profile.maximum)
		}
		rebuilt = append(rebuilt, chunk.data...)
		expectedOffset += uint64(len(chunk.data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rebuilt, data) {
		t.Fatal("block-based chunking changed the byte stream")
	}
}

func TestSparseCandidateRejectsEarlyAndRemovesStreams(t *testing.T) {
	directory := t.TempDir()
	size := 8 << 20
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	forwardPath := filepath.Join(directory, "forward.ops")
	reversePath := filepath.Join(directory, "reverse.ops")
	if err := os.WriteFile(sourcePath, bytes.Repeat([]byte{0x00}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, bytes.Repeat([]byte{0xff}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, usable, err := createSparseStreamsOptimized(context.Background(), sourcePath, targetPath, forwardPath, reversePath, uint64(size))
	if err != nil {
		t.Fatal(err)
	}
	if usable || sparseWorthUsing(stats, uint64(size)) {
		t.Fatalf("rejected sparse candidate remained usable: %#v", stats)
	}
	for _, path := range []string{forwardPath, reversePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("rejected sparse stream %q was not removed: %v", path, err)
		}
	}
}

func TestCopyAddCandidateRejectsAndRemovesStream(t *testing.T) {
	directory := t.TempDir()
	size := 8 << 20
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	outputPath := filepath.Join(directory, "copy-add.ops")
	if err := os.WriteFile(sourcePath, bytes.Repeat([]byte{0x00}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, bytes.Repeat([]byte{0xff}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, usable, err := createCopyAddStreamOptimized(context.Background(), sourcePath, targetPath, outputPath, uint64(size))
	if err != nil {
		t.Fatal(err)
	}
	if usable || copyAddWorthUsing(stats, uint64(size)) {
		t.Fatalf("rejected COPY/ADD candidate remained usable: %#v", stats)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("rejected COPY/ADD stream was not removed: %v", err)
	}
}

func TestVerifySourceReportsProgressAndHonorsCancellation(t *testing.T) {
	data := bytes.Repeat([]byte("verify-progress-"), 1<<20)
	path := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	digest, _, err := hashutil.Reader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var events []progress.Event
	if err := verifySourceForDecode(context.Background(), file, fileState{
		hash: digest,
		size: uint64(len(data)),
	}, 2, func(event progress.Event) {
		events = append(events, event)
	}, progress.Event{FileIndex: 1, FileCount: 1, Path: "source.bin"}); err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[0].Stage != progress.StageVerifying || events[len(events)-1].ProcessedBytes != uint64(len(data)) {
		t.Fatalf("unexpected verification progress: %#v", events)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifySourceForDecode(ctx, file, fileState{}, 2, nil, progress.Event{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled verification error = %v", err)
	}
}

func TestInspectContextReturnsCanceledBeforeOpeningRoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectContext(ctx, filepath.Join(t.TempDir(), "missing"), patchformat.Patch{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("inspection error = %v", err)
	}
}

func TestInstructionParsersHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	outputPath := filepath.Join(directory, "output.bin")
	if err := os.WriteFile(sourcePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	if err := applySparseStreamParallel(ctx, source, bytes.NewReader(sparseMagic[:]), output, 0, "", "", 1, nil, progress.Event{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("sparse cancellation error = %v", err)
	}
	if err := applyCopyAddStreamContext(ctx, source, bytes.NewReader(copyAddMagic[:]), output, 0, 0, "", nil, progress.Event{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("COPY/ADD cancellation error = %v", err)
	}
}

func TestLargeInstructionStreamUsesBoundedStreamingPath(t *testing.T) {
	directory := t.TempDir()
	operationsPath := filepath.Join(directory, "operations.bin")
	compressedPath := filepath.Join(directory, "operations.zst")
	sourcePath := filepath.Join(directory, "source.bin")
	outputPath := filepath.Join(directory, "output.bin")
	payload := bytes.Repeat([]byte("streamed-instruction-data"), int(instructionMemoryThreshold/24)+4096)

	var operations bytes.Buffer
	operations.Write(copyAddMagic[:])
	operations.WriteByte(copyAddOpcodeAdd)
	var encoded [10]byte
	count := binary.PutUvarint(encoded[:], uint64(len(payload)))
	operations.Write(encoded[:count])
	operations.Write(payload)
	operations.WriteByte(copyAddOpcodeEnd)
	if uint64(operations.Len()) <= instructionMemoryThreshold {
		t.Fatalf("test instruction stream is too small: %d", operations.Len())
	}
	for path, data := range map[string][]byte{
		operationsPath: operations.Bytes(),
		sourcePath:     nil,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := zstd.CompressFile(operationsPath, compressedPath, 3, nil); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.Open(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	compressedInfo, err := compressed.Stat()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	decoders, err := newDecoderPool(1, newZstdWindowBudget(processZstdWindowBudgetLimit()))
	if err != nil {
		t.Fatal(err)
	}
	defer decoders.Close()
	decoder, releaseDecoder, err := decoders.acquire(context.Background(), compressed, 0, uint64(compressedInfo.Size()))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseDecoder()
	targetHash, _, err := hashutil.Reader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyCompressedInstructionStream(
		context.Background(), decoder, uint64(operations.Len()),
		func(reader io.Reader) error {
			return applyCopyAddStreamContext(
				context.Background(), source, reader, output, 0, uint64(len(payload)), targetHash, nil, progress.Event{},
			)
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatal("streamed instruction output does not match")
	}
}
