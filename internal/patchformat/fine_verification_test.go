package patchformat

import (
	"testing"
	"time"
)

func fineVerificationTestHeader() Header {
	return Header{
		FormatVersion:     FormatVersion,
		CreatedAt:         time.Unix(1, 0).UTC(),
		HashAlgorithm:     HashBLAKE3Tree,
		Compression:       Compression{Algorithm: AlgorithmHybrid, Library: SupportedZstdVersion, Mode: CompressionHybrid},
		DefaultWindowSize: 1 << 20,
		FineVerification:  true,
		Files: []FileEntry{{
			Path:                "file.bin",
			SourceSize:          1 << 20,
			WindowSize:          1 << 20,
			SourceChunkSize:     uint32(IdentityChunkSize),
			SourceChunks:        []Digest{{}},
			SourceFineChunkSize: 256 << 10,
			SourceFineChunks: []FineDigest{
				{Index: 0, Digest: Digest{1}},
				{Index: 3, Digest: Digest{2}},
			},
		}},
	}
}

func TestFineVerificationIndexRoundTrip(t *testing.T) {
	header := fineVerificationTestHeader()
	encoded, err := EncodeIndex(header)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeIndex(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.FineVerification {
		t.Fatal("fine verification flag was not preserved")
	}
	values := decoded.Files[0].SourceFineChunks
	if len(values) != 2 || values[0].Index != 0 || values[1].Index != 3 ||
		values[0].Digest != (Digest{1}) || values[1].Digest != (Digest{2}) {
		t.Fatalf("unexpected fine verification values: %#v", values)
	}
}

func TestFineVerificationRejectsUnsortedIndexes(t *testing.T) {
	header := fineVerificationTestHeader()
	header.Files[0].SourceFineChunks[1].Index = 0
	if _, err := EncodeIndex(header); err == nil {
		t.Fatal("duplicate fine digest indexes unexpectedly accepted")
	}
}

func TestFineVerificationRequiresFeatureFlag(t *testing.T) {
	header := fineVerificationTestHeader()
	header.FineVerification = false
	if err := ValidateHeader(header); err == nil {
		t.Fatal("fine digests without feature flag unexpectedly accepted")
	}
}

func TestLegacyIndexDoesNotGainFineExtension(t *testing.T) {
	header := fineVerificationTestHeader()
	header.FineVerification = false
	header.Files[0].SourceFineChunkSize = 0
	header.Files[0].SourceFineChunks = nil
	encoded, err := EncodeIndex(header)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeIndex(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.FineVerification || entryHasFineVerification(decoded.Files[0]) {
		t.Fatal("legacy index unexpectedly contains fine verification")
	}
}
