package patchformat

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"strings"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
)

var Magic = [8]byte{'V', 'I', 'P', 'R', '\r', '\n', 0x1a, 0x01}

const (
	FormatVersion        = 1
	MaxHeaderSize        = 64 << 20
	SupportedZstdVersion = "1.5.7"
)

// Header describes a VIPR patch. Blob offsets are relative to DataOffset.
type Header struct {
	FormatVersion int         `json:"formatVersion"`
	CreatedAt     time.Time   `json:"createdAt"`
	Creator       CreatorInfo `json:"creator"`
	Comment       string      `json:"comment"`
	HashAlgorithm string      `json:"hashAlgorithm"`
	Compression   Compression `json:"compression"`
	Reverse       bool        `json:"reverse"`
	Files         []FileEntry `json:"files"`
}

type CreatorInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"buildDate,omitempty"`
}

type Compression struct {
	Algorithm string `json:"algorithm"`
	Library   string `json:"library"`
	Mode      string `json:"mode"`
	Level     int    `json:"level"`
}

type FileEntry struct {
	Path          string `json:"path"`
	SourceHash    string `json:"sourceHash"`
	TargetHash    string `json:"targetHash"`
	SourceSize    uint64 `json:"sourceSize"`
	TargetSize    uint64 `json:"targetSize"`
	SourceMode    uint32 `json:"sourceMode"`
	TargetMode    uint32 `json:"targetMode"`
	ForwardOffset uint64 `json:"forwardOffset"`
	ForwardLength uint64 `json:"forwardLength"`
	ReverseOffset uint64 `json:"reverseOffset,omitempty"`
	ReverseLength uint64 `json:"reverseLength,omitempty"`
}

// ignoredFields lists legacy JSON fields that remain accepted for backward
// compatibility but are intentionally discarded instead of being retained in
// the parsed patch model.
type ignoredFields struct {
	TargetHint ignoredJSONValue `json:"targetHint"`
}

type ignoredJSONValue struct{}

// UnmarshalJSON consumes a legacy JSON value without retaining a decoded
// representation in the patch model. The complete header payload is already
// bounded and held by Decode.
func (*ignoredJSONValue) UnmarshalJSON([]byte) error { return nil }

// Patch provides parsed metadata and the absolute offset of the data section.
type Patch struct {
	Header     Header
	DataOffset uint64
}

// EncodePrefix validates and writes magic, header length, and JSON header.
func EncodePrefix(writer io.Writer, header Header) (uint64, error) {
	if err := ValidateHeader(header); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(header)
	if err != nil {
		return 0, fmt.Errorf("encode patch header: %w", err)
	}
	if len(payload) > MaxHeaderSize {
		return 0, fmt.Errorf("patch header exceeds %d bytes", MaxHeaderSize)
	}
	if _, err := writer.Write(Magic[:]); err != nil {
		return 0, err
	}
	if err := binary.Write(writer, binary.LittleEndian, uint64(len(payload))); err != nil {
		return 0, err
	}
	if _, err := writer.Write(payload); err != nil {
		return 0, err
	}
	return uint64(len(Magic) + 8 + len(payload)), nil
}

// Read opens and validates a VIPR patch header.
func Read(path string) (Patch, error) {
	file, err := os.Open(path)
	if err != nil {
		return Patch{}, fmt.Errorf("open patch: %w", err)
	}
	defer file.Close()
	return Decode(file)
}

// Decode parses a VIPR patch from reader.
func Decode(reader io.Reader) (Patch, error) {
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return Patch{}, fmt.Errorf("read patch magic: %w", err)
	}
	if !bytes.Equal(magic[:], Magic[:]) {
		return Patch{}, fmt.Errorf("not a VIPR patch")
	}
	var headerLength uint64
	if err := binary.Read(reader, binary.LittleEndian, &headerLength); err != nil {
		return Patch{}, fmt.Errorf("read patch header length: %w", err)
	}
	if headerLength == 0 || headerLength > MaxHeaderSize {
		return Patch{}, fmt.Errorf("invalid patch header length %d", headerLength)
	}
	payload := make([]byte, headerLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Patch{}, fmt.Errorf("read patch header: %w", err)
	}
	header, err := decodeHeader(payload)
	if err != nil {
		return Patch{}, err
	}
	if err := ValidateHeader(header); err != nil {
		return Patch{}, err
	}
	return Patch{Header: header, DataOffset: uint64(len(Magic)) + 8 + headerLength}, nil
}

func decodeHeader(payload []byte) (Header, error) {
	type headerAlias Header
	type fileEntryJSON struct {
		FileEntry
		ignoredFields
	}
	decoded := struct {
		headerAlias
		Files []fileEntryJSON `json:"files"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Header{}, fmt.Errorf("decode patch header: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Header{}, fmt.Errorf("patch header contains trailing JSON values")
		}
		return Header{}, fmt.Errorf("decode trailing patch header data: %w", err)
	}
	header := Header(decoded.headerAlias)
	header.Files = make([]FileEntry, len(decoded.Files))
	for index, entry := range decoded.Files {
		header.Files[index] = entry.FileEntry
	}
	return header, nil
}

