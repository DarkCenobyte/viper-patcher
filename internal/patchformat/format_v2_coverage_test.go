package patchformat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

type coverageFailWriter struct {
	failAt int
	writes int
}

func (writer *coverageFailWriter) Write(data []byte) (int, error) {
	if writer.writes == writer.failAt {
		return 0, errors.New("injected write failure")
	}
	writer.writes++
	return len(data), nil
}

func coverageHybridReverseHeader() Header {
	header := validHeader()
	header.Compression = Compression{
		Algorithm: AlgorithmHybrid,
		Library:   SupportedZstdVersion,
		Mode:      CompressionHybridV2,
		Level:     3,
	}
	header.Reverse = true
	header.Files[0].ForwardMethod = MethodReplace
	header.Files[0].ReverseMethod = MethodReplace
	header.Files[0].ReverseLength = 10
	return header
}

func TestReverseDifferentialMethodCoverage(t *testing.T) {
	entry := FileEntry{}
	if got := entry.ReverseDifferentialMethod(); got != MethodPatchFrom {
		t.Fatalf("default reverse method = %q", got)
	}
	entry.ReverseMethod = MethodCopyAdd
	if got := entry.ReverseDifferentialMethod(); got != MethodCopyAdd {
		t.Fatalf("explicit reverse method = %q", got)
	}
}

func TestVersionTwoValidationBranches(t *testing.T) {
	tests := []struct {
		name   string
		header func() Header
		want   string
	}{
		{
			name: "hybrid mode algorithm",
			header: func() Header {
				header := validHeader()
				header.Compression.Mode = CompressionHybridV2
				return header
			},
			want: "requires algorithm",
		},
		{
			name: "patch-from mode algorithm",
			header: func() Header {
				header := validHeader()
				header.Compression.Algorithm = AlgorithmHybrid
				return header
			},
			want: "requires algorithm",
		},
		{
			name: "unsupported version two mode",
			header: func() Header {
				header := validHeader()
				header.Compression.Mode = "unknown"
				return header
			},
			want: "unsupported version 2 compression mode",
		},
		{
			name: "legacy algorithm",
			header: func() Header {
				header := validHeader()
				header.FormatVersion = LegacyFormatVersion
				header.Compression.Algorithm = AlgorithmHybrid
				return header
			},
			want: "version 1 compression algorithm",
		},
		{
			name: "legacy mode",
			header: func() Header {
				header := validHeader()
				header.FormatVersion = LegacyFormatVersion
				header.Compression.Mode = CompressionHybridV2
				return header
			},
			want: "version 1 compression mode",
		},
		{
			name: "unsupported reverse method",
			header: func() Header {
				header := coverageHybridReverseHeader()
				header.Files[0].ReverseMethod = "unknown"
				return header
			},
			want: "unsupported reverse method",
		},
		{
			name: "missing reverse expanded length",
			header: func() Header {
				header := coverageHybridReverseHeader()
				header.Files[0].ReverseMethod = MethodSparse
				return header
			},
			want: "no reverse expanded length",
		},
		{
			name: "hybrid reverse with patch-from metadata",
			header: func() Header {
				header := validHeader()
				header.Reverse = true
				header.Files[0].ReverseMethod = MethodReplace
				header.Files[0].ReverseLength = 10
				return header
			},
			want: "hybrid reverse method",
		},
		{
			name: "unsafe reverse instruction length",
			header: func() Header {
				header := coverageHybridReverseHeader()
				header.Files[0].SourceSize = 1024
				header.Files[0].ReverseMethod = MethodCopyAdd
				header.Files[0].ReverseExpandedLength = 2*1024 + (1 << 20) + 1
				return header
			},
			want: "unsafe reverse instruction-stream",
		},
		{
			name: "unexpected reverse metadata",
			header: func() Header {
				header := validHeader()
				header.Files[0].ReverseOffset = 1
				return header
			},
			want: "unexpectedly contains reverse data",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateHeader(test.header())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	if validInstructionExpandedLength(1, ^uint64(0)) {
		t.Fatal("overflowing instruction limit was accepted")
	}
}

func TestEncodePrefixWriteFailures(t *testing.T) {
	for failAt := 0; failAt < 3; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			writer := &coverageFailWriter{failAt: failAt}
			if _, err := EncodePrefix(writer, validHeader()); err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDecodeAdditionalTruncationCases(t *testing.T) {
	t.Run("header length", func(t *testing.T) {
		if _, err := Decode(bytes.NewReader(Magic[:])); err == nil || !strings.Contains(err.Error(), "header length") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("zero header", func(t *testing.T) {
		var buffer bytes.Buffer
		buffer.Write(Magic[:])
		if err := binary.Write(&buffer, binary.LittleEndian, uint64(0)); err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(&buffer); err == nil || !strings.Contains(err.Error(), "invalid patch header length") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("truncated header", func(t *testing.T) {
		var buffer bytes.Buffer
		buffer.Write(Magic[:])
		if err := binary.Write(&buffer, binary.LittleEndian, uint64(4)); err != nil {
			t.Fatal(err)
		}
		buffer.Write([]byte("{}"))
		if _, err := Decode(&buffer); err == nil || !strings.Contains(err.Error(), "read patch header") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
