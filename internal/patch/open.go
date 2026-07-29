//go:build ignore

package patch

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type openedPatch struct {
	file     *os.File
	parsed   patchformat.Patch
	digest   string
	path     string
	identity os.FileInfo
	size     uint64
}

// PreparedPatch keeps one validated patch handle open between GUI inspection
// and application. The same immutable handle is reused, so application does not
// need to reopen and hash the complete patch a second time.
type PreparedPatch struct {
	mutex  sync.Mutex
	path   string
	opened *openedPatch
}

// Prepare opens, parses, validates, and fingerprints one patch for later reuse.
func Prepare(path string) (*PreparedPatch, error) {
	opened, err := openPatch(path, "", true)
	if err != nil {
		return nil, err
	}
	return &PreparedPatch{path: path, opened: opened}, nil
}

// Path returns the path used to open the prepared patch.
func (prepared *PreparedPatch) Path() string {
	if prepared == nil {
		return ""
	}
	prepared.mutex.Lock()
	defer prepared.mutex.Unlock()
	return prepared.path
}

// Digest returns the physical BLAKE3-256 fingerprint calculated during Prepare.
func (prepared *PreparedPatch) Digest() (string, error) {
	if prepared == nil {
		return "", fmt.Errorf("prepared patch is unavailable")
	}
	prepared.mutex.Lock()
	defer prepared.mutex.Unlock()
	if prepared.opened == nil {
		return "", fmt.Errorf("prepared patch is closed")
	}
	return prepared.opened.digest, nil
}

// Parsed returns an independent copy of the validated patch metadata.
func (prepared *PreparedPatch) Parsed() (patchformat.Patch, error) {
	if prepared == nil {
		return patchformat.Patch{}, fmt.Errorf("prepared patch is unavailable")
	}
	prepared.mutex.Lock()
	defer prepared.mutex.Unlock()
	if prepared.opened == nil {
		return patchformat.Patch{}, fmt.Errorf("prepared patch is closed")
	}
	parsed := prepared.opened.parsed
	parsed.Header.Files = append([]patchformat.FileEntry(nil), parsed.Header.Files...)
	return parsed, nil
}

func (prepared *PreparedPatch) acquire() (*openedPatch, func() error, error) {
	if prepared == nil {
		return nil, nil, fmt.Errorf("prepared patch is unavailable")
	}
	prepared.mutex.Lock()
	if prepared.opened == nil {
		prepared.mutex.Unlock()
		return nil, nil, fmt.Errorf("prepared patch is closed")
	}
	if err := prepared.opened.verifyStable(); err != nil {
		prepared.mutex.Unlock()
		return nil, nil, err
	}
	return prepared.opened, func() error {
		prepared.mutex.Unlock()
		return nil
	}, nil
}

// Close releases the stable patch handle. It waits for an active prepared apply.
func (prepared *PreparedPatch) Close() error {
	if prepared == nil {
		return nil
	}
	prepared.mutex.Lock()
	defer prepared.mutex.Unlock()
	if prepared.opened == nil {
		return nil
	}
	err := prepared.opened.Close()
	prepared.opened = nil
	return err
}

// Open parses and validates one stable patch file.
func Open(path string) (patchformat.Patch, error) {
	parsed, _, err := OpenWithDigest(path)
	return parsed, err
}

// OpenWithDigest reads one stable patch file and returns its verified BLAKE3-256
// fingerprint. The patch bytes are hashed exactly once.
func OpenWithDigest(path string) (patchformat.Patch, string, error) {
	opened, err := openPatch(path, "", true)
	if err != nil {
		return patchformat.Patch{}, "", err
	}
	defer opened.Close()
	return opened.parsed, opened.digest, nil
}

func openPatchForApply(path, expectedDigest string) (*openedPatch, error) {
	return openPatch(path, expectedDigest, expectedDigest != "")
}

