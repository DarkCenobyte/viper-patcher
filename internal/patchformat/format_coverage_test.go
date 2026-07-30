package patchformat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func cloneV4CoverageHeader(header Header) Header {
	result := header
	result.Files = make([]FileEntry, len(header.Files))
	for index := range header.Files {
		entry := header.Files[index]
		entry.SourceChunks = append([]Digest(nil), entry.SourceChunks...)
		entry.TargetChunks = append([]Digest(nil), entry.TargetChunks...)
		entry.ForwardWindows = append([]WindowDescriptor(nil), entry.ForwardWindows...)
		entry.ReverseWindows = append([]WindowDescriptor(nil), entry.ReverseWindows...)
		result.Files[index] = entry
	}
	return result
}

func TestV4DigestAndOptimizationHelpers(t *testing.T) {
	digest := Digest{1, 2, 3, 4}
	parsed, err := ParseDigest(digest.Hex())
	if err != nil || parsed != digest {
		t.Fatalf("ParseDigest = %x, %v", parsed, err)
	}
	for _, invalid := range []string{"", "00", strings.Repeat("g", 64), strings.Repeat("0", 66)} {
		if _, err := ParseDigest(invalid); err == nil {
			t.Fatalf("ParseDigest(%q) unexpectedly succeeded", invalid)
		}
	}

	for _, test := range []struct {
		input string
		mode  OptimizationMode
		text  string
	}{
		{"", OptimizeBalanced, "balanced"},
		{" balanced ", OptimizeBalanced, "balanced"},
		{"speed", OptimizeApplySpeed, "apply-speed"},
		{"APPLY-SPEED", OptimizeApplySpeed, "apply-speed"},
		{"size", OptimizePatchSize, "patch-size"},
		{"patch-size", OptimizePatchSize, "patch-size"},
	} {
		mode, err := ParseOptimizationMode(test.input)
		if err != nil || mode != test.mode || mode.String() != test.text {
			t.Fatalf("ParseOptimizationMode(%q) = %v, %v", test.input, mode, err)
		}
	}
	if _, err := ParseOptimizationMode("other"); err == nil {
		t.Fatal("unsupported optimization mode was accepted")
	}
	if got := OptimizationMode(255).String(); got != "unknown-255" {
		t.Fatalf("unknown mode string = %q", got)
	}
}