// ValidateHeader validates format-level invariants and blob metadata.
func ValidateHeader(header Header) error {
	if header.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported VIPR format version %d", header.FormatVersion)
	}
	if header.HashAlgorithm != "sha256" {
		return fmt.Errorf("unsupported hash algorithm %q", header.HashAlgorithm)
	}
	if header.Compression.Algorithm != "zstd" || header.Compression.Mode != "patch-from" {
		return fmt.Errorf("unsupported compression metadata")
	}
	if header.Compression.Library != SupportedZstdVersion {
		return fmt.Errorf("unsupported libzstd version %q", header.Compression.Library)
	}
	if len(header.Files) == 0 {
		return fmt.Errorf("patch contains no files")
	}

	seen := make(map[string]struct{}, len(header.Files))
	for index, entry := range header.Files {
		if err := ValidatePath(entry.Path); err != nil {
			return fmt.Errorf("file entry %d: %w", index, err)
		}
		// VIPR archives are intended to be portable. Detect collisions using a
		// case-insensitive key so an archive cannot address two files that become
		// the same path on Windows or a case-insensitive macOS volume.
		key := pathutil.CaseInsensitiveKey(entry.Path)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate, Unicode-equivalent, or case-colliding patch path %q", entry.Path)
		}
		seen[key] = struct{}{}
		if !validSHA256(entry.SourceHash) || !validSHA256(entry.TargetHash) {
			return fmt.Errorf("file entry %q has an invalid SHA-256 digest", entry.Path)
		}
		if err := validatePortableMode(entry.SourceMode); err != nil {
			return fmt.Errorf("file entry %q has an invalid source mode: %w", entry.Path, err)
		}
		if err := validatePortableMode(entry.TargetMode); err != nil {
			return fmt.Errorf("file entry %q has an invalid target mode: %w", entry.Path, err)
		}
		if entry.ForwardLength == 0 {
			return fmt.Errorf("file entry %q has no forward differential", entry.Path)
		}
		if header.Reverse && entry.ReverseLength == 0 {
			return fmt.Errorf("file entry %q has no reverse differential", entry.Path)
		}
		if !header.Reverse && (entry.ReverseLength != 0 || entry.ReverseOffset != 0) {
			return fmt.Errorf("file entry %q unexpectedly contains reverse data", entry.Path)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ValidatePath validates one canonical, portable path stored in a VIPR patch.
func ValidatePath(value string) error {
	if value == "" || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("patch path is empty or contains NUL")
	}
	if strings.Contains(value, "\\") || strings.Contains(value, ":") {
		return fmt.Errorf("patch path %q is not portable", value)
	}
	if pathpkg.IsAbs(value) || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("unsafe patch path %q", value)
	}
	if clean := pathpkg.Clean(value); clean != value {
		return fmt.Errorf("patch path %q is not canonical", value)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") ||
			strings.IndexFunc(component, func(character rune) bool {
				return character < 32 || strings.ContainsRune(`<>"|?*`, character)
			}) >= 0 || isWindowsDeviceName(component) {
			return fmt.Errorf("patch path %q contains a non-portable component", value)
		}
	}
	return nil
}

func isWindowsDeviceName(component string) bool {
	base := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

func validatePortableMode(mode uint32) error {
	if mode&^uint32(0o777) != 0 {
		return fmt.Errorf("unsupported file mode %#o", mode)
	}
	return nil
}
