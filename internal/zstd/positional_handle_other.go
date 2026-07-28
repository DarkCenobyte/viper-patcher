//go:build !windows

package zstd

import (
	"fmt"
	"os"
)

func acquirePositionalReadHandle(file *os.File) (uintptr, func(), error) {
	if file == nil {
		return 0, nil, fmt.Errorf("positional input file is required")
	}
	return file.Fd(), func() {}, nil
}