func TestV4NormalizeAndValidateHeader(t *testing.T) {
	header := validTestHeader()
	header.FormatVersion = 0
	header.CreatedAt = time.Time{}
	header.HashAlgorithm = ""
	header.Compression.Algorithm = ""
	header.Compression.Mode = ""
	header.Files[0].SourceDigest = Digest{}
	header.Files[0].TargetDigest = Digest{}
	header.Files[0].WindowSize = 0
	header.Files[0].SourceChunkSize = 0
	normalizeHeader(&header)
	if header.FormatVersion != FormatVersion || header.CreatedAt.IsZero() || header.HashAlgorithm != HashBLAKE3Tree || header.Compression.Algorithm != AlgorithmHybrid || header.Compression.Mode != CompressionHybrid {
		t.Fatalf("normalization failed: %+v", header)
	}
	if header.Files[0].SourceDigest == (Digest{}) || header.Files[0].TargetDigest == (Digest{}) || header.Files[0].WindowSize != header.DefaultWindowSize || header.Files[0].SourceChunkSize != uint32(IdentityChunkSize) {
		t.Fatalf("file normalization failed: %+v", header.Files[0])
	}
	if err := ValidateHeader(header); err != nil {
		t.Fatal(err)
	}

	base := validTestHeader()
	mutations := []struct {
		name   string
		mutate func(*Header)
	}{
		{"format", func(h *Header) { h.FormatVersion = 3 }},
		{"hash", func(h *Header) { h.HashAlgorithm = "sha256" }},
		{"algorithm", func(h *Header) { h.Compression.Algorithm = "zstd" }},
		{"mode", func(h *Header) { h.Compression.Mode = "stream" }},
		{"library", func(h *Header) { h.Compression.Library = "1.5.5" }},
		{"level-low", func(h *Header) { h.Compression.Level = -131073 }},
		{"level-high", func(h *Header) { h.Compression.Level = 23 }},
		{"optimization", func(h *Header) { h.Optimization = 255 }},
		{"window", func(h *Header) { h.DefaultWindowSize = 3 << 20 }},
		{"comment-invalid-utf8", func(h *Header) { h.Comment = string([]byte{0xff}) }},
		{"no-files", func(h *Header) { h.Files = nil }},
		{"path", func(h *Header) { h.Files[0].Path = "../escape" }},
		{"duplicate-path", func(h *Header) {
			duplicate := h.Files[0]
			duplicate.Path = "A.BIN"
			h.Files = append(h.Files, duplicate)
		}},
		{"entry-window", func(h *Header) { h.Files[0].WindowSize = 3 << 20 }},
		{"chunk-size", func(h *Header) { h.Files[0].SourceChunkSize = 1 }},
		{"source-range", func(h *Header) { h.Files[0].SourceSize = math.MaxInt64 + 1 }},
		{"target-range", func(h *Header) { h.Files[0].TargetSize = math.MaxInt64 + 1 }},
		{"source-hash", func(h *Header) { h.Files[0].SourceHash = strings.Repeat("0", 64) }},
		{"target-hash", func(h *Header) { h.Files[0].TargetHash = strings.Repeat("0", 64) }},
		{"digest-table", func(h *Header) { h.Files[0].SourceChunks = nil }},
		{"forward", func(h *Header) { h.Files[0].ForwardWindows = nil }},
		{"unexpected-reverse", func(h *Header) {
			h.Files[0].ReverseWindows = append([]WindowDescriptor(nil), h.Files[0].ForwardWindows...)
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			header := cloneV4CoverageHeader(base)
			test.mutate(&header)
			if err := ValidateHeader(header); err == nil {
				t.Fatal("invalid header was accepted")
			}
		})
	}

	reverse := cloneV4CoverageHeader(base)
	reverse.Reverse = true
	reverse.Files[0].ReverseWindows = []WindowDescriptor{{
		OutputSize:    1,
		Kind:          WindowReplaceRaw,
		Codec:         CodecNone,
		PayloadOffset: PrefixSize + 1,
		PayloadSize:   1,
		ExpandedSize:  1,
		Digest:        Digest{3},
	}}
	if err := ValidateHeader(reverse); err != nil {
		t.Fatalf("valid reverse header: %v", err)
	}
	reverse.Files[0].ReverseWindows[0].OutputOffset = 1
	if err := ValidateHeader(reverse); err == nil {
		t.Fatal("invalid reverse windows were accepted")
	}
}

func TestV4PathWindowSizeAndChunkHelpers(t *testing.T) {
	for _, path := range []string{"a.bin", "nested/data.bin", "é/文件.bin"} {
		if err := ValidatePath(path); err != nil {
			t.Fatalf("ValidatePath(%q): %v", path, err)
		}
	}
	invalidUTF8 := string([]byte{0xff})
	if utf8.ValidString(invalidUTF8) {
		t.Fatal("invalid UTF-8 fixture is valid")
	}
	for _, path := range []string{"", ".", "..", "../a", "/a", "a\\b", "a//b", "a/./b", "a/../b", "a\x00b", "a ", "a.", "C:drive", invalidUTF8} {
		if err := ValidatePath(path); err == nil {
			t.Fatalf("ValidatePath(%q) unexpectedly succeeded", path)
		}
	}
	for _, size := range []uint32{256 << 10, 512 << 10, 1 << 20, 2 << 20, 4 << 20, 8 << 20} {
		if !validWindowSize(size) {
			t.Fatalf("valid window size %d was rejected", size)
		}
	}
	if validWindowSize(0) || validWindowSize(3<<20) {
		t.Fatal("invalid window size was accepted")
	}
	if chunkCount(0) != 0 || chunkCount(1) != 1 || chunkCount(IdentityChunkSize) != 1 || chunkCount(IdentityChunkSize+1) != 2 {
		t.Fatal("chunkCount boundary calculation is incorrect")
	}
}

