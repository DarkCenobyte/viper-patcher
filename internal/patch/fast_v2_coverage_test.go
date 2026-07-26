package patch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func coverageDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func coverageApplicationFiles(t *testing.T, sourceData, operationData []byte) (*os.File, *os.File, *os.File, string) {
	t.Helper()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	operationsPath := filepath.Join(directory, "operations.bin")
	outputPath := filepath.Join(directory, "output.bin")
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(operationsPath, operationData, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := os.Open(operationsPath)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		_ = operations.Close()
		_ = source.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = output.Close()
		_ = operations.Close()
		_ = source.Close()
	})
	return source, operations, output, outputPath
}

func coverageAppendUvarint(buffer *bytes.Buffer, value uint64) {
	var encoded [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(encoded[:], value)
	buffer.Write(encoded[:count])
}

func coverageCopyAddStream(build func(*bytes.Buffer)) []byte {
	var buffer bytes.Buffer
	buffer.Write(copyAddMagic[:])
	build(&buffer)
	return buffer.Bytes()
}

func coverageSparseStream(build func(*bytes.Buffer)) []byte {
	var buffer bytes.Buffer
	buffer.Write(sparseMagic[:])
	build(&buffer)
	return buffer.Bytes()
}

func TestApplyCopyAddStreamSuccessAndProgress(t *testing.T) {
	sourceData := []byte("abcdefgh")
	targetData := []byte("abcdWXYZ")
	operationsData := coverageCopyAddStream(func(buffer *bytes.Buffer) {
		buffer.WriteByte(copyAddOpcodeCopy)
		coverageAppendUvarint(buffer, 0)
		coverageAppendUvarint(buffer, 4)
		buffer.WriteByte(copyAddOpcodeAdd)
		coverageAppendUvarint(buffer, 4)
		buffer.WriteString("WXYZ")
		buffer.WriteByte(copyAddOpcodeEnd)
	})
	source, operations, output, outputPath := coverageApplicationFiles(t, sourceData, operationsData)
	var reported progress.Event
	err := applyCopyAddStream(
		source,
		operations,
		output,
		uint64(len(sourceData)),
		uint64(len(targetData)),
		coverageDigest(sourceData),
		coverageDigest(targetData),
		func(event progress.Event) { reported = event },
		progress.Event{Path: "game.bin"},
	)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, targetData) {
		t.Fatalf("output = %q, want %q", actual, targetData)
	}
	if reported.ProcessedBytes != uint64(len(targetData)) || reported.TotalBytes != uint64(len(targetData)) {
		t.Fatalf("unexpected progress event: %#v", reported)
	}
}

