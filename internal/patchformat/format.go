//go:generate go run ./gen

package patchformat

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
)

var (
	Magic       = [8]byte{'V', 'I', 'P', 'R', '\r', '\n', 0x1a, 0x04}
	IndexMagic  = [8]byte{'V', 'I', 'P', 'R', 'I', 'X', '4', 0x00}
	FooterMagic = [8]byte{'V', 'I', 'P', 'R', 'I', 'D', 'X', '4'}
)

const (
	MaxIndexSize         uint64 = 256 << 20
	MaxFileEntries              = 1 << 18
	MaxWindowsPerFile           = 1 << 24
	MaxPathBytes                = 1 << 20
	MaxCommentBytes             = 16 << 20
	MaxWindowSize               = 8 << 20
	MinWindowSize               = 64 << 10
	HashBLAKE3Tree              = "blake3-tree-v1"
	SupportedZstdVersion        = "1.5.7"
	CompressionHybrid           = "window-hybrid-v4"
	AlgorithmHybrid             = "hybrid"
)

type Digest [32]byte

func (digest Digest) Hex() string { return hex.EncodeToString(digest[:]) }

func ParseDigest(value string) (Digest, error) {
	var result Digest
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(result) {
		return result, fmt.Errorf("invalid 256-bit digest")
	}
	copy(result[:], decoded)
	return result, nil
}

type WindowKind uint8
type Codec uint8

type OptimizationMode uint8

const (
	OptimizeBalanced OptimizationMode = iota
	OptimizeApplySpeed
	OptimizePatchSize
)

func (mode OptimizationMode) String() string {
	switch mode {
	case OptimizeBalanced:
		return "balanced"
	case OptimizeApplySpeed:
		return "apply-speed"
	case OptimizePatchSize:
		return "patch-size"
	default:
		return fmt.Sprintf("unknown-%d", mode)
	}
}

func ParseOptimizationMode(value string) (OptimizationMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "balanced":
		return OptimizeBalanced, nil
	case "apply-speed", "speed":
		return OptimizeApplySpeed, nil
	case "patch-size", "size":
		return OptimizePatchSize, nil
	default:
		return 0, fmt.Errorf("unsupported optimization mode %q", value)
	}
}

type CreatorInfo struct {
	Name      string
	Version   string
	Commit    string
	BuildDate string
}

type Compression struct {
	Algorithm string
	Library   string
	Mode      string
	Level     int
}

type WindowDescriptor struct {
	OutputOffset     uint64
	OutputSize       uint32
	Kind             WindowKind
	Codec            Codec
	Flags            uint16
	PayloadOffset    uint64
	PayloadSize      uint32
	ExpandedSize     uint32
	SourceOffset     uint64
	SourceSize       uint32
	SourceFirstChunk uint32
	SourceChunkCount uint16
	InstructionCount uint16
	Digest           Digest
}

type FileEntry struct {
	Path       string
	SourceHash string
	TargetHash string
	SourceSize uint64
	TargetSize uint64

	SourceDigest    Digest
	TargetDigest    Digest
	WindowSize      uint32
	SourceChunkSize uint32
	SourceChunks    []Digest
	TargetChunks    []Digest
	ForwardWindows  []WindowDescriptor
	ReverseWindows  []WindowDescriptor

	SourceFineChunkSize uint32
	TargetFineChunkSize uint32
	SourceFineChunks    []FineDigest
	TargetFineChunks    []FineDigest
}

type Header struct {
	FormatVersion     int
	CreatedAt         time.Time
	Creator           CreatorInfo
	Comment           string
	HashAlgorithm     string
	Compression       Compression
	Reverse           bool
	DefaultWindowSize uint32
	Optimization      OptimizationMode
	FineVerification  bool
	Files             []FileEntry
}

type Patch struct {
	Header        Header
	DataOffset    uint64
	IndexOffset   uint64
	IndexSize     uint64
	IndexDigest   Digest
	ContainerSize uint64
}

type IndexVerifier func(index []byte, expected Digest) error

func WritePrefix(writer io.Writer, flags uint32) error {
	var prefix [PrefixSize]byte
	copy(prefix[:8], Magic[:])
	binary.LittleEndian.PutUint32(prefix[8:12], flags)
	if _, err := writer.Write(prefix[:]); err != nil {
		return fmt.Errorf("write V4 prefix: %w", err)
	}
	return nil
}

func WriteFooter(writer io.Writer, indexOffset, indexSize uint64, digest Digest, flags uint32) error {
	var footer [FooterSize]byte
	copy(footer[:8], FooterMagic[:])
	binary.LittleEndian.PutUint64(footer[8:16], indexOffset)
	binary.LittleEndian.PutUint64(footer[16:24], indexSize)
	copy(footer[24:56], digest[:])
	binary.LittleEndian.PutUint32(footer[56:60], flags)
	if _, err := writer.Write(footer[:]); err != nil {
		return fmt.Errorf("write V4 footer: %w", err)
	}
	return nil
}

