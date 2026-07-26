package patch

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

// Open reads a patch and validates every differential range against the container size.
func Open(path string) (patchformat.Patch, error) {
	parsed, _, err := OpenWithDigest(path)
	return parsed, err
}

// OpenWithDigest reads one stable patch file and returns its verified SHA-256 digest.
func OpenWithDigest(path string) (patchformat.Patch, string, error) {
	file, identity, err := openStableRegularFile(path)
	if err != nil {
		return patchformat.Patch{}, "", err
	}
	defer file.Close()

	digest, size, err := hashutil.Reader(file)
	if err != nil {
		return patchformat.Patch{}, "", fmt.Errorf("hash patch file: %w", err)
	}
	if identity.Size() < 0 || size != uint64(identity.Size()) {
		return patchformat.Patch{}, "", fmt.Errorf("patch file changed while it was being read")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return patchformat.Patch{}, "", fmt.Errorf("rewind patch file: %w", err)
	}
	parsed, err := patchformat.Decode(file)
	if err != nil {
		return patchformat.Patch{}, "", err
	}
	if err := validateDifferentialRanges(parsed, size); err != nil {
		return patchformat.Patch{}, "", err
	}
	if err := verifyOpenPatch(file, path, identity, digest, size); err != nil {
		return patchformat.Patch{}, "", err
	}
	return parsed, digest, nil
}

func openPrivatePatchSnapshot(snapshot fileSnapshot) (patchformat.Patch, error) {
	if snapshot.SnapshotIdentity == nil {
		return patchformat.Patch{}, fmt.Errorf("private patch snapshot identity is unavailable")
	}
	file, err := os.Open(snapshot.SnapshotPath)
	if err != nil {
		return patchformat.Patch{}, fmt.Errorf("open private patch snapshot: %w", err)
	}
	defer file.Close()
	identity, err := file.Stat()
	if err != nil {
		return patchformat.Patch{}, fmt.Errorf("inspect private patch snapshot: %w", err)
	}
	if !os.SameFile(snapshot.SnapshotIdentity, identity) || identity.Size() < 0 || uint64(identity.Size()) != snapshot.Size {
		return patchformat.Patch{}, fmt.Errorf("private patch snapshot changed before parsing")
	}
	parsed, err := patchformat.Decode(file)
	if err != nil {
		return patchformat.Patch{}, err
	}
	if err := validateDifferentialRanges(parsed, snapshot.Size); err != nil {
		return patchformat.Patch{}, err
	}
	current, err := file.Stat()
	if err != nil {
		return patchformat.Patch{}, fmt.Errorf("inspect private patch snapshot after parsing: %w", err)
	}
	pathInfo, err := os.Lstat(snapshot.SnapshotPath)
	if err != nil {
		return patchformat.Patch{}, fmt.Errorf("inspect private patch snapshot path after parsing: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, current) || !os.SameFile(identity, pathInfo) || current.Size() != identity.Size() {
		return patchformat.Patch{}, fmt.Errorf("private patch snapshot changed while it was being parsed")
	}
	return parsed, nil
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

func verifyOpenPatch(file *os.File, path string, identity os.FileInfo, expectedDigest string, expectedSize uint64) error {
	currentInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect patch after parsing: %w", err)
	}
	if !os.SameFile(identity, currentInfo) || currentInfo.Size() < 0 || uint64(currentInfo.Size()) != expectedSize || !currentInfo.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("patch file changed while it was being parsed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind patch after parsing: %w", err)
	}
	actualDigest, actualSize, err := hashutil.Reader(file)
	if err != nil {
		return fmt.Errorf("verify patch after parsing: %w", err)
	}
	if actualDigest != expectedDigest || actualSize != expectedSize {
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
