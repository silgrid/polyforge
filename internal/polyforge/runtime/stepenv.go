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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// CellTarget is one serving Cell's target facts (polyforge#76): its
// profile, its AIHub base URL as configured and the deploy configuration
// file that Cell's deploys read. One row of the all-Cells list a step's
// deploy procedure derives its deploy targets from and checks each target
// against - every serving Cell of this machine, never only the work
// item's own.
type CellTarget struct {
	Profile   string
	Server    string
	DeployEnv string
}

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
	// Cell is the name of the Cell the step's work item belongs to
	// (spawn.EnvCell; polyforge#75): "default" for the Host's own Cell,
	// else the named Cell's profile. The daemon answers it from its own
	// Cell (d.profile); the step env is the one source every step process
	// reads it from.
	Cell string
	// CellServer is this Cell's AIHub base URL as configured
	// (spawn.EnvCellServer): the deploy target a step's deploy procedure
	// must reach — the one server this Cell's identity domain holds.
	CellServer string
	// DeployEnv is the deploy configuration file this Cell's deploys read
	// (spawn.EnvDeployEnv): an absolute path in the Host configuration
	// directory - deploy.env for the default Cell, deploy-<profile>.env for
	// a named one (cellcfg.DeployEnvFileName, the Cell file naming).
	DeployEnv string
	// CellTargets is every serving Cell's target facts
	// (spawn.EnvCellTargets; polyforge#76), the default Cell first then the
	// named ones by profile: a deploy procedure derives its deploy targets
	// from this list - every Cell of this machine that has a deploy
	// configuration file is one - and checks each target against its own
	// row. The daemon's source is the Host registry, read live at every
	// derivation (a Cell added, removed or turned invalid moves the next
	// step derivation with it); a bare Config names no source and answers
	// none.
	CellTargets []CellTarget
	// ContainerEngine is the container backend a verification container
	// runs on (polyforge#159, 84-engine): the daemon-resolved [daemon]
	// container_engine — "docker" or "podman". Empty = the daemon stated
	// none (a hand run, a bare test Config): the variable is then absent
	// and the scripts' own default, docker, applies — an empty value is
	// never injected (the selection refuses one). Host-only, read once at
	// the daemon's start (never a live reload).
	ContainerEngine string
}

// Vars answers the harness-neutral entries of the step env: PF_PYTHON (an
// empty value included: a set name with an empty value is still the
// daemon's answer), the step's Cell facts (polyforge#75) - PF_CELL,
// PF_CELL_SERVER and PF_DEPLOY_ENV - and the all-Cells target facts
// (polyforge#76): PF_CELL_TARGETS, one line per serving Cell of this
// machine, "<profile>\t<server>\t<deploy configuration file>" in the
// registry's order (default first, then by profile). Empty values are
// included the same way, so a step can always tell a stated fact from an
// inherited copy (the sanitize point strips those).
func (e StepEnv) Vars() []string {
	var lines []string
	for _, t := range e.CellTargets {
		lines = append(lines, t.Profile+"\t"+t.Server+"\t"+t.DeployEnv)
	}
	return []string{
		spawn.EnvPython + "=" + e.Python,
		spawn.EnvCell + "=" + e.Cell,
		spawn.EnvCellServer + "=" + e.CellServer,
		spawn.EnvDeployEnv + "=" + e.DeployEnv,
		spawn.EnvCellTargets + "=" + strings.Join(lines, "\n"),
	}
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
// states neither) and the step's params at their final values
// (spawn.EnvParamName each, polyforge#78).
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
	for _, name := range slices.Sorted(maps.Keys(p.Params)) {
		out = append(out, spawn.EnvParamName(name)+"="+p.Params[name])
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