func EncodeIndex(header Header) ([]byte, error) {
	normalizeHeader(&header)
	if err := ValidateHeader(header); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	buffer.Grow(4096)
	buffer.Write(IndexMagic[:])
	writeU32(&buffer, FormatVersion)
	flags := containerFlags(header.Reverse, header.FineVerification)
	writeU32(&buffer, flags)
	writeI64(&buffer, header.CreatedAt.UnixNano())
	writeI32(&buffer, int32(header.Compression.Level))
	buffer.WriteByte(byte(header.Optimization))
	buffer.Write([]byte{0, 0, 0})
	writeU32(&buffer, header.DefaultWindowSize)
	writeU32(&buffer, uint32(len(header.Files)))
	if err := writeString32(&buffer, header.Comment, MaxCommentBytes); err != nil {
		return nil, err
	}
	for _, value := range []string{header.Creator.Name, header.Creator.Version, header.Creator.Commit, header.Creator.BuildDate, header.Compression.Library} {
		if err := writeString16(&buffer, value); err != nil {
			return nil, err
		}
	}
	for index := range header.Files {
		if err := encodeFileEntry(&buffer, &header.Files[index], header.Reverse); err != nil {
			return nil, fmt.Errorf("encode file entry %d: %w", index, err)
		}
	}
	if header.FineVerification {
		if err := encodeFineVerification(&buffer, header.Files); err != nil {
			return nil, err
		}
	}
	if uint64(buffer.Len()) > MaxIndexSize {
		return nil, fmt.Errorf("V4 index exceeds %d bytes", MaxIndexSize)
	}
	return buffer.Bytes(), nil
}

func DecodeAt(reader io.ReaderAt, size uint64, verifier IndexVerifier) (Patch, error) {
	if reader == nil {
		return Patch{}, fmt.Errorf("patch reader is required")
	}
	if size < PrefixSize+FooterSize {
		return Patch{}, fmt.Errorf("truncated V4 container")
	}
	var prefix [PrefixSize]byte
	if _, err := reader.ReadAt(prefix[:], 0); err != nil {
		return Patch{}, fmt.Errorf("read V4 prefix: %w", err)
	}
	if !bytes.Equal(prefix[:8], Magic[:]) {
		return Patch{}, fmt.Errorf("not a VIPR V4 patch")
	}
	prefixFlags := binary.LittleEndian.Uint32(prefix[8:12])
	if prefixFlags&^supportedContainerFlags != 0 {
		return Patch{}, fmt.Errorf("V4 prefix contains unsupported flags")
	}
	if binary.LittleEndian.Uint32(prefix[12:16]) != 0 {
		return Patch{}, fmt.Errorf("V4 prefix reserved field is non-zero")
	}

	var footer [FooterSize]byte
	footerOffset := size - FooterSize
	if footerOffset > uint64(^uint64(0)>>1) {
		return Patch{}, fmt.Errorf("patch is too large")
	}
	if _, err := reader.ReadAt(footer[:], int64(footerOffset)); err != nil {
		return Patch{}, fmt.Errorf("read V4 footer: %w", err)
	}
	if !bytes.Equal(footer[:8], FooterMagic[:]) {
		return Patch{}, fmt.Errorf("invalid V4 footer magic")
	}
	indexOffset := binary.LittleEndian.Uint64(footer[8:16])
	indexSize := binary.LittleEndian.Uint64(footer[16:24])
	var digest Digest
	copy(digest[:], footer[24:56])
	footerFlags := binary.LittleEndian.Uint32(footer[56:60])
	if footerFlags&^supportedContainerFlags != 0 {
		return Patch{}, fmt.Errorf("V4 footer contains unsupported flags")
	}
	if binary.LittleEndian.Uint32(footer[60:64]) != 0 {
		return Patch{}, fmt.Errorf("V4 footer reserved field is non-zero")
	}
	if indexSize == 0 || indexSize > MaxIndexSize {
		return Patch{}, fmt.Errorf("invalid V4 index size %d", indexSize)
	}
	if indexOffset < PrefixSize || indexOffset > footerOffset || indexSize > footerOffset-indexOffset || indexOffset+indexSize != footerOffset {
		return Patch{}, fmt.Errorf("invalid V4 index range")
	}
	index := make([]byte, int(indexSize))
	if _, err := reader.ReadAt(index, int64(indexOffset)); err != nil {
		return Patch{}, fmt.Errorf("read V4 index: %w", err)
	}
	if verifier != nil {
		if err := verifier(index, digest); err != nil {
			return Patch{}, fmt.Errorf("verify V4 index: %w", err)
		}
	}
	header, err := decodeIndex(index)
	if err != nil {
		return Patch{}, err
	}
	if header.Reverse != (footerFlags&ContainerFlagReverse != 0) ||
		header.FineVerification != (footerFlags&ContainerFlagFineVerification != 0) ||
		prefixFlags != footerFlags {
		return Patch{}, fmt.Errorf("V4 container flags disagree")
	}
	result := Patch{Header: header, DataOffset: PrefixSize, IndexOffset: indexOffset, IndexSize: indexSize, IndexDigest: digest, ContainerSize: size}
	if err := ValidatePatch(result); err != nil {
		return Patch{}, err
	}
	return result, nil
}

