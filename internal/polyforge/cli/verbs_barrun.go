package cli

// verbs_barrun.go: `pf bar-run -- '<carried test_bar line>'`
// — the deliver step's one way to re-run a carried test_bar command so the
// run outlives the step process. It runs the line with `bash -c` in the
// current directory (which must be the task repo the line was carried for,
// <capsule>/repos/<name>), its output on this command's stdout/stderr and
// its stdin closed, then posts {argv: [line], cwd, exit, duration_ms} to the
// daemon's /step/bar-run, which reads the repo HEAD itself, stamps the
// calling spawn and appends the entry to the work item's bar-run record.
// The exit status is the command's own; a run whose record could not be
// posted is a loud error (the command did run, but no credential is
// stored). Reachable inside a deliver step only: the daemon refuses every
// other caller and every other step.

import (
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var cmdBarRun = &Command{
	Name:    "bar-run",
	Group:   GroupExec,
	Summary: "Re-run one carried test_bar command and record it with the daemon (inside a deliver step only)",
	Usage:   "pf bar-run -- '<one carried test_bar command line>'",
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return fmt.Errorf("bar-run: expected exactly one command line after -- (quote the carried test_bar command as one argument)")
		}
		if ctx.Step == "" {
			return fmt.Errorf("bar-run: runs inside a workflow deliver step only (PF_WI_ID and PF_DAEMON_ID are not set)")
		}
		return barRun(ctx, args[0])
	},
}

// exitStatusError ends the invocation with the run command's own exit
// status (exitCode honours it).
type exitStatusError struct {
	code int
	msg  string
}

func (e *exitStatusError) Error() string { return e.msg }

// barRun runs line in the current directory and records it.
func barRun(ctx *Context, line string) error {
	cwd, err := osGetwd()
	if err != nil {
		return fmt.Errorf("bar-run: cwd unreadable: %w", err)
	}
	cmd := exec.Command("bash", "-c", line)
	cmd.Dir = cwd
	cmd.Stdout, cmd.Stderr = ctx.Stdout, ctx.Stderr
	started := time.Now()
	runErr := cmd.Run()
	ms := time.Since(started).Milliseconds()
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return fmt.Errorf("bar-run: the command could not start (nothing recorded): %w", runErr)
		}
		if code = ee.ExitCode(); code == 0 {
			code = -1 // killed by a signal
		}
	}
	if _, err := ctx.daemon().do("POST", "/step/bar-run", "", map[string]any{
		"argv": []string{line}, "cwd": cwd, "exit": code, "duration_ms": ms,
	}, nil); err != nil {
		return fmt.Errorf("bar-run: the command ran (exit %d) but its run was NOT recorded — no credential is stored, run it again once the daemon accepts it: %v", code, err)
	}
	fmt.Fprintf(ctx.Stderr, "pf bar-run: recorded exit %d (%d ms) in %s\n", code, ms, cwd)
	if code != 0 {
		st := code
		if st < 0 || st > 255 {
			st = 1
		}
		return &exitStatusError{code: st, msg: fmt.Sprintf("bar-run: the command exited %d (recorded)", code)}
	}
	return nil
}
