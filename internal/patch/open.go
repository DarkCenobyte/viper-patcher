package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type openedPatch struct {
	file   *os.File
	parsed patchformat.Patch
	digest string
}

// Open parses and validates one stable patch file.
func Open(path string) (patchformat.Patch, error) {
	parsed, _, err := OpenWithDigest(path)
	return parsed, err
}

// OpenWithDigest reads one stable patch file and returns its verified SHA-256
// digest. The patch bytes are hashed exactly once.
func OpenWithDigest(path string) (patchformat.Patch, string, error) {
	opened, err := openPatchForApply(path, "")
	if err != nil {
		return patchformat.Patch{}, "", err
	}
	defer opened.Close()
	return opened.parsed, opened.digest, nil
}

func openPatchForApply(path, expectedDigest string) (*openedPatch, error) {
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

	hash := sha256.New()
	reader := io.TeeReader(file, hash)
	parsed, err := patchformat.Decode(reader)
	if err != nil {
		return closeWithError(err)
	}
	remaining, err := io.Copy(hash, reader)
	if err != nil {
		return closeWithError(fmt.Errorf("hash patch payload: %w", err))
	}
	if identity.Size() < 0 {
		return closeWithError(fmt.Errorf("patch file has an invalid size"))
	}
	size := parsed.DataOffset + uint64(remaining)
	if size != uint64(identity.Size()) {
		return closeWithError(fmt.Errorf("patch file changed while it was being read"))
	}
	if err := validateDifferentialRanges(parsed, size); err != nil {
		return closeWithError(err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedDigest != "" && digest != expectedDigest {
		return closeWithError(fmt.Errorf("selected patch changed after it was inspected"))
	}
	if err := verifyOpenPatchMetadata(file, path, identity, size); err != nil {
		return closeWithError(err)
	}
	return &openedPatch{file: file, parsed: parsed, digest: digest}, nil
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