// Decode exists for tooling that only has a sequential reader. Production paths
// use DecodeAt so large payloads are never materialized in memory.
func Decode(reader io.Reader) (Patch, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, int64(MaxIndexSize)+1<<30))
	if err != nil {
		return Patch{}, err
	}
	return DecodeAt(bytes.NewReader(payload), uint64(len(payload)), nil)
}

func normalizeHeader(header *Header) {
	if header.FormatVersion == 0 {
		header.FormatVersion = FormatVersion
	}
	if header.CreatedAt.IsZero() {
		header.CreatedAt = time.Now().UTC()
	}
	if header.HashAlgorithm == "" {
		header.HashAlgorithm = HashBLAKE3Tree
	}
	if header.Compression.Algorithm == "" {
		header.Compression.Algorithm = AlgorithmHybrid
	}
	if header.Compression.Mode == "" {
		header.Compression.Mode = CompressionHybrid
	}
	if hasFineVerification(header.Files) {
		header.FineVerification = true
	}
	for i := range header.Files {
		entry := &header.Files[i]
		if entry.SourceDigest == (Digest{}) && entry.SourceHash != "" {
			entry.SourceDigest, _ = ParseDigest(entry.SourceHash)
		}
		if entry.TargetDigest == (Digest{}) && entry.TargetHash != "" {
			entry.TargetDigest, _ = ParseDigest(entry.TargetHash)
		}
		entry.SourceHash = entry.SourceDigest.Hex()
		entry.TargetHash = entry.TargetDigest.Hex()
		if entry.SourceChunkSize == 0 {
			entry.SourceChunkSize = uint32(IdentityChunkSize)
		}
		if entry.WindowSize == 0 {
			entry.WindowSize = header.DefaultWindowSize
		}
	}
}

func ValidateHeader(header Header) error {
	if header.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported VIPR format version %d", header.FormatVersion)
	}
	if header.HashAlgorithm != HashBLAKE3Tree {
		return fmt.Errorf("unsupported hash algorithm %q", header.HashAlgorithm)
	}
	if header.Compression.Algorithm != AlgorithmHybrid || header.Compression.Mode != CompressionHybrid || header.Compression.Library != SupportedZstdVersion {
		return fmt.Errorf("invalid V4 compression metadata")
	}
	if header.Compression.Level < -131072 || header.Compression.Level > 22 {
		return fmt.Errorf("invalid V4 compression level %d", header.Compression.Level)
	}
	if header.Optimization > OptimizePatchSize {
		return fmt.Errorf("invalid V4 optimization mode %d", header.Optimization)
	}
	if !validWindowSize(header.DefaultWindowSize) {
		return fmt.Errorf("invalid V4 default window size %d", header.DefaultWindowSize)
	}
	if len(header.Comment) > MaxCommentBytes || !utf8.ValidString(header.Comment) {
		return fmt.Errorf("invalid V4 comment")
	}
	if len(header.Files) == 0 || len(header.Files) > MaxFileEntries {
		return fmt.Errorf("invalid V4 file count %d", len(header.Files))
	}
	seen := make(map[string]struct{}, len(header.Files))
	for index := range header.Files {
		entry := header.Files[index]
		if err := ValidatePath(entry.Path); err != nil {
			return fmt.Errorf("file entry %d: %w", index, err)
		}
		key := pathutil.CaseInsensitiveKey(entry.Path)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate, Unicode-equivalent, or case-colliding path %q", entry.Path)
		}
		seen[key] = struct{}{}
		if !validWindowSize(entry.WindowSize) || entry.SourceChunkSize != uint32(IdentityChunkSize) {
			return fmt.Errorf("file entry %q has invalid window/chunk sizing", entry.Path)
		}
		if entry.SourceSize > math.MaxInt64 || entry.TargetSize > math.MaxInt64 {
			return fmt.Errorf("file entry %q exceeds the signed 64-bit file range", entry.Path)
		}
		if entry.SourceHash != "" {
			parsed, err := ParseDigest(entry.SourceHash)
			if err != nil || parsed != entry.SourceDigest {
				return fmt.Errorf("file entry %q has an invalid source digest", entry.Path)
			}
		}
		if entry.TargetHash != "" {
			parsed, err := ParseDigest(entry.TargetHash)
			if err != nil || parsed != entry.TargetDigest {
				return fmt.Errorf("file entry %q has an invalid target digest", entry.Path)
			}
		}
		if uint64(len(entry.SourceChunks)) != chunkCount(entry.SourceSize) || uint64(len(entry.TargetChunks)) != chunkCount(entry.TargetSize) {
			return fmt.Errorf("file entry %q has an invalid digest table", entry.Path)
		}
		if !header.FineVerification && entryHasFineVerification(entry) {
			return fmt.Errorf("file entry %q has fine digests without the feature flag", entry.Path)
		}
		if err := validateFineDigestTable(entry.SourceSize, entry.SourceFineChunkSize, entry.SourceFineChunks); err != nil {
			return fmt.Errorf("file entry %q source fine digests: %w", entry.Path, err)
		}
		if err := validateFineDigestTable(entry.TargetSize, entry.TargetFineChunkSize, entry.TargetFineChunks); err != nil {
			return fmt.Errorf("file entry %q target fine digests: %w", entry.Path, err)
		}
		if err := validateWindowSet(entry.ForwardWindows, entry.TargetSize, entry.SourceSize, entry.WindowSize, true); err != nil {
			return fmt.Errorf("file entry %q forward windows: %w", entry.Path, err)
		}
		if header.Reverse {
			if err := validateWindowSet(entry.ReverseWindows, entry.SourceSize, entry.TargetSize, entry.WindowSize, true); err != nil {
				return fmt.Errorf("file entry %q reverse windows: %w", entry.Path, err)
			}
		} else if len(entry.ReverseWindows) != 0 {
			return fmt.Errorf("file entry %q contains unexpected reverse windows", entry.Path)
		}
	}
	return nil
}

