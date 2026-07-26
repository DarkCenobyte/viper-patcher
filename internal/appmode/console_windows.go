//go:build windows

package appmode

import (
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	user32                = syscall.NewLazyDLL("user32.dll")
	getConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	freeConsole           = kernel32.NewProc("FreeConsole")
	showWindow            = user32.NewProc("ShowWindow")
)

// PrepareGUI hides a console allocated only for this process and then detaches
// from it. A terminal shared with a parent shell is never hidden.
func PrepareGUI() {
	var processIDs [2]uint32
	count, _, _ := getConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&processIDs[0])),
		uintptr(len(processIDs)),
	)
	if shouldHideConsole(uint32(count)) {
		window, _, _ := getConsoleWindow.Call()
		if window != 0 {
			showWindow.Call(window, 0)
		}
	}
	freeConsole.Call()
}
