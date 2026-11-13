package daemon

// stepenv.go: the step environment the daemon owns (runtime.StepEnv) and
// the command PATH it shares with the daemon's own in-process commands.
// With [daemon] command_path set, every step process starts with that PATH
// behind the directories of this daemon's own pf and of the step's harness
// binary (runtime.StepEnv.StepPath), and the git and gh the daemon runs
// in-process (commandEnv: the forge binding's) run under the key's value;
// unset, both keep the daemon process's own PATH. So a step's scripts
// resolve what the daemon resolves, and its `pf step …` and its harness's
// interpreter resolve whatever the key holds.
//
// Reachability: daemon code only — no route serves it and no step agent
// can reach it (GET /status reports the step PATH, step_path).

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// stepEnv answers the daemon-owned step environment (runtime.StepEnv,
// bound as the runtime's Config.StepEnv): the live command_path ("" =
// unset), this daemon's pf directory and the Python interpreter the daemon
// resolved at its start (Config.Python), the step scripts' PF_PYTHON.
func (d *Daemon) stepEnv() runtime.StepEnv {
	d.reloadMu.Lock()
	p := d.commandPath
	d.reloadMu.Unlock()
	return runtime.StepEnv{CommandPath: p, PfDir: d.pfDir, Python: d.cfg.Python}
}

// selfDir answers the directory of this process's executable, symlinks
// resolved ("" when it cannot be known): the daemon's own pf.
func selfDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// stepPathNow answers the PATH a step process starts with, before its
// harness binary's directory (GET /status step_path): the composed one
// when command_path is set, else the daemon process's own PATH.
func (d *Daemon) stepPathNow() string {
	if p := d.stepEnv().StepPath(""); p != "" {
		return p
	}
	return os.Getenv("PATH")
}

// commandEnv answers the environment the daemon's in-process commands
// start from: the sanitized step environment with PATH set to the
// effective command PATH (commandPathNow).
func (d *Daemon) commandEnv() []string {
	env := runtime.SanitizedEnv(d.cfg.APIKey)
	p := d.commandPathNow()
	if p == "" {
		return env
	}
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+p)
}

// commandPathNow answers the PATH the daemon's in-process commands run
// under: the live command_path, else the daemon process's own PATH.
func (d *Daemon) commandPathNow() string {
	d.reloadMu.Lock()
	p := d.commandPath
	d.reloadMu.Unlock()
	if p == "" {
		return os.Getenv("PATH")
	}
	return p
}