func ValidatePatch(patch Patch) error {
	if err := ValidateHeader(patch.Header); err != nil {
		return err
	}
	type interval struct{ start, end uint64 }
	intervals := make([]interval, 0)
	for _, entry := range patch.Header.Files {
		sets := [][]WindowDescriptor{entry.ForwardWindows}
		if patch.Header.Reverse {
			sets = append(sets, entry.ReverseWindows)
		}
		for _, windows := range sets {
			for _, window := range windows {
				if window.PayloadSize == 0 {
					continue
				}
				start := window.PayloadOffset
				end := start + uint64(window.PayloadSize)
				if end < start || start < PrefixSize || end > patch.IndexOffset {
					return fmt.Errorf("window payload range is outside the V4 data section")
				}
				intervals = append(intervals, interval{start, end})
			}
		}
	}
	if len(intervals) == 0 {
		if patch.IndexOffset != PrefixSize {
			return fmt.Errorf("V4 patch has unreferenced payload bytes")
		}
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	if intervals[0].start != PrefixSize {
		return fmt.Errorf("V4 patch contains a gap before the first payload")
	}
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start != intervals[i-1].end {
			return fmt.Errorf("V4 patch contains overlapping or unreferenced payload bytes")
		}
	}
	if intervals[len(intervals)-1].end != patch.IndexOffset {
		return fmt.Errorf("V4 patch contains trailing unreferenced payload bytes")
	}
	return nil
}

func ValidatePath(value string) error {
	if value == "" || len(value) > MaxPathBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("invalid empty, oversized, non-UTF-8, or NUL path")
	}
	if strings.Contains(value, "\\") {
		return fmt.Errorf("patch paths must use forward slashes")
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("unsafe patch path %q", value)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasSuffix(component, " ") || strings.HasSuffix(component, ".") || strings.Contains(component, ":") {
			return fmt.Errorf("non-portable patch path %q", value)
		}
	}
	return nil
}

func validWindowSize(size uint32) bool {
	switch size {
	case 256 << 10, 512 << 10, 1 << 20, 2 << 20, 4 << 20, 8 << 20:
		return true
	default:
		return false
	}
}

func chunkCount(size uint64) uint64 {
	if size == 0 {
		return 0
	}
	return (size + IdentityChunkSize - 1) / IdentityChunkSize
}

