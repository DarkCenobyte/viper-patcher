//go:build ignore

package patchformat

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validHeader() Header {
	return Header{
		FormatVersion: FormatVersion,
		CreatedAt:     time.Unix(1, 0).UTC(),
		Creator:       CreatorInfo{Name: "creator", Version: "test"},
		Comment:       "hello",
		HashAlgorithm: HashBLAKE3Tree,
		Compression:   Compression{Algorithm: AlgorithmHybrid, Library: SupportedZstdVersion, Mode: CompressionHybrid, Level: 3},
		Files: []FileEntry{{
			Path:          "bin/game.exe",
			SourceHash:    strings.Repeat("a", 64),
			TargetHash:    strings.Repeat("b", 64),
			ForwardMethod: MethodReplace,
			ForwardLength: 10,
		}},
	}
}

func TestEncodeDecode(t *testing.T) {
	var buffer bytes.Buffer
	offset, err := EncodePrefix(&buffer, validHeader())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Decode(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.DataOffset != offset || parsed.Header.Comment != "hello" {
		t.Fatalf("unexpected parsed patch: %#v", parsed)
	}
}

func TestLegacyTargetHintIsRejected(t *testing.T) {
	payload, err := json.Marshal(validHeader())
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.Replace(payload, []byte(`"path":"bin/game.exe"`), []byte(`"path":"bin/game.exe","targetHint":{"legacy":"renamed.exe"}`), 1)
	if _, err := Decode(encodedPayload(t, payload)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected targetHint rejection, got %v", err)
	}
}

func TestFileEntryStillRejectsUnknownFields(t *testing.T) {
	payload, err := json.Marshal(validHeader())
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.Replace(payload, []byte(`"path":"bin/game.exe"`), []byte(`"path":"bin/game.exe","unexpected":true`), 1)
	if _, err := Decode(encodedPayload(t, payload)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown file-entry field rejection, got %v", err)
	}
}

func encodedPayload(t *testing.T, payload []byte) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	buffer.Write(Magic[:])
	if err := binary.Write(&buffer, binary.LittleEndian, uint64(len(payload))); err != nil {
		t.Fatal(err)
	}
	buffer.Write(payload)
	return bytes.NewReader(buffer.Bytes())
}

func TestDecodeRejectsMagic(t *testing.T) {
	if _, err := Decode(bytes.NewReader([]byte("not-a-patch"))); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDecodeRejectsOversizedHeader(t *testing.T) {
	var buffer bytes.Buffer
	buffer.Write(Magic[:])
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(MaxHeaderSize+1))
	if _, err := Decode(&buffer); err == nil {
		t.Fatal("expected an error")
	}
}

func TestEncodeRejectsInvalidHeader(t *testing.T) {
	header := validHeader()
	header.Files = append(header.Files, header.Files[0])
	var buffer bytes.Buffer
	if _, err := EncodePrefix(&buffer, header); err == nil {
		t.Fatal("expected an error")
	}
	if buffer.Len() != 0 {
		t.Fatal("invalid header must not be written")
	}
}

func TestValidateHeaderFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Header)
	}{
		{"version", func(header *Header) { header.FormatVersion++ }},
		{"hash", func(header *Header) { header.HashAlgorithm = "sha256" }},
		{"compression algorithm", func(header *Header) { header.Compression.Algorithm = "zstd" }},
		{"compression mode", func(header *Header) { header.Compression.Mode = "patch-from" }},
		{"zstd version", func(header *Header) { header.Compression.Library = "1.5.6" }},
		{"empty files", func(header *Header) { header.Files = nil }},
		{"empty path", func(header *Header) { header.Files[0].Path = "" }},
		{"bad digest", func(header *Header) { header.Files[0].SourceHash = "bad" }},
		{"source size overflow", func(header *Header) { header.Files[0].SourceSize = maxSignedFileSize + 1 }},
		{"target size overflow", func(header *Header) { header.Files[0].TargetSize = maxSignedFileSize + 1 }},
		{"non-hex digest", func(header *Header) { header.Files[0].SourceHash = strings.Repeat("z", 64) }},
		{"uppercase digest", func(header *Header) { header.Files[0].SourceHash = strings.Repeat("A", 64) }},
		{"traversal path", func(header *Header) { header.Files[0].Path = "../game.exe" }},
		{"backslash path", func(header *Header) { header.Files[0].Path = `bin\game.exe` }},
		{"non-canonical path", func(header *Header) { header.Files[0].Path = "bin/../game.exe" }},
		{"reserved device path", func(header *Header) { header.Files[0].Path = "bin/CON.txt" }},
		{"invalid Windows character", func(header *Header) { header.Files[0].Path = "bin/game?.exe" }},
		{"missing method", func(header *Header) { header.Files[0].ForwardMethod = "" }},
		{"missing forward", func(header *Header) { header.Files[0].ForwardLength = 0 }},
		{"missing reverse", func(header *Header) { header.Reverse = true }},
		{"duplicate path", func(header *Header) { header.Files = append(header.Files, header.Files[0]) }},
		{"case collision", func(header *Header) {
			duplicate := header.Files[0]
			duplicate.Path = "BIN/GAME.EXE"
			header.Files = append(header.Files, duplicate)
		}},
		{"Unicode normalization collision", func(header *Header) {
			duplicate := header.Files[0]
			header.Files[0].Path = "data/é.txt"
			duplicate.Path = "data/e\u0301.txt"
			header.Files = append(header.Files, duplicate)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := validHeader()
			test.mutate(&header)
			if err := ValidateHeader(header); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	header := validHeader()
	payload := []byte(`{"formatVersion":3} {}`)
	var buffer bytes.Buffer
	buffer.Write(Magic[:])
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(len(payload)))
	buffer.Write(payload)
	if _, err := Decode(&buffer); err == nil {
		t.Fatalf("expected invalid trailing JSON to be rejected: %#v", header)
	}
}

func FuzzDecode(f *testing.F) {
	var valid bytes.Buffer
	if _, err := EncodePrefix(&valid, validHeader()); err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Bytes())
	f.Add([]byte("not a patch"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(bytes.NewReader(data))
	})
}