func openPatch(path, expectedDigest string, calculateDigest bool) (*openedPatch, error) {
	file, identity, err := openStableRegularFile(path)
	if err != nil {
		return nil, err
	}
	closeWithError := func(operationError error) (*openedPatch, error) {
		closeError := file.Close()
		if closeError != nil {
			return nil, fmt.Errorf("%v; close patch file: %w", operationError, closeError)
		}
		return nil, operationError
	}

	var parsed patchformat.Patch
	var digest string
	var decodeError error
	if calculateDigest {
		parsed, digest, decodeError = decodeAndHashPatch(file)
	} else {
		parsed, decodeError = patchformat.Decode(file)
	}
	if decodeError != nil {
		return closeWithError(decodeError)
	}
	if identity.Size() < 0 {
		return closeWithError(fmt.Errorf("patch file has an invalid size"))
	}
	size := uint64(identity.Size())
	if expectedDigest != "" && digest != expectedDigest {
		return closeWithError(fmt.Errorf("selected patch changed after it was inspected"))
	}
	if err := verifyOpenPatchMetadata(file, path, identity, size); err != nil {
		return closeWithError(err)
	}
	if err := validateDifferentialRanges(parsed, size); err != nil {
		return closeWithError(err)
	}
	return &openedPatch{
		file:     file,
		parsed:   parsed,
		digest:   digest,
		path:     path,
		identity: identity,
		size:     size,
	}, nil
}

func decodeAndHashPatch(reader io.Reader) (patchformat.Patch, string, error) {
	hash := hashutil.NewFingerprintHasher()
	hashingReader := io.TeeReader(reader, hash)
	parsed, err := patchformat.Decode(hashingReader)
	if err != nil {
		return patchformat.Patch{}, "", err
	}
	// Decode consumes exactly the prefix without read-ahead. Hash the remaining
	// payload directly with the explicit one MiB copy buffer instead of the small
	// io.Discard ReaderFrom buffer or an os.File WriterTo fallback.
	if _, err := copyBuffered(hash, reader); err != nil {
		return patchformat.Patch{}, "", fmt.Errorf("hash patch payload: %w", err)
	}
	return parsed, hex.EncodeToString(hash.Sum(nil)), nil
}

func (opened *openedPatch) verifyStable() error {
	if opened == nil || opened.file == nil || opened.identity == nil {
		return fmt.Errorf("patch file is unavailable")
	}
	return verifyOpenPatchMetadata(opened.file, opened.path, opened.identity, opened.size)
}

func (opened *openedPatch) Close() error {
	if opened == nil || opened.file == nil {
		return nil
	}
	err := opened.file.Close()
	opened.file = nil
	return err
}

func validateDifferentialRanges(parsed patchformat.Patch, containerSize uint64) error {
	type interval struct{ start, end uint64 }
	intervals := make([]interval, 0, len(parsed.Header.Files)*2)
	add := func(offset, length uint64) error {
		if offset > ^uint64(0)-parsed.DataOffset || length > ^uint64(0)-(parsed.DataOffset+offset) {
			return fmt.Errorf("differential range overflows")
		}
		start := parsed.DataOffset + offset
		end := start + length
		if end > containerSize {
			return fmt.Errorf("differential range exceeds patch file size")
		}
		intervals = append(intervals, interval{start: start, end: end})
		return nil
	}
	for _, entry := range parsed.Header.Files {
		if err := add(entry.ForwardOffset, entry.ForwardLength); err != nil {
			return fmt.Errorf("invalid forward differential for %q: %w", entry.Path, err)
		}
		if parsed.Header.Reverse {
			if err := add(entry.ReverseOffset, entry.ReverseLength); err != nil {
				return fmt.Errorf("invalid reverse differential for %q: %w", entry.Path, err)
			}
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	if len(intervals) == 0 || intervals[0].start != parsed.DataOffset {
		return fmt.Errorf("patch contains a gap before its first differential")
	}
	for index := 1; index < len(intervals); index++ {
		if intervals[index].start < intervals[index-1].end {
			return fmt.Errorf("patch contains overlapping differential ranges")
		}
		if intervals[index].start != intervals[index-1].end {
			return fmt.Errorf("patch contains unreferenced data between differentials")
		}
	}
	if intervals[len(intervals)-1].end != containerSize {
		return fmt.Errorf("patch contains trailing unreferenced data")
	}
	return nil
}

func verifyOpenPatchMetadata(file *os.File, path string, identity os.FileInfo, expectedSize uint64) error {
	currentInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect patch after parsing: %w", err)
	}
	if !os.SameFile(identity, currentInfo) || currentInfo.Size() < 0 || uint64(currentInfo.Size()) != expectedSize || !currentInfo.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("patch file changed while it was being parsed")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect patch path after parsing: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, pathInfo) {
		return fmt.Errorf("patch file was replaced while it was being parsed")
	}
	return nil
}