func validateWindowSet(windows []WindowDescriptor, outputSize, inputSize uint64, windowSize uint32, required bool) error {
	if outputSize == 0 {
		if len(windows) != 0 {
			return fmt.Errorf("empty output has windows")
		}
		return nil
	}
	if required && len(windows) == 0 {
		return fmt.Errorf("non-empty output has no windows")
	}
	if len(windows) > MaxWindowsPerFile {
		return fmt.Errorf("too many windows")
	}
	var cursor uint64
	for index, window := range windows {
		if cursor > outputSize || window.OutputOffset != cursor || window.OutputSize == 0 || uint64(window.OutputSize) > uint64(windowSize) || uint64(window.OutputSize) > outputSize-cursor {
			return fmt.Errorf("invalid window %d output range", index)
		}
		if err := validateWindow(window, inputSize); err != nil {
			return fmt.Errorf("window %d: %w", index, err)
		}
		cursor += uint64(window.OutputSize)
	}
	if cursor != outputSize {
		return fmt.Errorf("windows cover %d bytes, expected %d", cursor, outputSize)
	}
	return nil
}

func maxZstdPayloadSize(expanded uint32) uint64 {
	// This deliberately exceeds ZSTD_compressBound for the supported V4
	// window sizes while preventing a tiny output from declaring a GiB payload.
	size := uint64(expanded)
	return size + size/128 + 4096
}

func validateWindow(window WindowDescriptor, inputSize uint64) error {
	if window.Flags != 0 {
		return fmt.Errorf("unsupported window flags")
	}
	maxInstructionSize := uint64(window.OutputSize)*2 + 1<<20
	hasSource := false
	switch window.Kind {
	case WindowSame:
		hasSource = true
		if window.Codec != CodecNone || window.PayloadSize != 0 || window.ExpandedSize != 0 || window.SourceSize != window.OutputSize || window.SourceOffset != window.OutputOffset || window.InstructionCount != 0 {
			return fmt.Errorf("invalid SAME window metadata")
		}
	case WindowCopy:
		hasSource = true
		if window.Codec != CodecNone || window.PayloadSize != 0 || window.ExpandedSize != 0 || window.SourceSize != window.OutputSize || window.InstructionCount != 0 {
			return fmt.Errorf("invalid COPY window metadata")
		}
	case WindowDeltaRaw:
		hasSource = true
		if window.Codec != CodecNone || window.PayloadSize == 0 || window.ExpandedSize != window.PayloadSize || window.InstructionCount == 0 || uint64(window.ExpandedSize) > maxInstructionSize || window.SourceSize == 0 {
			return fmt.Errorf("invalid raw delta metadata")
		}
	case WindowDeltaZstd:
		hasSource = true
		if window.Codec != CodecZstd || window.PayloadSize == 0 || window.ExpandedSize == 0 || window.InstructionCount == 0 || uint64(window.ExpandedSize) > maxInstructionSize ||
			uint64(window.PayloadSize) > maxZstdPayloadSize(window.ExpandedSize) || window.SourceSize == 0 {
			return fmt.Errorf("invalid zstd delta metadata")
		}
	case WindowReplaceRaw:
		if window.Codec != CodecNone || window.PayloadSize != window.OutputSize || window.ExpandedSize != window.OutputSize || window.SourceSize != 0 || window.InstructionCount != 0 {
			return fmt.Errorf("invalid raw replacement metadata")
		}
	case WindowReplaceZstd:
		if window.Codec != CodecZstd || window.PayloadSize == 0 || window.ExpandedSize != window.OutputSize ||
			uint64(window.PayloadSize) > maxZstdPayloadSize(window.ExpandedSize) || window.SourceSize != 0 || window.InstructionCount != 0 {
			return fmt.Errorf("invalid zstd replacement metadata")
		}
	case WindowZero:
		if window.Codec != CodecNone || window.PayloadSize != 0 || window.ExpandedSize != window.OutputSize || window.SourceSize != 0 || window.InstructionCount != 0 {
			return fmt.Errorf("invalid zero window metadata")
		}
	case WindowRun:
		if window.Codec != CodecNone || window.PayloadSize != 1 || window.ExpandedSize != window.OutputSize || window.SourceSize != 0 || window.InstructionCount != 0 {
			return fmt.Errorf("invalid run window metadata")
		}
	default:
		return fmt.Errorf("unsupported window kind %d", window.Kind)
	}
	if hasSource {
		if window.SourceOffset > inputSize || uint64(window.SourceSize) > inputSize-window.SourceOffset {
			return fmt.Errorf("source range exceeds input")
		}
		first := window.SourceOffset / IdentityChunkSize
		end := (window.SourceOffset + uint64(window.SourceSize) + IdentityChunkSize - 1) / IdentityChunkSize
		if first > math.MaxUint32 || end-first > math.MaxUint16 || window.SourceFirstChunk != uint32(first) || window.SourceChunkCount != uint16(end-first) {
			return fmt.Errorf("source digest range is inconsistent")
		}
	} else if window.SourceOffset != 0 || window.SourceFirstChunk != 0 || window.SourceChunkCount != 0 {
		return fmt.Errorf("source-free window declares source metadata")
	}
	return nil
}