func TestApplyCopyAddStreamRejectsMalformedInput(t *testing.T) {
	if err := applyCopyAddStream(nil, nil, nil, 0, 0, "", "", nil, progress.Event{}); err == nil || !strings.Contains(err.Error(), "requires source") {
		t.Fatalf("unexpected nil-file error: %v", err)
	}

	tests := []struct {
		name       string
		sourceData []byte
		operations []byte
		targetSize uint64
		sourceHash string
		targetHash string
		want       string
	}{
		{
			name:       "source hash",
			sourceData: []byte("source"),
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) { buffer.WriteByte(copyAddOpcodeEnd) }),
			sourceHash: strings.Repeat("0", 64),
			targetHash: coverageDigest(nil),
			want:       "source failed",
		},
		{
			name:       "invalid magic",
			operations: []byte("not-copy-add"),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "invalid copy-add stream magic",
		},
		{
			name: "unsupported opcode",
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(99)
			}),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "unsupported copy-add opcode",
		},
		{
			name:       "zero copy length",
			sourceData: []byte("a"),
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeCopy)
				coverageAppendUvarint(buffer, 0)
				coverageAppendUvarint(buffer, 0)
			}),
			sourceHash: coverageDigest([]byte("a")),
			targetHash: coverageDigest(nil),
			want:       "COPY range",
		},
		{
			name:       "copy outside source",
			sourceData: []byte("a"),
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeCopy)
				coverageAppendUvarint(buffer, 1)
				coverageAppendUvarint(buffer, 1)
			}),
			sourceHash: coverageDigest([]byte("a")),
			targetHash: coverageDigest(nil),
			want:       "COPY range",
		},
		{
			name: "zero add length",
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeAdd)
				coverageAppendUvarint(buffer, 0)
			}),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "ADD range",
		},
		{
			name: "truncated add",
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeAdd)
				coverageAppendUvarint(buffer, 2)
				buffer.WriteByte('x')
			}),
			targetSize: 2,
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest([]byte("xx")),
			want:       "read ADD payload",
		},
		{
			name: "wrong output size",
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeEnd)
			}),
			targetSize: 1,
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest([]byte{0}),
			want:       "output size",
		},
		{
			name: "trailing data",
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeEnd)
				buffer.WriteByte(1)
			}),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "trailing data",
		},
		{
			name: "target hash",
			operations: coverageCopyAddStream(func(buffer *bytes.Buffer) {
				buffer.WriteByte(copyAddOpcodeAdd)
				coverageAppendUvarint(buffer, 1)
				buffer.WriteByte('x')
				buffer.WriteByte(copyAddOpcodeEnd)
			}),
			targetSize: 1,
			sourceHash: coverageDigest(nil),
			targetHash: strings.Repeat("0", 64),
			want:       "output failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, operations, output, _ := coverageApplicationFiles(t, test.sourceData, test.operations)
			err := applyCopyAddStream(
				source,
				operations,
				output,
				uint64(len(test.sourceData)),
				test.targetSize,
				test.sourceHash,
				test.targetHash,
				nil,
				progress.Event{},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestApplySparseStreamSuccessAndValidation(t *testing.T) {
	sourceData := []byte("abcdefgh")
	targetData := []byte("abcdXYgh")
	operationsData := coverageSparseStream(func(buffer *bytes.Buffer) {
		coverageAppendUvarint(buffer, 4)
		coverageAppendUvarint(buffer, 2)
		buffer.WriteString("XY")
		coverageAppendUvarint(buffer, 0)
		coverageAppendUvarint(buffer, 0)
	})
	source, operations, output, outputPath := coverageApplicationFiles(t, sourceData, operationsData)
	var reported progress.Event
	err := applySparseStream(
		source,
		operations,
		output,
		uint64(len(sourceData)),
		coverageDigest(sourceData),
		coverageDigest(targetData),
		func(event progress.Event) { reported = event },
		progress.Event{Path: "game.bin"},
	)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, targetData) {
		t.Fatalf("output = %q, want %q", actual, targetData)
	}
	if reported.ProcessedBytes != uint64(len(sourceData)) || reported.TotalBytes != uint64(len(sourceData)) {
		t.Fatalf("unexpected progress event: %#v", reported)
	}

	if err := applySparseStream(nil, nil, nil, 0, "", "", nil, progress.Event{}); err == nil || !strings.Contains(err.Error(), "requires source") {
		t.Fatalf("unexpected nil-file error: %v", err)
	}
}

func TestApplySparseStreamRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name       string
		sourceData []byte
		operations []byte
		expected   uint64
		sourceHash string
		targetHash string
		want       string
	}{
		{
			name:       "invalid magic",
			operations: []byte("not-sparse"),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "invalid sparse stream magic",
		},
		{
			name: "invalid terminator",
			operations: coverageSparseStream(func(buffer *bytes.Buffer) {
				coverageAppendUvarint(buffer, 1)
				coverageAppendUvarint(buffer, 0)
			}),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "invalid sparse terminator",
		},
		{
			name:       "range exceeds size",
			sourceData: []byte("abcdefgh"),
			operations: coverageSparseStream(func(buffer *bytes.Buffer) {
				coverageAppendUvarint(buffer, 8)
				coverageAppendUvarint(buffer, 1)
			}),
			expected:   8,
			sourceHash: coverageDigest([]byte("abcdefgh")),
			targetHash: coverageDigest([]byte("abcdefgh")),
			want:       "exceeds expected",
		},
		{
			name:       "truncated replacement",
			sourceData: []byte("ab"),
			operations: coverageSparseStream(func(buffer *bytes.Buffer) {
				coverageAppendUvarint(buffer, 0)
				coverageAppendUvarint(buffer, 2)
				buffer.WriteByte('X')
			}),
			expected:   2,
			sourceHash: coverageDigest([]byte("ab")),
			targetHash: coverageDigest([]byte("XY")),
			want:       "replacement bytes",
		},
		{
			name: "trailing data",
			operations: coverageSparseStream(func(buffer *bytes.Buffer) {
				coverageAppendUvarint(buffer, 0)
				coverageAppendUvarint(buffer, 0)
				buffer.WriteByte(1)
			}),
			sourceHash: coverageDigest(nil),
			targetHash: coverageDigest(nil),
			want:       "trailing data",
		},
		{
			name:       "source hash",
			sourceData: []byte("a"),
			operations: coverageSparseStream(func(buffer *bytes.Buffer) {
				coverageAppendUvarint(buffer, 0)
				coverageAppendUvarint(buffer, 0)
			}),
			expected:   1,
			sourceHash: strings.Repeat("0", 64),
			targetHash: coverageDigest([]byte("a")),
			want:       "source failed",
		},
		{
			name:       "target hash",
			sourceData: []byte("a"),
			operations: coverageSparseStream(func(buffer *bytes.Buffer) {
				coverageAppendUvarint(buffer, 0)
				coverageAppendUvarint(buffer, 0)
			}),
			expected:   1,
			sourceHash: coverageDigest([]byte("a")),
			targetHash: strings.Repeat("0", 64),
			want:       "output failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, operations, output, _ := coverageApplicationFiles(t, test.sourceData, test.operations)
			err := applySparseStream(
				source,
				operations,
				output,
				test.expected,
				test.sourceHash,
				test.targetHash,
				nil,
				progress.Event{},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestFastV2CreationCleanupAndUtilityBranches(t *testing.T) {
	if equalBytes([]byte{1}, []byte{1, 2}) || equalBytes([]byte{1}, []byte{2}) || !equalBytes([]byte{1, 2}, []byte{1, 2}) {
		t.Fatal("equalBytes returned an unexpected result")
	}

	directory := t.TempDir()
	chunkPath := filepath.Join(directory, "chunks.bin")
	if err := os.WriteFile(chunkPath, []byte("chunk"), 0o600); err != nil {
		t.Fatal(err)
	}
	chunkFile, err := os.Open(chunkPath)
	if err != nil {
		t.Fatal(err)
	}
	defer chunkFile.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := forEachContentChunk(ctx, chunkFile, func(contentChunk) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled chunking error = %v", err)
	}
	sentinel := errors.New("stop chunking")
	if err := forEachContentChunk(context.Background(), chunkFile, func(contentChunk) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error = %v", err)
	}

	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	forwardPath := filepath.Join(directory, "forward.ops")
	if err := os.WriteFile(sourcePath, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := createSparseStreams(sourcePath, targetPath, forwardPath, ""); err == nil || !strings.Contains(err.Error(), "equal source and target sizes") {
		t.Fatalf("unexpected sparse creation error: %v", err)
	}
	if _, err := os.Stat(forwardPath); !os.IsNotExist(err) {
		t.Fatalf("incomplete sparse stream was not removed: %v", err)
	}

	copyAddPath := filepath.Join(directory, "copy-add.ops")
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := createCopyAddStream(canceled, sourcePath, targetPath, copyAddPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected canceled copy-add error: %v", err)
	}
	if _, err := os.Stat(copyAddPath); !os.IsNotExist(err) {
		t.Fatalf("incomplete copy-add stream was not removed: %v", err)
	}
}
