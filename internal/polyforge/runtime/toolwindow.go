package runtime

// toolwindow.go: the harness-neutral step binding for the daemon's
// supervision tool window. Every step process must be able
// to derive a whole-run deadline below the tool rule, not only claude
// (whose BASH_MAX_TIMEOUT_MS = Y - 1m is the shell-call maximum). The
// value resolves the same way the supervisor's fill does, so a daemon
// started without --tool-window hands out the 30m default.

import (
	"strconv"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func toolWindowEnv(toolWindow time.Duration) string {
	window := supervise.Defaults{Tool: toolWindow}.Fill().Tool
	return spawn.EnvToolWindow + "=" + strconv.FormatInt(window.Milliseconds(), 10)
}
