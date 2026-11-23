//go:build linux

package platform

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal: the TCGETS ioctl succeeds only
// on one, where a character-device check would also accept /dev/null.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
