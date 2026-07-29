//go:build ignore

package patchformat

import (
	"strings"
	"testing"
)

func TestOlderFormatsAreRejected(t *testing.T) {
	for _, version := range []int{1, 2} {
		header := validHeader()
		header.FormatVersion = version
		if err := ValidateHeader(header); err == nil || !strings.Contains(err.Error(), "unsupported VIPR format version") {
			t.Fatalf("version %d error = %v", version, err)
		}
	}
}

func TestFormatThreeMethods(t *testing.T) {
	for _, method := range []string{MethodCopyAdd, MethodSparse, MethodReplace, MethodChunkedReplace} {
		t.Run(method, func(t *testing.T) {
			header := validHeader()
			header.Files[0].SourceSize = 12
			header.Files[0].TargetSize = 12
			header.Files[0].ForwardMethod = method
			if method == MethodSparse || method == MethodCopyAdd {
				header.Files[0].ForwardExpandedLength = 12
			}
			if err := ValidateHeader(header); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFormatThreeRejectsMissingExpandedLength(t *testing.T) {
	for _, method := range []string{MethodSparse, MethodCopyAdd} {
		header := validHeader()
		header.Files[0].ForwardMethod = method
		if err := ValidateHeader(header); err == nil || !strings.Contains(err.Error(), "expanded length") {
			t.Fatalf("method %q error = %v", method, err)
		}
	}
}

func TestFormatThreeRejectsUnsafeInstructionLength(t *testing.T) {
	header := validHeader()
	header.Files[0].ForwardMethod = MethodCopyAdd
	header.Files[0].TargetSize = 1024
	header.Files[0].ForwardExpandedLength = 2*1024 + (1 << 20) + 1
	if err := ValidateHeader(header); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFormatThreeRejectsInconsistentMethodMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FileEntry)
		want   string
	}{
		{
			name: "sparse size change",
			mutate: func(entry *FileEntry) {
				entry.ForwardMethod = MethodSparse
				entry.ForwardExpandedLength = 12
				entry.SourceSize = 11
				entry.TargetSize = 12
			},
			want: "equal input and output sizes",
		},
		{
			name: "replace expanded length",
			mutate: func(entry *FileEntry) {
				entry.ForwardMethod = MethodReplace
				entry.ForwardExpandedLength = 1
			},
			want: "must not declare an expanded length",
		},
		{
			name: "empty chunked replacement",
			mutate: func(entry *FileEntry) {
				entry.ForwardMethod = MethodChunkedReplace
				entry.TargetSize = 0
			},
			want: "non-empty output",
		},
		{
			name: "chunked expanded length",
			mutate: func(entry *FileEntry) {
				entry.ForwardMethod = MethodChunkedReplace
				entry.TargetSize = 1
				entry.ForwardExpandedLength = 1
			},
			want: "must not declare an expanded length",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := validHeader()
			test.mutate(&header.Files[0])
			err := ValidateHeader(header)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLegacyMetadataIsRejected(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Header)
	}{
		{"sha256", func(header *Header) { header.HashAlgorithm = "sha256" }},
		{"patch-from mode", func(header *Header) { header.Compression.Mode = "patch-from" }},
		{"zstd algorithm", func(header *Header) { header.Compression.Algorithm = "zstd" }},
		{"patch-from method", func(header *Header) { header.Files[0].ForwardMethod = "zstd-patch-from" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := validHeader()
			test.mutate(&header)
			if err := ValidateHeader(header); err == nil {
				t.Fatal("expected legacy metadata to be rejected")
			}
		})
	}
}
