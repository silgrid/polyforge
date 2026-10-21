// Package supervisor names the daemon's supervision on a work machine
// (polyforge#174) and answers whether it is in place. It is the leaf both
// `pf daemon ensure` (which creates the supervision) and `pf doctor`
// (which reports it) share, so the two can never disagree on the name.
//
// The supervision is a tmux session running a restart loop around
// `pf daemon` (crashes are restarted within ~5s). tmux does not survive a
// container or machine restart; the harness adapters run
// `pf daemon ensure` at session start, which recreates the loop. An
// operator who prefers systemd may run the daemon from a unit named
// SystemdUnit instead; doctor accepts either.
//
// Every tmux call runs with TMUX and TMUX_PANE removed from the
// environment, so it always talks to the user's default tmux server
// (keyed by TMUX_TMPDIR and the uid), never to whatever server the
// calling shell happens to sit in.
package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SessionName is the tmux session that hosts the daemon restart loop.
const SessionName = "polyforge-daemon"

// SystemdUnit is the unit name doctor accepts as an alternative
// supervision (ensure never creates it).
const SystemdUnit = "polyforge-daemon.service"

// MarkerEnv is set to "tmux:<session>" in the environment of a daemon
// the loop starts (diagnostics: /proc/<pid>/environ shows who started it).
const MarkerEnv = "POLYFORGE_SUPERVISOR"

// ErrNoTmux reports that no tmux binary is on PATH.
var ErrNoTmux = errors.New("tmux not found on PATH")

// TmuxEnv answers the environment every tmux invocation runs with: the
// caller's, minus TMUX and TMUX_PANE.
func TmuxEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// Tmux runs one tmux command (bounded) and answers its combined output.
func Tmux(args ...string) (string, error) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return "", ErrNoTmux
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = TmuxEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// LoopRunning reports whether the tmux session hosting the restart loop
// exists. ErrNoTmux when tmux is not installed.
func LoopRunning() (bool, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return false, ErrNoTmux
	}
	// "=name" is an exact match (a bare name would also match a prefix).
	_, err := Tmux("has-session", "-t", "="+SessionName)
	return err == nil, nil
}

// SystemdActive reports whether SystemdUnit is active (false when there
// is no systemctl or no such unit).
func SystemdActive() bool {
	bin, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "is-active", "--quiet", SystemdUnit).Run() == nil
}
