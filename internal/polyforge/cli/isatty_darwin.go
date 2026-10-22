//go:build darwin

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// termiosOK answers whether f is an interactive terminal: the TIOCGETA
// ioctl succeeds only on a tty — a pipe, a regular file and /dev/null
// (a char device that is no terminal) all answer ENOTTY, so the mode
// bit alone would misread a redirected stdin as a promptable tty.
func termiosOK(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	return err == nil
}
