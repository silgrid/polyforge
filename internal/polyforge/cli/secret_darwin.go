//go:build darwin

package cli

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// readSecretLine reads one line from a terminal with echo disabled.
func readSecretLine(f *os.File) (string, error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return "", err
	}
	neu := *old
	neu.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &neu); err != nil {
		return "", err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, unix.TIOCSETA, old) }()
	fmt.Fprint(os.Stderr, "api_key (input hidden): ")
	line, err := readOneLine(f)
	fmt.Fprintln(os.Stderr)
	return line, err
}
