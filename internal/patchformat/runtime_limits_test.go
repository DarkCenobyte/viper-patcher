package patchformat

import (
	"bytes"
	"testing"
	"time"
)

func minimalLimitedHeader(t *testing.T) Header {
	t.Helper()
	var digest Digest
	digest[0] = 1
	return Header{
		FormatVersion: FormatVersion,
		CreatedAt:     time.Unix(1, 0).UTC(),
		Creator:       CreatorInfo{Name: "test"},
		HashAlgorithm: HashBLAKE3Tree,
		Compression: Compression{
			Algorithm: AlgorithmHybrid,
			Library:   SupportedZstdVersion,
			Mode:      CompressionHybrid,
			Level:     3,
		},
		DefaultWindowSize: 256 << 10,
		Files: []FileEntry{{
			Path:            "file.bin",
			SourceHash:      digest.Hex(),
			TargetHash:      digest.Hex(),
			SourceDigest:    digest,
			TargetDigest:    digest,
			WindowSize:      256 << 10,
			SourceChunkSize: uint32(IdentityChunkSize),
		}},
	}
}

func TestDecodeAtWithLimitsRejectsWireIndexBeforeAllocation(t *testing.T) {
	var footer [FooterSize]byte
	copy(footer[:8], FooterMagic[:])
	footer[16] = 1
	container := append(make([]byte, PrefixSize), footer[:]...)
	_, err := DecodeAtWithLimits(bytes.NewReader(container), uint64(len(container)), DecodeLimits{MaxIndexBytes: 0}, nil)
	if err == nil {
		t.Fatal("invalid limited container unexpectedly accepted")
	}
}

func TestEstimateDecodedIndexHonorsAggregateLimits(t *testing.T) {
	header := minimalLimitedHeader(t)
	header.Files[0].ForwardWindows = make([]WindowDescriptor, 4)
	_, counts, err := estimateDecodedIndex(header)
	if err != nil {
		t.Fatal(err)
	}
	if counts.files != 1 || counts.windows != 4 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestDefaultDecodeLimitsAreStricterOn32Bit(t *testing.T) {
	limits := DefaultDecodeLimits()
	if strconvIntSize() == 32 && limits.MaxIndexBytes > 32<<20 {
		t.Fatalf("32-bit index limit = %d", limits.MaxIndexBytes)
	}
}
