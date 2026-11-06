package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_host_requires.go: `pf doctor`'s "host prerequisites" row. A
// repository's verification rules may need tools on the host that are
// the repository's requirement, not the platform's: its fast gate runs on
// the host too (build's fast checks, review's host replay, the micro
// lane). Such a repository declares each one in its
// .polyforge/verify-scope.conf as `host_requires=<name>|<probe>|<hint>`;
// the scope engine lists the declarations in its `--json` report
// (`host_requires`), and this row asks the engine — the daemon's lane
// set's own, under its workflows dir — for them in every mapped
// workspace's .repo clone that carries a conf at its root, then runs each
// probe with `bash -c` from the clone's root, stdin closed, under the PATH
// a step runs under (the daemon's step_path: the fast gate runs there).
// A probe exiting 0 passes and the detail shows its first non-empty output
// line (a version); one exiting non-zero or outlasting its timeout fails
// the row, naming the repository, the prerequisite and its install hint.
// The doctor installs nothing. An engine that cannot list a clone's
// declarations is a warning (the prerequisites are then unknown, never a
// faked verdict). The doctor parses no conf and names no prerequisite of
// its own: every one comes from a repository's declaration. The row is
// skipped while the daemon is unreachable or reports no workflows dir or
// step PATH — the engine and the PATH are then unknown, never guessed.

// docChHostRequires is the row name.
const docChHostRequires = "host prerequisites"

// hostRequiresProbeTimeout bounds one probe and one engine call.
const hostRequiresProbeTimeout = 30 * time.Second

// hostRequiresKillDelay bounds the wait for a killed probe's output
// pipes (a process outside its group may still hold them).
const hostRequiresKillDelay = time.Second

// hostRequiresCheck is the ladder's wrapper: the skip judgement and the
// row's arguments from the daemon's status.
func hostRequiresCheck(r *doctorRun) (string, string) {
	switch {
	case !r.sockOK:
		return "skip", "not run: the daemon is unreachable, so the scope engine its lane set ships and the PATH its steps run under are unknown (`pf daemon ensure`, then re-run the doctor)"
	case r.status.WorkflowsDir == "":
		return "skip", "not run: the daemon reports no workflows dir, so its scope engine is unknown"
	case r.status.StepPath == "":
		return "skip", "not run: the daemon reports no step PATH, so the PATH a probe must run under is unknown"
	}
	engine := filepath.Join(r.status.WorkflowsDir, "_shared", "scripts", "verify-scope")
	return hostRequiresRow(r.present, engine, r.status.StepPath, hostRequiresProbeTimeout).row()
}

// hostRequire is one declaration as the engine lists it.
type hostRequire struct {
	Name  string `json:"name"`
	Probe string `json:"probe"`
	Hint  string `json:"hint"`
}

// hostRequiresRow probes every host prerequisite the .repo clones of the
// given workspaces declare: engine is the scope engine's script, stepPath
// the PATH the probes run under, timeout one probe's bound.
func hostRequiresRow(workspaces []string, engine, stepPath string, timeout time.Duration) doctorCheck {
	c := doctorCheck{Name: docChHostRequires}
	var met, unmet, notes []string
	declared := 0
	for _, ws := range workspaces {
		entries, err := os.ReadDir(filepath.Join(ws, workspace.ReposDir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			clone := workspace.RepoCloneDir(ws, e.Name())
			if !e.IsDir() || !workspace.IsRepoClone(clone) {
				continue
			}
			if st, err := os.Stat(filepath.Join(clone, workspace.GeneratedDir, "verify-scope.conf")); err != nil || !st.Mode().IsRegular() {
				continue
			}
			who := fmt.Sprintf("`%s` (%s)", e.Name(), ws)
			reqs, err := hostRequiresOf(engine, clone)
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s: the scope engine could not list its host prerequisites: %v", who, err))
				continue
			}
			for _, q := range reqs {
				declared++
				line, verdict := probeHostRequire(clone, stepPath, q.Probe, timeout)
				if verdict == "" {
					met = append(met, fmt.Sprintf("%s: %s — %s", who, q.Name, line))
					continue
				}
				unmet = append(unmet, fmt.Sprintf("%s: %s — probe `%s` %s — install: %s", who, q.Name, q.Probe, verdict, q.Hint))
			}
		}
	}
	satisfied := ""
	if len(met) > 0 {
		satisfied = "; satisfied: " + strings.Join(met, "; ")
	}
	switch {
	case len(unmet) > 0:
		c.Status = "fail"
		c.Detail = "unmet under the step PATH, probed from each clone's root: " + strings.Join(unmet, "; ") +
			" — the doctor installs nothing: install each by hand (the repository's fast gate fails on this host until then), then re-run the doctor"
		if len(notes) > 0 {
			c.Detail += "; also: " + strings.Join(notes, "; ")
		}
		c.Detail += satisfied
	case len(notes) > 0:
		c.Status = "warn"
		c.Detail = strings.Join(notes, "; ") + " — those prerequisites are unknown" + satisfied
	case declared == 0:
		c.Status = "pass"
		c.Detail = "no mapped repo declares a host prerequisite (`host_requires=` in its .polyforge/verify-scope.conf)"
	default:
		c.Status = "pass"
		c.Detail = "probed under the step PATH from each clone's root: " + strings.Join(met, "; ")
	}
	return c
}

// hostRequiresOf asks the engine for the clone's declarations: `--base
// HEAD` leaves the diff empty, so the engine only reads its conf.
func hostRequiresOf(engine, clone string) ([]hostRequire, error) {
	var stdout, stderr bytes.Buffer
	err := runBounded(hostRequiresProbeTimeout, clone, nil, &stdout, &stderr, "sh", engine, "--json", "--base", "HEAD")
	if err != nil {
		if msg := lastLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	var doc struct {
		HostRequires []hostRequire `json:"host_requires"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		return nil, fmt.Errorf("its --json report does not decode: %w", err)
	}
	return doc.HostRequires, nil
}

// probeHostRequire runs one probe from the clone's root under stepPath:
// a met probe answers its first non-empty output line and an empty
// verdict; an unmet one answers the verdict (how it exited, with that
// line).
func probeHostRequire(clone, stepPath, probe string, timeout time.Duration) (string, string) {
	var out bytes.Buffer
	env := append(os.Environ(), "PATH="+stepPath)
	err := runBounded(timeout, clone, env, &out, &out, "bash", "-c", probe)
	line := firstNonEmptyLine(out.String())
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		if line == "" {
			line = "(no output)"
		}
		return line, ""
	case errors.Is(err, context.DeadlineExceeded):
		return line, fmt.Sprintf("timed out after %s", timeout)
	case errors.As(err, &exitErr):
		v := fmt.Sprintf("exited %d", exitErr.ExitCode())
		if line != "" {
			v += ": " + line
		}
		return line, v
	default:
		return line, fmt.Sprintf("could not run: %v", err)
	}
}

// runBounded runs argv in dir (env nil = the doctor's own) with stdin
// closed, killing its whole process group at the timeout; a timeout
// answers an error wrapping context.DeadlineExceeded.
func runBounded(timeout time.Duration, dir string, env []string, stdout, stderr *bytes.Buffer, argv ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = hostRequiresKillDelay
	err := cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", context.DeadlineExceeded, err)
	}
	return err
}

// firstNonEmptyLine answers s's first line holding more than blanks.
func firstNonEmptyLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// lastLine answers s's last line holding more than blanks.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