func validCoverageSourceWindow(kind WindowKind) WindowDescriptor {
	window := WindowDescriptor{
		OutputOffset:     0,
		OutputSize:       16,
		Kind:             kind,
		Codec:            CodecNone,
		SourceOffset:     8,
		SourceSize:       16,
		SourceFirstChunk: 0,
		SourceChunkCount: 1,
		Digest:           Digest{9},
	}
	switch kind {
	case WindowSame:
		window.SourceOffset = 0
	case WindowDeltaRaw:
		window.PayloadOffset = PrefixSize
		window.PayloadSize = 8
		window.ExpandedSize = 8
		window.InstructionCount = 1
	case WindowDeltaZstd:
		window.Codec = CodecZstd
		window.PayloadOffset = PrefixSize
		window.PayloadSize = 4
		window.ExpandedSize = 8
		window.InstructionCount = 1
	}
	return window
}

func TestV4ValidateEveryWindowKind(t *testing.T) {
	valid := []WindowDescriptor{
		validCoverageSourceWindow(WindowSame),
		validCoverageSourceWindow(WindowCopy),
		validCoverageSourceWindow(WindowDeltaRaw),
		validCoverageSourceWindow(WindowDeltaZstd),
		{OutputSize: 16, Kind: WindowReplaceRaw, Codec: CodecNone, PayloadOffset: PrefixSize, PayloadSize: 16, ExpandedSize: 16},
		{OutputSize: 16, Kind: WindowReplaceZstd, Codec: CodecZstd, PayloadOffset: PrefixSize, PayloadSize: 4, ExpandedSize: 16},
		{OutputSize: 16, Kind: WindowZero, Codec: CodecNone, ExpandedSize: 16},
		{OutputSize: 16, Kind: WindowRun, Codec: CodecNone, PayloadOffset: PrefixSize, PayloadSize: 1, ExpandedSize: 16},
	}
	for _, window := range valid {
		if err := validateWindow(window, 64); err != nil {
			t.Fatalf("kind %d rejected: %v", window.Kind, err)
		}
	}

	invalid := []WindowDescriptor{
		func() WindowDescriptor { w := valid[0]; w.Flags = 1; return w }(),
		func() WindowDescriptor { w := valid[0]; w.SourceOffset = 1; return w }(),
		func() WindowDescriptor { w := valid[1]; w.SourceSize = 15; return w }(),
		func() WindowDescriptor { w := valid[2]; w.PayloadSize = 0; return w }(),
		func() WindowDescriptor { w := valid[3]; w.Codec = CodecNone; return w }(),
		func() WindowDescriptor { w := valid[4]; w.PayloadSize = 15; return w }(),
		func() WindowDescriptor { w := valid[5]; w.ExpandedSize = 15; return w }(),
		func() WindowDescriptor { w := valid[6]; w.SourceOffset = 1; return w }(),
		func() WindowDescriptor { w := valid[7]; w.PayloadSize = 2; return w }(),
		{OutputSize: 1, Kind: WindowKind(255)},
		func() WindowDescriptor { w := valid[1]; w.SourceOffset = 60; return w }(),
		func() WindowDescriptor { w := valid[1]; w.SourceFirstChunk = 1; return w }(),
	}
	for index, window := range invalid {
		if err := validateWindow(window, 64); err == nil {
			t.Fatalf("invalid window %d (%+v) was accepted", index, window)
		}
	}
}

