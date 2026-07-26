package patchformat

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	pathpkg "path"
	"strings"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
)

var Magic = [8]byte{'V', 'I', 'P', 'R', '\r', '\n', 0x1a, 0x01}

const (
	LegacyFormatVersion  = 1
	FormatVersion        = 2
	MaxHeaderSize        = 64 << 20
	SupportedZstdVersion = "1.5.7"
	CompressionPatchFrom = "patch-from"
	CompressionHybridV2  = "hybrid-v2"
	AlgorithmZstd        = "zstd"
	AlgorithmHybrid      = "hybrid"
	MethodPatchFrom      = "zstd-patch-from"
	MethodCopyAdd        = "zstd-copy-add"
	MethodSparse         = "zstd-sparse"
	MethodReplace        = "zstd-replace"
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
	Path                  string `json:"path"`
	SourceHash            string `json:"sourceHash"`
	TargetHash            string `json:"targetHash"`
	SourceSize            uint64 `json:"sourceSize"`
	TargetSize            uint64 `json:"targetSize"`
	SourceMode            uint32 `json:"sourceMode"`
	TargetMode            uint32 `json:"targetMode"`
	ForwardMethod         string `json:"forwardMethod,omitempty"`
	ForwardOffset         uint64 `json:"forwardOffset"`
	ForwardLength         uint64 `json:"forwardLength"`
	ForwardExpandedLength uint64 `json:"forwardExpandedLength,omitempty"`
	ReverseMethod         string `json:"reverseMethod,omitempty"`
	ReverseOffset         uint64 `json:"reverseOffset,omitempty"`
	ReverseLength         uint64 `json:"reverseLength,omitempty"`
	ReverseExpandedLength uint64 `json:"reverseExpandedLength,omitempty"`
}

// ForwardDifferentialMethod returns the normalized forward method. Version 1
// entries and early version 2 entries without an explicit method remain
// compatible and use zstd patch-from.
func (entry FileEntry) ForwardDifferentialMethod() string {
	if entry.ForwardMethod == "" {
		return MethodPatchFrom
	}
	return entry.ForwardMethod
}

// ReverseDifferentialMethod returns the normalized reverse method.
func (entry FileEntry) ReverseDifferentialMethod() string {
	if entry.ReverseMethod == "" {
		return MethodPatchFrom
	}
	return entry.ReverseMethod
}

// ignoredFields lists legacy JSON fields that remain accepted for backward
// compatibility but are intentionally discarded instead of being retained in
// the parsed patch model.
type ignoredFields struct {
	TargetHint ignoredJSONValue `json:"targetHint"`
}

type ignoredJSONValue struct{}

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
	if header.FormatVersion != LegacyFormatVersion && header.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported VIPR format version %d", header.FormatVersion)
	}
	if header.HashAlgorithm != "sha256" {
		return fmt.Errorf("unsupported hash algorithm %q", header.HashAlgorithm)
	}
	if header.FormatVersion == LegacyFormatVersion && header.Compression.Algorithm != AlgorithmZstd {
		return fmt.Errorf("unsupported version 1 compression algorithm %q", header.Compression.Algorithm)
	}
	if header.FormatVersion == FormatVersion {
		switch header.Compression.Mode {
		case CompressionHybridV2:
			if header.Compression.Algorithm != AlgorithmHybrid {
				return fmt.Errorf("version 2 hybrid mode requires algorithm %q", AlgorithmHybrid)
			}
		case CompressionPatchFrom:
			if header.Compression.Algorithm != AlgorithmZstd {
				return fmt.Errorf("version 2 patch-from mode requires algorithm %q", AlgorithmZstd)
			}
		default:
			return fmt.Errorf("unsupported version 2 compression mode %q", header.Compression.Mode)
		}
	}
	if header.FormatVersion == LegacyFormatVersion && header.Compression.Mode != CompressionPatchFrom {
		return fmt.Errorf("unsupported version 1 compression mode %q", header.Compression.Mode)
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
		if !validMethod(entry.ForwardDifferentialMethod()) {
			return fmt.Errorf("file entry %q has unsupported forward method %q", entry.Path, entry.ForwardDifferentialMethod())
		}
		if methodRequiresExpandedLength(entry.ForwardDifferentialMethod()) && entry.ForwardExpandedLength == 0 {
			return fmt.Errorf("file entry %q has no forward expanded length", entry.Path)
		}
		if entry.ForwardDifferentialMethod() != MethodPatchFrom && header.Compression.Mode != CompressionHybridV2 {
			return fmt.Errorf("file entry %q uses a hybrid method with non-hybrid compression metadata", entry.Path)
		}
		if methodRequiresExpandedLength(entry.ForwardDifferentialMethod()) && !validInstructionExpandedLength(entry.ForwardExpandedLength, entry.TargetSize) {
			return fmt.Errorf("file entry %q has an unsafe forward instruction-stream expanded length", entry.Path)
		}
		if header.Reverse {
			if entry.ReverseLength == 0 {
				return fmt.Errorf("file entry %q has no reverse differential", entry.Path)
			}
			if !validMethod(entry.ReverseDifferentialMethod()) {
				return fmt.Errorf("file entry %q has unsupported reverse method %q", entry.Path, entry.ReverseDifferentialMethod())
			}
			if methodRequiresExpandedLength(entry.ReverseDifferentialMethod()) && entry.ReverseExpandedLength == 0 {
				return fmt.Errorf("file entry %q has no reverse expanded length", entry.Path)
			}
			if entry.ReverseDifferentialMethod() != MethodPatchFrom && header.Compression.Mode != CompressionHybridV2 {
				return fmt.Errorf("file entry %q uses a hybrid reverse method with non-hybrid compression metadata", entry.Path)
			}
			if methodRequiresExpandedLength(entry.ReverseDifferentialMethod()) && !validInstructionExpandedLength(entry.ReverseExpandedLength, entry.SourceSize) {
				return fmt.Errorf("file entry %q has an unsafe reverse instruction-stream expanded length", entry.Path)
			}
		} else if entry.ReverseLength != 0 || entry.ReverseOffset != 0 || entry.ReverseMethod != "" || entry.ReverseExpandedLength != 0 {
			return fmt.Errorf("file entry %q unexpectedly contains reverse data", entry.Path)
		}
	}
	return nil
}

func validMethod(method string) bool {
	return method == MethodPatchFrom || method == MethodCopyAdd || method == MethodSparse || method == MethodReplace
}

func methodRequiresExpandedLength(method string) bool {
	return method == MethodCopyAdd || method == MethodSparse
}

func validInstructionExpandedLength(expanded, fileSize uint64) bool {
	const allowance uint64 = 1 << 20
	if fileSize > (^uint64(0)-allowance)/2 {
		return false
	}
	return expanded > 0 && expanded <= fileSize*2+allowance
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