func encodeFileEntry(buffer *bytes.Buffer, entry *FileEntry, reverse bool) error {
	if err := writeString32(buffer, entry.Path, MaxPathBytes); err != nil {
		return err
	}
	writeU64(buffer, entry.SourceSize)
	writeU64(buffer, entry.TargetSize)
	buffer.Write(entry.SourceDigest[:])
	buffer.Write(entry.TargetDigest[:])
	writeU32(buffer, entry.WindowSize)
	writeU32(buffer, entry.SourceChunkSize)
	writeU32(buffer, uint32(len(entry.SourceChunks)))
	writeU32(buffer, uint32(len(entry.TargetChunks)))
	writeU32(buffer, uint32(len(entry.ForwardWindows)))
	if reverse {
		writeU32(buffer, uint32(len(entry.ReverseWindows)))
	} else {
		writeU32(buffer, 0)
	}
	for _, digest := range entry.SourceChunks {
		buffer.Write(digest[:])
	}
	for _, digest := range entry.TargetChunks {
		buffer.Write(digest[:])
	}
	for _, window := range entry.ForwardWindows {
		encodeWindow(buffer, window)
	}
	if reverse {
		for _, window := range entry.ReverseWindows {
			encodeWindow(buffer, window)
		}
	}
	return nil
}

func encodeWindowBytes(destination []byte, window WindowDescriptor) {
	if len(destination) < WindowDescriptorSize {
		panic("short V4 window descriptor destination")
	}
	binary.LittleEndian.PutUint64(destination[0:8], window.OutputOffset)
	binary.LittleEndian.PutUint32(destination[8:12], window.OutputSize)
	destination[12] = byte(window.Kind)
	destination[13] = byte(window.Codec)
	binary.LittleEndian.PutUint16(destination[14:16], window.Flags)
	binary.LittleEndian.PutUint64(destination[16:24], window.PayloadOffset)
	binary.LittleEndian.PutUint32(destination[24:28], window.PayloadSize)
	binary.LittleEndian.PutUint32(destination[28:32], window.ExpandedSize)
	binary.LittleEndian.PutUint64(destination[32:40], window.SourceOffset)
	binary.LittleEndian.PutUint32(destination[40:44], window.SourceSize)
	binary.LittleEndian.PutUint32(destination[44:48], window.SourceFirstChunk)
	binary.LittleEndian.PutUint16(destination[48:50], window.SourceChunkCount)
	binary.LittleEndian.PutUint16(destination[50:52], window.InstructionCount)
	copy(destination[52:84], window.Digest[:])
	clear(destination[84:WindowDescriptorSize])
}

// MarshalWindowDescriptor encodes one descriptor in its canonical V4 wire
// representation for the native data plane.
func MarshalWindowDescriptor(window WindowDescriptor) [WindowDescriptorSize]byte {
	var encoded [WindowDescriptorSize]byte
	encodeWindowBytes(encoded[:], window)
	return encoded
}

// MarshalWindowDescriptors encodes descriptors in their canonical V4 wire
// representation for the native data plane. Passing the stable wire layout
// instead of a C struct array avoids architecture-dependent ABI padding.
func MarshalWindowDescriptors(windows []WindowDescriptor) []byte {
	encoded := make([]byte, len(windows)*WindowDescriptorSize)
	for index := range windows {
		start := index * WindowDescriptorSize
		encodeWindowBytes(encoded[start:start+WindowDescriptorSize], windows[index])
	}
	return encoded
}

func encodeWindow(buffer *bytes.Buffer, window WindowDescriptor) {
	encoded := MarshalWindowDescriptor(window)
	buffer.Write(encoded[:])
}

