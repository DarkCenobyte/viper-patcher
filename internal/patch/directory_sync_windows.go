//go:build ignore

package patch

import "os"

// Windows has no portable os.Root operation that can flush a directory entry.
// Generated files are still synchronized before they are renamed into place.
func syncRootDirectory(_ *os.Root, _ string) error {
	return nil
}
