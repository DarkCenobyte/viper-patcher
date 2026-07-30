package patchformat

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"unsafe"
)

// DecodeLimits bounds both the wire index and its expanded Go representation.
// A zero field selects the architecture-aware default.
type DecodeLimits struct {
	MaxIndexBytes   uint64
	MaxDecodedBytes uint64
	MaxFiles        uint64
	MaxWindows      uint64
	MaxDigests      uint64
	MaxStringBytes  uint64
}

// DefaultDecodeLimits keeps 32-bit processes well below their constrained
// virtual address space while retaining the full V4 wire limit on 64-bit.
func DefaultDecodeLimits() DecodeLimits {
	if strconvIntSize() == 32 {
		return DecodeLimits{
			MaxIndexBytes:   32 << 20,
			MaxDecodedBytes: 192 << 20,
			MaxFiles:        16 << 10,
			MaxWindows:      1 << 20,
			MaxDigests:      1 << 20,
			MaxStringBytes:  32 << 20,
		}
	}
	return DecodeLimits{
		MaxIndexBytes:   MaxIndexSize,
		MaxDecodedBytes: 512 << 20,
		MaxFiles:        MaxFileEntries,
		MaxWindows:      8 << 20,
		MaxDigests:      16 << 20,
		MaxStringBytes:  64 << 20,
	}
}

func strconvIntSize() int {
	return 32 << (^uint(0) >> 63)
}

func normalizeDecodeLimits(limits DecodeLimits) DecodeLimits {
	defaults := DefaultDecodeLimits()
	if limits.MaxIndexBytes == 0 {
		limits.MaxIndexBytes = defaults.MaxIndexBytes
	}
	if limits.MaxDecodedBytes == 0 {
		limits.MaxDecodedBytes = defaults.MaxDecodedBytes
	}
	if limits.MaxFiles == 0 {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxWindows == 0 {
		limits.MaxWindows = defaults.MaxWindows
	}
	if limits.MaxDigests == 0 {
		limits.MaxDigests = defaults.MaxDigests
	}
	if limits.MaxStringBytes == 0 {
		limits.MaxStringBytes = defaults.MaxStringBytes
	}
	return limits
}

// DecodeAtWithLimits rejects oversized indexes before allocation and then
// accounts for the decoded slices before returning the patch to callers.
func DecodeAtWithLimits(reader io.ReaderAt, size uint64, limits DecodeLimits, verifier IndexVerifier) (Patch, error) {
	limits = normalizeDecodeLimits(limits)
	if reader == nil {
		return Patch{}, fmt.Errorf("patch reader is required")
	}
	if size < PrefixSize+FooterSize {
		return Patch{}, fmt.Errorf("truncated V4 container")
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
	indexSize := binary.LittleEndian.Uint64(footer[16:24])
	if indexSize == 0 || indexSize > limits.MaxIndexBytes {
		return Patch{}, fmt.Errorf("V4 index size %d exceeds runtime limit %d", indexSize, limits.MaxIndexBytes)
	}
	if indexSize > uint64(maxInt()) {
		return Patch{}, fmt.Errorf("V4 index cannot be represented by this architecture")
	}

	parsed, err := DecodeAt(reader, size, verifier)
	if err != nil {
		return Patch{}, err
	}
	estimated, counts, err := estimateDecodedIndex(parsed.Header)
	if err != nil {
		return Patch{}, err
	}
	if counts.files > limits.MaxFiles {
		return Patch{}, fmt.Errorf("V4 file count %d exceeds runtime limit %d", counts.files, limits.MaxFiles)
	}
	if counts.windows > limits.MaxWindows {
		return Patch{}, fmt.Errorf("V4 window count %d exceeds runtime limit %d", counts.windows, limits.MaxWindows)
	}
	if counts.digests > limits.MaxDigests {
		return Patch{}, fmt.Errorf("V4 digest count %d exceeds runtime limit %d", counts.digests, limits.MaxDigests)
	}
	if counts.strings > limits.MaxStringBytes {
		return Patch{}, fmt.Errorf("V4 string bytes %d exceed runtime limit %d", counts.strings, limits.MaxStringBytes)
	}
	if estimated > limits.MaxDecodedBytes {
		return Patch{}, fmt.Errorf("V4 decoded index estimate %d exceeds runtime limit %d", estimated, limits.MaxDecodedBytes)
	}
	return parsed, nil
}

type decodedCounts struct {
	files, windows, digests, strings uint64
}

func estimateDecodedIndex(header Header) (uint64, decodedCounts, error) {
	var counts decodedCounts
	counts.files = uint64(len(header.Files))
	for _, value := range []string{
		header.Comment,
		header.Creator.Name,
		header.Creator.Version,
		header.Creator.Commit,
		header.Creator.BuildDate,
		header.Compression.Algorithm,
		header.Compression.Library,
		header.Compression.Mode,
		header.HashAlgorithm,
	} {
		if err := checkedAdd(&counts.strings, uint64(len(value))); err != nil {
			return 0, counts, err
		}
	}
	for index := range header.Files {
		entry := &header.Files[index]
		if err := checkedAdd(&counts.strings, uint64(len(entry.Path)+len(entry.SourceHash)+len(entry.TargetHash))); err != nil {
			return 0, counts, err
		}
		if err := checkedAdd(&counts.windows, uint64(len(entry.ForwardWindows)+len(entry.ReverseWindows))); err != nil {
			return 0, counts, err
		}
		if err := checkedAdd(&counts.digests, uint64(len(entry.SourceChunks)+len(entry.TargetChunks))); err != nil {
			return 0, counts, err
		}
	}

	total := uint64(unsafe.Sizeof(Header{}))
	terms := []struct {
		count uint64
		size  uint64
	}{
		{counts.files, uint64(unsafe.Sizeof(FileEntry{}))},
		{counts.windows, uint64(unsafe.Sizeof(WindowDescriptor{}))},
		{counts.digests, uint64(unsafe.Sizeof(Digest{}))},
		{counts.strings, 1},
	}
	for _, term := range terms {
		high, low := bits.Mul64(term.count, term.size)
		if high != 0 {
			return 0, counts, fmt.Errorf("V4 decoded index estimate overflows")
		}
		if err := checkedAdd(&total, low); err != nil {
			return 0, counts, err
		}
	}
	// Account conservatively for slice headers, maps, alignment, and allocator
	// size classes without depending on their exact runtime implementation.
	high, overhead := bits.Mul64(total, 2)
	if high != 0 {
		return 0, counts, fmt.Errorf("V4 decoded index overhead estimate overflows")
	}
	return overhead, counts, nil
}

func checkedAdd(total *uint64, value uint64) error {
	sum, carry := bits.Add64(*total, value, 0)
	if carry != 0 {
		return fmt.Errorf("V4 decoded index estimate overflows")
	}
	*total = sum
	return nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
