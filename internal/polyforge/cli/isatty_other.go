//go:build !linux && !darwin

package cli

import "os"

// termiosOK is the fallback for platforms without a termios constant
// wired here: the char-device mode bit, the best stdlib-only
// approximation (it also answers true for /dev/null — a char device
// that is no terminal).
func termiosOK(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
