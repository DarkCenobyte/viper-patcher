package patchformat

import (
	"strings"
	"testing"
)

func TestVersionOneHeaderRemainsReadable(t *testing.T) {
	header := validHeader()
	header.FormatVersion = LegacyFormatVersion
	header.Compression = Compression{Algorithm: AlgorithmZstd, Library: SupportedZstdVersion, Mode: CompressionPatchFrom, Level: 3}
	header.Files[0].ForwardMethod = ""
	if err := ValidateHeader(header); err != nil {
		t.Fatal(err)
	}
	if got := header.Files[0].ForwardDifferentialMethod(); got != MethodPatchFrom {
		t.Fatalf("legacy method = %q", got)
	}
}

func TestVersionTwoMethods(t *testing.T) {
	for _, method := range []string{MethodPatchFrom, MethodCopyAdd, MethodSparse, MethodReplace} {
		t.Run(method, func(t *testing.T) {
			header := validHeader()
			header.Compression = Compression{Algorithm: AlgorithmHybrid, Library: SupportedZstdVersion, Mode: CompressionHybridV2, Level: 3}
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

func TestVersionTwoRejectsSparseWithoutExpandedLength(t *testing.T) {
	header := validHeader()
	header.Compression = Compression{Algorithm: AlgorithmHybrid, Library: SupportedZstdVersion, Mode: CompressionHybridV2, Level: 3}
	header.Files[0].ForwardMethod = MethodSparse
	if err := ValidateHeader(header); err == nil || !strings.Contains(err.Error(), "expanded length") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionTwoRejectsUnsafeInstructionLength(t *testing.T) {
	header := validHeader()
	header.Compression = Compression{Algorithm: AlgorithmHybrid, Library: SupportedZstdVersion, Mode: CompressionHybridV2, Level: 3}
	header.Files[0].ForwardMethod = MethodCopyAdd
	header.Files[0].TargetSize = 1024
	header.Files[0].ForwardExpandedLength = 2*(1024) + (1 << 20) + 1
	if err := ValidateHeader(header); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionTwoRejectsHybridMethodWithPatchFromMetadata(t *testing.T) {
	header := validHeader()
	header.Compression = Compression{Algorithm: AlgorithmZstd, Library: SupportedZstdVersion, Mode: CompressionPatchFrom, Level: 3}
	header.Files[0].ForwardMethod = MethodReplace
	if err := ValidateHeader(header); err == nil || !strings.Contains(err.Error(), "hybrid") {
		t.Fatalf("unexpected error: %v", err)
	}
}
