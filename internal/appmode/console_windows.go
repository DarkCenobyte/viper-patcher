//go:build windows

package appmode

import "syscall"

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	freeConsole      = kernel32.NewProc("FreeConsole")
	showWindow       = user32.NewProc("ShowWindow")
)

// PrepareGUI hides and detaches the console allocated for a graphical launch.
func PrepareGUI() {
	window, _, _ := getConsoleWindow.Call()
	if window != 0 {
		showWindow.Call(window, 0)
	}
	freeConsole.Call()
}