func TestV4ValidateWindowSetsAndPayloadLayout(t *testing.T) {
	window := WindowDescriptor{OutputSize: 1, Kind: WindowReplaceRaw, Codec: CodecNone, PayloadOffset: PrefixSize, PayloadSize: 1, ExpandedSize: 1}
	if err := validateWindowSet(nil, 0, 0, 256<<10, true); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		windows    []WindowDescriptor
		outputSize uint64
		required   bool
	}{
		{[]WindowDescriptor{window}, 0, true},
		{nil, 1, true},
		{[]WindowDescriptor{{OutputOffset: 1, OutputSize: 1, Kind: WindowZero, ExpandedSize: 1}}, 1, true},
		{[]WindowDescriptor{{OutputSize: 0, Kind: WindowZero}}, 1, true},
		{[]WindowDescriptor{{OutputSize: 2, Kind: WindowZero, ExpandedSize: 2}}, 1, true},
		{[]WindowDescriptor{window}, 2, true},
	} {
		if err := validateWindowSet(test.windows, test.outputSize, 0, 256<<10, test.required); err == nil {
			t.Fatalf("invalid window set %+v was accepted", test)
		}
	}

	zeroHeader := validTestHeader()
	zeroHeader.Files[0].SourceSize = 0
	zeroHeader.Files[0].TargetSize = 0
	zeroHeader.Files[0].SourceChunks = nil
	zeroHeader.Files[0].TargetChunks = nil
	zeroHeader.Files[0].ForwardWindows = nil
	if err := ValidatePatch(Patch{Header: zeroHeader, IndexOffset: PrefixSize}); err != nil {
		t.Fatalf("zero-payload patch: %v", err)
	}
	if err := ValidatePatch(Patch{Header: zeroHeader, IndexOffset: PrefixSize + 1}); err == nil {
		t.Fatal("unreferenced zero-payload byte was accepted")
	}

	header := validTestHeader()
	header.Files[0].SourceSize = 0
	header.Files[0].SourceChunks = nil
	header.Files[0].TargetSize = 2
	header.Files[0].TargetChunks = []Digest{{4}}
	header.Files[0].ForwardWindows = []WindowDescriptor{
		{OutputSize: 1, Kind: WindowReplaceRaw, Codec: CodecNone, PayloadOffset: PrefixSize, PayloadSize: 1, ExpandedSize: 1},
		{OutputOffset: 1, OutputSize: 1, Kind: WindowReplaceRaw, Codec: CodecNone, PayloadOffset: PrefixSize + 1, PayloadSize: 1, ExpandedSize: 1},
	}
	validPatch := Patch{Header: header, IndexOffset: PrefixSize + 2}
	if err := ValidatePatch(validPatch); err != nil {
		t.Fatal(err)
	}
	cases := []func(*Patch){
		func(p *Patch) { p.Header.Files[0].ForwardWindows[0].PayloadOffset++ },
		func(p *Patch) { p.Header.Files[0].ForwardWindows[1].PayloadOffset = PrefixSize },
		func(p *Patch) { p.IndexOffset++ },
		func(p *Patch) { p.Header.Files[0].ForwardWindows[1].PayloadOffset = p.IndexOffset },
	}
	for index, mutate := range cases {
		patch := validPatch
		patch.Header = cloneV4CoverageHeader(header)
		mutate(&patch)
		if err := ValidatePatch(patch); err == nil {
			t.Fatalf("invalid payload layout %d was accepted", index)
		}
	}
}

type v4CoverageFailWriter struct{ err error }

func (writer v4CoverageFailWriter) Write([]byte) (int, error) { return 0, writer.err }

type v4CoverageFailReader struct{ err error }

func (reader v4CoverageFailReader) Read([]byte) (int, error) { return 0, reader.err }

