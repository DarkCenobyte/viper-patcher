package patchformat

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestV4IndexRoundTrip(t *testing.T) {
	header := validTestHeader()
	index, err := EncodeIndex(header)
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest{9}
	file := buildTestContainer(t, header.Reverse, index, digest, 0)
	parsed, err := DecodeAt(bytes.NewReader(file), uint64(len(file)), func([]byte, Digest) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Files[0].Path != "a.bin" || parsed.Header.FormatVersion != 4 {
		t.Fatalf("unexpected round trip: %+v", parsed.Header)
	}
}

func TestV4RejectsContainerFlagDisagreement(t *testing.T) {
	header := validTestHeader()
	index, err := EncodeIndex(header)
	if err != nil {
		t.Fatal(err)
	}
	file := buildTestContainer(t, false, index, Digest{9}, 1)
	if _, err := DecodeAt(bytes.NewReader(file), uint64(len(file)), nil); err == nil {
		t.Fatal("expected inconsistent prefix, footer, and index flags to be rejected")
	}
}

func TestV4RejectsOversizedFileCountBeforeAllocation(t *testing.T) {
	index, err := EncodeIndex(validTestHeader())
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(index[36:40], uint32(MaxFileEntries+1))
	file := buildTestContainer(t, false, index, Digest{9}, 0)
	if _, err := DecodeAt(bytes.NewReader(file), uint64(len(file)), nil); err == nil {
		t.Fatal("expected oversized file count to be rejected")
	}
}

func TestV4RejectsExpandedInstructionBomb(t *testing.T) {
	header := validTestHeader()
	window := &header.Files[0].ForwardWindows[0]
	window.Kind = WindowDeltaRaw
	window.Codec = CodecNone
	window.PayloadSize = 1<<20 + 3
	window.ExpandedSize = window.PayloadSize
	window.SourceOffset = 0
	window.SourceSize = 1
	window.SourceFirstChunk = 0
	window.SourceChunkCount = 1
	window.InstructionCount = 1
	if _, err := EncodeIndex(header); err == nil {
		t.Fatal("expected oversized expanded instruction stream to be rejected")
	}
}

func validTestHeader() Header {
	source := Digest{1}
	target := Digest{2}
	return Header{
		FormatVersion:     FormatVersion,
		CreatedAt:         time.Unix(1, 2).UTC(),
		Creator:           CreatorInfo{Name: "test", Version: "1"},
		HashAlgorithm:     HashBLAKE3Tree,
		Compression:       Compression{Algorithm: AlgorithmHybrid, Library: "1.5.7", Mode: CompressionHybrid, Level: 3},
		DefaultWindowSize: 256 << 10,
		Files: []FileEntry{{
			Path:            "a.bin",
			SourceHash:      source.Hex(),
			TargetHash:      target.Hex(),
			SourceSize:      1,
			TargetSize:      1,
			SourceDigest:    source,
			TargetDigest:    target,
			WindowSize:      256 << 10,
			SourceChunkSize: uint32(IdentityChunkSize),
			SourceChunks:    []Digest{{3}},
			TargetChunks:    []Digest{{4}},
			ForwardWindows: []WindowDescriptor{{
				OutputSize:    1,
				Kind:          WindowReplaceRaw,
				Codec:         CodecNone,
				PayloadOffset: PrefixSize,
				PayloadSize:   1,
				ExpandedSize:  1,
				Digest:        Digest{4},
			}},
		}},
	}
}

func buildTestContainer(t *testing.T, reverse bool, index []byte, digest Digest, footerFlags uint32) []byte {
	t.Helper()
	var file bytes.Buffer
	var prefixFlags uint32
	if reverse {
		prefixFlags = 1
	}
	if err := WritePrefix(&file, prefixFlags); err != nil {
		t.Fatal(err)
	}
	file.WriteByte(7)
	offset := uint64(file.Len())
	file.Write(index)
	if err := WriteFooter(&file, offset, uint64(len(index)), digest, footerFlags); err != nil {
		t.Fatal(err)
	}
	return file.Bytes()
}

func FuzzDecodeV4(f *testing.F) {
	f.Add([]byte("VIPR4\r\n\x1a"))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeAt(bytes.NewReader(data), uint64(len(data)), nil) })
}

func TestMarshalWindowDescriptorsUsesCanonicalLayout(t *testing.T) {
	window := WindowDescriptor{
		OutputOffset:     0x0102030405060708,
		OutputSize:       0x11121314,
		Kind:             WindowDeltaZstd,
		Codec:            CodecZstd,
		Flags:            0x1516,
		PayloadOffset:    0x1718191a1b1c1d1e,
		PayloadSize:      0x21222324,
		ExpandedSize:     0x25262728,
		SourceOffset:     0x3132333435363738,
		SourceSize:       0x41424344,
		SourceFirstChunk: 0x45464748,
		SourceChunkCount: 0x5152,
		InstructionCount: 0x5354,
	}
	for index := range window.Digest {
		window.Digest[index] = byte(index + 1)
	}
	encoded := MarshalWindowDescriptors([]WindowDescriptor{window})
	if len(encoded) != WindowDescriptorSize {
		t.Fatalf("encoded descriptor size = %d, want %d", len(encoded), WindowDescriptorSize)
	}
	if got := binary.LittleEndian.Uint64(encoded[0:8]); got != window.OutputOffset {
		t.Fatalf("output offset = %#x, want %#x", got, window.OutputOffset)
	}
	if got := binary.LittleEndian.Uint64(encoded[16:24]); got != window.PayloadOffset {
		t.Fatalf("payload offset = %#x, want %#x", got, window.PayloadOffset)
	}
	if got := binary.LittleEndian.Uint64(encoded[32:40]); got != window.SourceOffset {
		t.Fatalf("source offset = %#x, want %#x", got, window.SourceOffset)
	}
	if !bytes.Equal(encoded[52:84], window.Digest[:]) {
		t.Fatal("encoded descriptor digest mismatch")
	}
	if !bytes.Equal(encoded[84:88], []byte{0, 0, 0, 0}) {
		t.Fatal("encoded descriptor reserved field is not zero")
	}
}
