//go:build !windows

package zstd

import (
	"fmt"
	"os"
)

// acquirePositionalReadHandle reuses the caller's descriptor. Native reads use
// pread, so concurrent segments do not share or mutate a file cursor.
func acquirePositionalReadHandle(file *os.File) (uintptr, func(), error) {
	if file == nil {
		return 0, nil, fmt.Errorf("positional input file is required")
	}
	return file.Fd(), func() {}, nil
}
