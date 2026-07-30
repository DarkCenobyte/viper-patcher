//go:build vipr_legacy_zstd && windows

package zstd

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

var reOpenFileProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

// acquirePositionalReadHandle reopens a reusable private synchronous handle.
// ReadFile may update that handle's cursor while honoring the supplied
// OVERLAPPED offset, but the caller's Go file cursor remains untouched. Decoder
// pools keep one such handle per slot, so payload reads performed by different
// workers are not serialized through one shared synchronous Windows handle.
func acquirePositionalReadHandle(file *os.File) (uintptr, func(), error) {
	if file == nil {
		return 0, nil, fmt.Errorf("positional input file is required")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return 0, nil, fmt.Errorf("access positional input handle: %w", err)
	}

	var reopened windows.Handle
	var reopenError error
	if err := raw.Control(func(handle uintptr) {
		// ReOpenFile accepts FILE_FLAG_* values here, not FILE_ATTRIBUTE_*.
		// No flag keeps this private handle synchronous, which is intentional:
		// ReadFile may advance only this handle's private cursor.
		result, _, callError := reOpenFileProc.Call(
			handle,
			uintptr(windows.GENERIC_READ),
			uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE),
			0,
		)
		reopened = windows.Handle(result)
		if reopened == windows.InvalidHandle {
			reopenError = callError
		}
	}); err != nil {
		return 0, nil, fmt.Errorf("control positional input handle: %w", err)
	}
	if reopenError != nil {
		return 0, nil, fmt.Errorf("reopen positional input handle: %w", reopenError)
	}

	return uintptr(reopened), func() {
		_ = windows.CloseHandle(reopened)
	}, nil
}