func decodeIndex(payload []byte) (Header, error) {
	reader := newIndexReader(payload)
	magic, err := reader.bytes(8)
	if err != nil {
		return Header{}, err
	}
	if !bytes.Equal(magic, IndexMagic[:]) {
		return Header{}, fmt.Errorf("invalid V4 index magic")
	}
	version, err := reader.u32()
	if err != nil {
		return Header{}, err
	}
	if version != FormatVersion {
		return Header{}, fmt.Errorf("unsupported V4 index version %d", version)
	}
	flags, err := reader.u32()
	if err != nil {
		return Header{}, err
	}
	if flags&^supportedContainerFlags != 0 {
		return Header{}, fmt.Errorf("V4 index contains unsupported flags")
	}
	created, err := reader.i64()
	if err != nil {
		return Header{}, err
	}
	level, err := reader.i32()
	if err != nil {
		return Header{}, err
	}
	optimization, err := reader.u8()
	if err != nil {
		return Header{}, err
	}
	reserved, err := reader.bytes(3)
	if err != nil {
		return Header{}, err
	}
	if reserved[0]|reserved[1]|reserved[2] != 0 {
		return Header{}, fmt.Errorf("V4 index reserved bytes are non-zero")
	}
	defaultWindow, err := reader.u32()
	if err != nil {
		return Header{}, err
	}
	fileCount, err := reader.u32()
	if err != nil {
		return Header{}, err
	}
	if fileCount == 0 || fileCount > MaxFileEntries {
		return Header{}, fmt.Errorf("invalid V4 file count %d", fileCount)
	}
	comment, err := reader.string32(MaxCommentBytes)
	if err != nil {
		return Header{}, err
	}
	values := make([]string, 5)
	for i := range values {
		values[i], err = reader.string16()
		if err != nil {
			return Header{}, err
		}
	}
	header := Header{FormatVersion: FormatVersion, CreatedAt: time.Unix(0, created).UTC(), Creator: CreatorInfo{Name: values[0], Version: values[1], Commit: values[2], BuildDate: values[3]}, Comment: comment, HashAlgorithm: HashBLAKE3Tree, Compression: Compression{Algorithm: AlgorithmHybrid, Library: values[4], Mode: CompressionHybrid, Level: int(level)}, Reverse: flags&ContainerFlagReverse != 0, DefaultWindowSize: defaultWindow, Optimization: OptimizationMode(optimization), FineVerification: flags&ContainerFlagFineVerification != 0, Files: make([]FileEntry, int(fileCount))}
	for i := range header.Files {
		header.Files[i], err = decodeFileEntry(reader, header.Reverse)
		if err != nil {
			return Header{}, fmt.Errorf("decode file entry %d: %w", i, err)
		}
	}
	if header.FineVerification {
		if err := decodeFineVerification(reader, header.Files); err != nil {
			return Header{}, err
		}
	}
	if reader.remaining() != 0 {
		return Header{}, fmt.Errorf("V4 index contains %d trailing bytes", reader.remaining())
	}
	normalizeHeader(&header)
	if err := ValidateHeader(header); err != nil {
		return Header{}, err
	}
	return header, nil
}

func decodeFileEntry(reader *indexReader, reverse bool) (FileEntry, error) {
	var entry FileEntry
	var err error
	entry.Path, err = reader.string32(MaxPathBytes)
	if err != nil {
		return entry, err
	}
	entry.SourceSize, err = reader.u64()
	if err != nil {
		return entry, err
	}
	entry.TargetSize, err = reader.u64()
	if err != nil {
		return entry, err
	}
	source, err := reader.bytes(32)
	if err != nil {
		return entry, err
	}
	copy(entry.SourceDigest[:], source)
	target, err := reader.bytes(32)
	if err != nil {
		return entry, err
	}
	copy(entry.TargetDigest[:], target)
	entry.WindowSize, err = reader.u32()
	if err != nil {
		return entry, err
	}
	entry.SourceChunkSize, err = reader.u32()
	if err != nil {
		return entry, err
	}
	sourceCount, err := reader.u32()
	if err != nil {
		return entry, err
	}
	targetCount, err := reader.u32()
	if err != nil {
		return entry, err
	}
	forwardCount, err := reader.u32()
	if err != nil {
		return entry, err
	}
	reverseCount, err := reader.u32()
	if err != nil {
		return entry, err
	}
	if entry.SourceSize > math.MaxInt64 || entry.TargetSize > math.MaxInt64 || uint64(sourceCount) != chunkCount(entry.SourceSize) || uint64(targetCount) != chunkCount(entry.TargetSize) || forwardCount > MaxWindowsPerFile || reverseCount > MaxWindowsPerFile {
		return entry, fmt.Errorf("invalid V4 table counts")
	}
	requiredBytes := (uint64(sourceCount)+uint64(targetCount))*32 + (uint64(forwardCount)+uint64(reverseCount))*WindowDescriptorSize
	if requiredBytes > uint64(reader.remaining()) || requiredBytes > MaxIndexSize {
		return entry, fmt.Errorf("V4 tables exceed remaining index data")
	}
	if !reverse && reverseCount != 0 {
		return entry, fmt.Errorf("unexpected reverse window count")
	}
	entry.SourceChunks = make([]Digest, int(sourceCount))
	entry.TargetChunks = make([]Digest, int(targetCount))
	for i := range entry.SourceChunks {
		value, e := reader.bytes(32)
		if e != nil {
			return entry, e
		}
		copy(entry.SourceChunks[i][:], value)
	}
	for i := range entry.TargetChunks {
		value, e := reader.bytes(32)
		if e != nil {
			return entry, e
		}
		copy(entry.TargetChunks[i][:], value)
	}
	entry.ForwardWindows = make([]WindowDescriptor, int(forwardCount))
	entry.ReverseWindows = make([]WindowDescriptor, int(reverseCount))
	for i := range entry.ForwardWindows {
		entry.ForwardWindows[i], err = decodeWindow(reader)
		if err != nil {
			return entry, err
		}
	}
	for i := range entry.ReverseWindows {
		entry.ReverseWindows[i], err = decodeWindow(reader)
		if err != nil {
			return entry, err
		}
	}
	return entry, nil
}

