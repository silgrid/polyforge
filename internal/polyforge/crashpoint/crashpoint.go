// Package crashpoint is the deterministic kill -9 of the crash tests: At
// SIGKILLs the process when the PF_CRASH_AT environment variable names the
// point it is called with, so a test can kill the daemon at an exact
// step of its work and assert what the restart makes of the state left
// behind. With the variable unset At does nothing.
package crashpoint

import (
	"os"
	"syscall"
)

// EnvVar names the crash point the process dies at.
const EnvVar = "PF_CRASH_AT"

var at = os.Getenv(EnvVar)

// At kills the process (SIGKILL: no deferred function, no flush runs)
// when point is the configured crash point.
func At(point string) {
	if at == "" || at != point {
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
