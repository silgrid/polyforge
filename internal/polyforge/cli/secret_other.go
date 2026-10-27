//go:build !linux && !darwin

package cli

import (
	"fmt"
	"os"
)

// readSecretLine is the fallback without a termios constant wired here:
// it reads the line plainly (the value is still never echoed by pf).
func readSecretLine(f *os.File) (string, error) {
	fmt.Fprint(os.Stderr, "api_key: ")
	return readOneLine(f)
}