func decodeWindow(reader *indexReader) (WindowDescriptor, error) {
	var window WindowDescriptor
	var err error
	window.OutputOffset, err = reader.u64()
	if err != nil {
		return window, err
	}
	window.OutputSize, err = reader.u32()
	if err != nil {
		return window, err
	}
	kind, err := reader.u8()
	if err != nil {
		return window, err
	}
	window.Kind = WindowKind(kind)
	codec, err := reader.u8()
	if err != nil {
		return window, err
	}
	window.Codec = Codec(codec)
	window.Flags, err = reader.u16()
	if err != nil {
		return window, err
	}
	window.PayloadOffset, err = reader.u64()
	if err != nil {
		return window, err
	}
	window.PayloadSize, err = reader.u32()
	if err != nil {
		return window, err
	}
	window.ExpandedSize, err = reader.u32()
	if err != nil {
		return window, err
	}
	window.SourceOffset, err = reader.u64()
	if err != nil {
		return window, err
	}
	window.SourceSize, err = reader.u32()
	if err != nil {
		return window, err
	}
	window.SourceFirstChunk, err = reader.u32()
	if err != nil {
		return window, err
	}
	window.SourceChunkCount, err = reader.u16()
	if err != nil {
		return window, err
	}
	window.InstructionCount, err = reader.u16()
	if err != nil {
		return window, err
	}
	digest, err := reader.bytes(32)
	if err != nil {
		return window, err
	}
	copy(window.Digest[:], digest)
	reserved, err := reader.u32()
	if err != nil {
		return window, err
	}
	if reserved != 0 {
		return window, fmt.Errorf("window reserved field is non-zero")
	}
	return window, nil
}

type indexReader struct {
	data   []byte
	offset int
}

func newIndexReader(data []byte) *indexReader { return &indexReader{data: data} }
func (r *indexReader) remaining() int         { return len(r.data) - r.offset }
func (r *indexReader) bytes(count int) ([]byte, error) {
	if count < 0 || count > r.remaining() {
		return nil, io.ErrUnexpectedEOF
	}
	value := r.data[r.offset : r.offset+count]
	r.offset += count
	return value, nil
}
func (r *indexReader) u8() (uint8, error) {
	v, e := r.bytes(1)
	if e != nil {
		return 0, e
	}
	return v[0], nil
}
func (r *indexReader) u16() (uint16, error) {
	v, e := r.bytes(2)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint16(v), nil
}
func (r *indexReader) u32() (uint32, error) {
	v, e := r.bytes(4)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(v), nil
}
func (r *indexReader) u64() (uint64, error) {
	v, e := r.bytes(8)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint64(v), nil
}
func (r *indexReader) i32() (int32, error) { v, e := r.u32(); return int32(v), e }
func (r *indexReader) i64() (int64, error) { v, e := r.u64(); return int64(v), e }
func (r *indexReader) string16() (string, error) {
	n, e := r.u16()
	if e != nil {
		return "", e
	}
	v, e := r.bytes(int(n))
	return string(v), e
}
func (r *indexReader) string32(limit int) (string, error) {
	n, e := r.u32()
	if e != nil {
		return "", e
	}
	if n > uint32(limit) {
		return "", fmt.Errorf("string exceeds limit")
	}
	v, e := r.bytes(int(n))
	return string(v), e
}

func writeU16(w io.Writer, value uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], value)
	_, _ = w.Write(b[:])
}
func writeU32(w io.Writer, value uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	_, _ = w.Write(b[:])
}
func writeU64(w io.Writer, value uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], value)
	_, _ = w.Write(b[:])
}
func writeI32(w io.Writer, value int32) { writeU32(w, uint32(value)) }
func writeI64(w io.Writer, value int64) { writeU64(w, uint64(value)) }
func writeString16(w io.Writer, value string) error {
	if len(value) > int(^uint16(0)) {
		return fmt.Errorf("string too long")
	}
	writeU16(w, uint16(len(value)))
	_, err := io.WriteString(w, value)
	return err
}
func writeString32(w io.Writer, value string, limit int) error {
	if len(value) > limit {
		return fmt.Errorf("string too long")
	}
	writeU32(w, uint32(len(value)))
	_, err := io.WriteString(w, value)
	return err
}