func TestV4ContainerDecodeFailuresAndReaderHelpers(t *testing.T) {
	header := validTestHeader()
	index, err := EncodeIndex(header)
	if err != nil {
		t.Fatal(err)
	}
	container := buildTestContainer(t, false, index, Digest{9}, 0)
	if _, err := Decode(bytes.NewReader(container)); err != nil {
		t.Fatal(err)
	}
	verifierErr := errors.New("verifier")
	if _, err := DecodeAt(bytes.NewReader(container), uint64(len(container)), func([]byte, Digest) error { return verifierErr }); !errors.Is(err, verifierErr) {
		t.Fatalf("verifier error = %v", err)
	}
	if _, err := DecodeAt(nil, 0, nil); err == nil {
		t.Fatal("nil ReaderAt was accepted")
	}
	if _, err := Decode(v4CoverageFailReader{verifierErr}); !errors.Is(err, verifierErr) {
		t.Fatalf("reader error = %v", err)
	}
	if err := WritePrefix(v4CoverageFailWriter{verifierErr}, 0); !errors.Is(err, verifierErr) {
		t.Fatalf("prefix writer error = %v", err)
	}
	if err := WriteFooter(v4CoverageFailWriter{verifierErr}, 0, 0, Digest{}, 0); !errors.Is(err, verifierErr) {
		t.Fatalf("footer writer error = %v", err)
	}

	mutations := []struct {
		name   string
		mutate func([]byte)
	}{
		{"prefix-magic", func(data []byte) { data[0] ^= 0xff }},
		{"prefix-flags", func(data []byte) { binary.LittleEndian.PutUint32(data[8:12], 2) }},
		{"prefix-reserved", func(data []byte) { data[12] = 1 }},
		{"footer-magic", func(data []byte) { data[len(data)-FooterSize] ^= 0xff }},
		{"footer-flags", func(data []byte) {
			binary.LittleEndian.PutUint32(data[len(data)-FooterSize+56:len(data)-FooterSize+60], 2)
		}},
		{"footer-reserved", func(data []byte) { data[len(data)-FooterSize+60] = 1 }},
		{"index-before-data", func(data []byte) {
			binary.LittleEndian.PutUint64(data[len(data)-FooterSize+8:len(data)-FooterSize+16], PrefixSize-1)
		}},
		{"index-too-large", func(data []byte) {
			binary.LittleEndian.PutUint64(data[len(data)-FooterSize+16:len(data)-FooterSize+24], MaxIndexSize+1)
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), container...)
			test.mutate(data)
			if _, err := DecodeAt(bytes.NewReader(data), uint64(len(data)), nil); err == nil {
				t.Fatal("malformed container was accepted")
			}
		})
	}

	indexMutations := []struct {
		name   string
		mutate func([]byte)
	}{
		{"magic", func(data []byte) { data[0] ^= 0xff }},
		{"version", func(data []byte) { binary.LittleEndian.PutUint32(data[8:12], FormatVersion+1) }},
		{"flags", func(data []byte) { binary.LittleEndian.PutUint32(data[12:16], 2) }},
		{"reserved", func(data []byte) { data[29] = 1 }},
		{"window-reserved", func(data []byte) { data[len(data)-1] = 1 }},
	}
	for _, test := range indexMutations {
		t.Run("index-"+test.name, func(t *testing.T) {
			payload := append([]byte(nil), index...)
			test.mutate(payload)
			data := buildTestContainer(t, false, payload, Digest{9}, 0)
			if _, err := DecodeAt(bytes.NewReader(data), uint64(len(data)), nil); err == nil {
				t.Fatal("malformed index was accepted")
			}
		})
	}
	trailing := buildTestContainer(t, false, append(append([]byte(nil), index...), 0), Digest{9}, 0)
	if _, err := DecodeAt(bytes.NewReader(trailing), uint64(len(trailing)), nil); err == nil {
		t.Fatal("trailing index byte was accepted")
	}

	reader := newIndexReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	if reader.remaining() != 8 {
		t.Fatal("unexpected initial reader length")
	}
	if value, err := reader.u8(); err != nil || value != 1 {
		t.Fatalf("u8 = %d, %v", value, err)
	}
	if value, err := reader.u16(); err != nil || value != 0x0302 {
		t.Fatalf("u16 = %#x, %v", value, err)
	}
	if _, err := reader.u64(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("u64 short read = %v", err)
	}
	if _, err := reader.bytes(-1); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("negative read = %v", err)
	}

	var buffer bytes.Buffer
	writeI32(&buffer, -2)
	writeI64(&buffer, -3)
	if buffer.Len() != 12 {
		t.Fatalf("integer writer size = %d", buffer.Len())
	}
	if err := writeString16(&buffer, strings.Repeat("x", math.MaxUint16+1)); err == nil {
		t.Fatal("oversized string16 was accepted")
	}
	if err := writeString32(&buffer, "too-long", 2); err == nil {
		t.Fatal("oversized string32 was accepted")
	}
	if err := writeString16(v4CoverageFailWriter{verifierErr}, "x"); !errors.Is(err, verifierErr) {
		t.Fatalf("string16 writer error = %v", err)
	}
	if err := writeString32(v4CoverageFailWriter{verifierErr}, "x", 1); !errors.Is(err, verifierErr) {
		t.Fatalf("string32 writer error = %v", err)
	}
}
