package runtime

// stepenv.go: the daemon-owned part of every step process's environment
// (workflow contract v2, orchestration O-2 "step environment"). The daemon
// binds the live source (Config.StepEnv): with [daemon] command_path set,
// the step process's PATH is that command PATH — the one the daemon's own
// in-process git and gh run under (daemon/stepenv.go) — behind the
// directories of the daemon's own pf and of the step's harness binary, so
// a command a step runs resolves what the daemon resolves and the step's
// `pf step …` and the harness's own interpreter resolve whatever the key
// holds; unset, the step keeps the PATH it inherits (the daemon's own).
// PF_PYTHON is the interpreter the step's scripts run under. Both harness
// adapters add the variables (stepEnvAdditions) next to the
// harness-neutral tool window.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// StepEnv is the daemon-owned step environment.
type StepEnv struct {
	// CommandPath is the live [daemon] command_path ("" = unset: a step
	// keeps the PATH it inherits, the daemon's own).
	CommandPath string
	// PfDir is the directory of the daemon's own pf executable, symlinks
	// resolved ("" = unknown): first on a step's PATH when CommandPath is
	// set, so the step's `pf step …` resolves.
	PfDir string
	// Python is the interpreter a step's scripts run under
	// (spawn.EnvPython): the daemon resolved it once at its start —
	// [daemon] python, else python3 on its PATH — and checked its version.
	Python string
}

// Vars answers the harness-neutral entries of the step env: PF_PYTHON (an
// empty value included: a set name with an empty value is still the
// daemon's answer).
func (e StepEnv) Vars() []string {
	return []string{spawn.EnvPython + "=" + e.Python}
}

// StepPath answers the PATH a step process of the harness binary
// harnessBin starts with: "" when CommandPath is unset (the step keeps the
// PATH it inherits), else PfDir, the harness binary's directory
// (HarnessDir: an npm or nvm install keeps the harness's `node` there) and
// CommandPath's entries, each once, in that order (JoinPath) — so neither
// the step's `pf` nor the harness's interpreter is lost to a command_path
// that lacks them.
func (e StepEnv) StepPath(harnessBin string) string {
	if e.CommandPath == "" {
		return ""
	}
	return JoinPath(e.PfDir, HarnessDir(harnessBin), e.CommandPath)
}

// HarnessDir answers the directory of the harness binary bin as the
// daemon starts it — resolved on the daemon's own PATH (exec.LookPath),
// symlinks kept, made absolute — or "" when it does not resolve.
func HarnessDir(bin string) string {
	if bin == "" {
		return ""
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Dir(p)
}

// JoinPath joins PATH values into one, each entry once (the first
// occurrence kept, order kept, empty entries dropped).
func JoinPath(paths ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		for _, dir := range filepath.SplitList(p) {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// stepEnvAdditions answers the step-environment entries of one derivation:
// the daemon's step env when the runtime binds a source (Config.StepEnv;
// a bare Config adds none) — its PATH composed for the harness binary
// harnessBin (StepEnv.StepPath) — the places the step's scripts run from (the
// capsule root, spawn.EnvCapsule; the lane snapshot and the shared tree
// beside it, spawn.EnvLaneDir / spawn.EnvSharedDir, when the derivation
// names its lane), the round the derivation belongs to (spawn.EnvRound; 0
// = not stated) and the dispatch identity `pf ask` records its question
// under (spawn.EnvStep / spawn.EnvSpawn; a derivation that names no step
// states neither).
func stepEnvAdditions(cfg *Config, p SpawnParams, harnessBin string) []string {
	var out []string
	if cfg.StepEnv != nil {
		e := cfg.StepEnv()
		out = append(out, e.Vars()...)
		if path := e.StepPath(harnessBin); path != "" {
			out = append(out, "PATH="+path)
		}
	}
	if p.CapsuleDir != "" {
		out = append(out, spawn.EnvCapsule+"="+p.CapsuleDir)
	}
	if p.LaneDir != "" {
		out = append(out, spawn.EnvLaneDir+"="+p.LaneDir, spawn.EnvSharedDir+"="+workflow.SiblingShared(p.LaneDir))
	}
	if p.Round > 0 {
		out = append(out, spawn.EnvRound+"="+strconv.FormatInt(p.Round, 10))
	}
	if p.StepID != "" && p.SpawnN > 0 {
		out = append(out, spawn.EnvStep+"="+p.StepID, spawn.EnvSpawn+"="+strconv.Itoa(p.SpawnN))
	}
	return out
}

// stepToolWindow answers the supervision tool window a derivation runs
// under: the step's own when its lane declares one (Params.ToolWindow),
// else the daemon's global window (Config.ToolWindow; <= 0 resolves to
// the supervision default downstream).
func stepToolWindow(cfg *Config, p SpawnParams) time.Duration {
	if p.ToolWindow > 0 {
		return p.ToolWindow
	}
	return cfg.ToolWindow
}
