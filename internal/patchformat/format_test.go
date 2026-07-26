package patchformat

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
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
		HashAlgorithm: "sha256",
		Compression:   Compression{Algorithm: "zstd", Library: "1.5.7", Mode: "patch-from", Level: 3},
		Files: []FileEntry{{
			Path:          "bin/game.exe",
			SourceHash:    strings.Repeat("a", 64),
			TargetHash:    strings.Repeat("b", 64),
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

func TestLegacyTargetHintCompatibility(t *testing.T) {
	header := validHeader()
	payload, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"targetHint"`)) {
		t.Fatal("current headers must not write targetHint")
	}

	payload = bytes.Replace(payload, []byte(`"path":"bin/game.exe"`), []byte(`"path":"bin/game.exe","targetHint":"renamed.exe"`), 1)
	var buffer bytes.Buffer
	buffer.Write(Magic[:])
	if err := binary.Write(&buffer, binary.LittleEndian, uint64(len(payload))); err != nil {
		t.Fatal(err)
	}
	buffer.Write(payload)
	parsed, err := Decode(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Files[0].LegacyTargetHint != "renamed.exe" {
		t.Fatalf("legacy target hint = %q", parsed.Header.Files[0].LegacyTargetHint)
	}
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
		{"hash", func(header *Header) { header.HashAlgorithm = "md5" }},
		{"compression", func(header *Header) { header.Compression.Mode = "other" }},
		{"zstd version", func(header *Header) { header.Compression.Library = "1.5.6" }},
		{"empty files", func(header *Header) { header.Files = nil }},
		{"empty path", func(header *Header) { header.Files[0].Path = "" }},
		{"bad digest", func(header *Header) { header.Files[0].SourceHash = "bad" }},
		{"special source mode", func(header *Header) { header.Files[0].SourceMode = 0o4755 }},
		{"special target mode", func(header *Header) { header.Files[0].TargetMode = 0o1000 }},
		{"unknown mode bit", func(header *Header) { header.Files[0].TargetMode = 1 << 31 }},
		{"non-hex digest", func(header *Header) { header.Files[0].SourceHash = strings.Repeat("z", 64) }},
		{"uppercase digest", func(header *Header) { header.Files[0].SourceHash = strings.Repeat("A", 64) }},
		{"traversal path", func(header *Header) { header.Files[0].Path = "../game.exe" }},
		{"backslash path", func(header *Header) { header.Files[0].Path = `bin\game.exe` }},
		{"non-canonical path", func(header *Header) { header.Files[0].Path = "bin/../game.exe" }},
		{"reserved device path", func(header *Header) { header.Files[0].Path = "bin/CON.txt" }},
		{"invalid Windows character", func(header *Header) { header.Files[0].Path = "bin/game?.exe" }},
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

func TestRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.vipr")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodePrefix(file, validHeader()); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	parsed, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Comment != "hello" {
		t.Fatalf("comment = %q", parsed.Header.Comment)
	}
}

func TestReadMissing(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "missing.vipr")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	header := validHeader()
	payload := []byte(`{"formatVersion":1} {}`)
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
