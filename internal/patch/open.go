package patch

import (
	"fmt"
	"os"
	"sort"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

// Open reads a patch and validates every differential range against the container size.
func Open(path string) (patchformat.Patch, error) {
	parsed, err := patchformat.Read(path)
	if err != nil {
		return patchformat.Patch{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return patchformat.Patch{}, err
	}
	if info.Size() < 0 {
		return patchformat.Patch{}, fmt.Errorf("patch file has an invalid size")
	}
	containerSize := uint64(info.Size())

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
			return patchformat.Patch{}, fmt.Errorf("invalid forward differential for %q: %w", entry.Path, err)
		}
		if parsed.Header.Reverse {
			if err := add(entry.ReverseOffset, entry.ReverseLength); err != nil {
				return patchformat.Patch{}, fmt.Errorf("invalid reverse differential for %q: %w", entry.Path, err)
			}
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	if len(intervals) == 0 || intervals[0].start != parsed.DataOffset {
		return patchformat.Patch{}, fmt.Errorf("patch contains a gap before its first differential")
	}
	for index := 1; index < len(intervals); index++ {
		if intervals[index].start < intervals[index-1].end {
			return patchformat.Patch{}, fmt.Errorf("patch contains overlapping differential ranges")
		}
		if intervals[index].start != intervals[index-1].end {
			return patchformat.Patch{}, fmt.Errorf("patch contains unreferenced data between differentials")
		}
	}
	if intervals[len(intervals)-1].end != containerSize {
		return patchformat.Patch{}, fmt.Errorf("patch contains trailing unreferenced data")
	}
	return parsed, nil
}
