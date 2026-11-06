package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_command_path.go: `pf doctor`'s "daemon command path" row. Every
// step process — and with it the scripts a step runs (verify.py and its
// scope engine) and the step's own `pf step …` — runs under the step PATH
// the daemon composes: with [daemon] command_path set, the daemon's own pf
// directory, the step's harness binary's directory and the key's value;
// unset, the daemon process's own PATH, which under `pf daemon ensure` is
// no interactive shell's. The row shows that PATH as the daemon reports it
// (GET /status step_path: the composition before the harness's directory)
// and resolves under it `pf` and the engine's own tools: git always, go
// when a mapped workspace's .repo clone is a Go repo for the engine (its
// `go list`) that runs on the host — a clone carrying
// .polyforge/Dockerfile.verify runs its engine and bar in a container whose
// image provides go, so the row names it instead of asking for go on this
// PATH. A tool that does not resolve is a warning naming the tool and the
// PATH; the row never fails (the prerequisites precedent) and is skipped
// while the daemon is unreachable — its PATH is then unknown, never
// guessed.

// docChCommandPath is the row name.
const docChCommandPath = "daemon command path"

// commandPathCheck is the row body.
func commandPathCheck(r *doctorRun) (string, string) {
	var host, boxed []string
	for _, clone := range goEngineClones(r.present) {
		if carriesVerifyDockerfile(clone) {
			boxed = append(boxed, clone)
		} else {
			host = append(host, clone)
		}
	}
	return daemonCommandPathCheck(r.sockOK, r.status.StepPath, host, boxed).row()
}

// goEngineClones answers the .repo clones of the given workspaces the
// engine analyzes as Go (it runs `go list` there): a `stack=go` conf pin,
// or no pin and go.mod the one supported manifest (the engine's own
// stack detection: confStackPin, manifestStacks).
func goEngineClones(workspaces []string) []string {
	var out []string
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
			pin := confStackPin(filepath.Join(clone, workspace.GeneratedDir, "verify-scope.conf"))
			stacks := manifestStacks(clone)
			if pin == "go" || pin == "" && slices.Equal(stacks, []string{"go"}) {
				out = append(out, clone)
			}
		}
	}
	return out
}

// daemonCommandPathCheck judges the step PATH: pf and every engine tool
// resolve under it (go only when goRepos, the Go repos verified on the
// host, is non-empty; boxedGoRepos verify in their container, whose image
// provides go).
func daemonCommandPathCheck(daemonUp bool, path string, goRepos, boxedGoRepos []string) doctorCheck {
	c := doctorCheck{Name: docChCommandPath}
	if !daemonUp {
		c.Status, c.Detail = "skip", "not run: the daemon is unreachable, so the PATH its steps run under is unknown (`pf daemon ensure`, then re-run the doctor)"
		return c
	}
	tools := []string{"pf", "git"}
	why := map[string]string{"pf": "a step's `pf step …`", "git": "the engine's diff"}
	if len(goRepos) > 0 {
		tools = append(tools, "go")
		why["go"] = "the engine's `go list` for " + strings.Join(capRows(goRepos), ", ")
	}
	var found, missing []string
	for _, tool := range tools {
		if p := lookPathIn(tool, path); p != "" {
			found = append(found, fmt.Sprintf("`%s` → %s", tool, p))
		} else {
			missing = append(missing, fmt.Sprintf("`%s` does not resolve (needed by %s)", tool, why[tool]))
		}
	}
	head := fmt.Sprintf("the PATH a step runs under (its scripts, the verification's scope engine and its `pf step …`; the harness binary's directory goes in front) is %q", path)
	if len(boxedGoRepos) > 0 {
		head += fmt.Sprintf("; `go` for %s is not needed on it: those repos run their engine and bar in their verification container (%s), whose image provides it", strings.Join(capRows(boxedGoRepos), ", "), dockerfileVerify)
	}
	if len(missing) > 0 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%s: %s — a step's commands exit 127 (a missing tool of the engine's makes it fall back to the full tier); set `[daemon] command_path` (`pf config set command_path <dir:dir:...>`) to a PATH that holds them — the daemon puts its own pf's directory in front of it — or start the daemon with them on its PATH (docs/reference/config.md)", head, strings.Join(missing, "; "))
		if len(found) > 0 {
			c.Detail += "; resolves: " + strings.Join(found, ", ")
		}
		return c
	}
	c.Status, c.Detail = "pass", head+"; resolves: "+strings.Join(found, ", ")
	return c
}

// dockerfileVerify is the file a repo's verification container is built
// from: a clone carrying it verifies in that container.
const dockerfileVerify = ".polyforge/Dockerfile.verify"

// carriesVerifyDockerfile reports whether the clone's working tree carries
// the Dockerfile.
func carriesVerifyDockerfile(clone string) bool {
	st, err := os.Stat(filepath.Join(clone, filepath.FromSlash(dockerfileVerify)))
	return err == nil && st.Mode().IsRegular()
}

// lookPathIn answers where tool resolves on the PATH value path ("" =
// nowhere): the first entry holding an executable regular file of that
// name.
func lookPathIn(tool, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, tool)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// confStackPin answers a verify-scope.conf's first valid `stack=` pin (go,
// npm, python, rust or none; an invalid pin is ignored, as in the engine);
// a missing or unreadable file pins nothing. The file is only read.
func confStackPin(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimLeft(strings.TrimSuffix(line, "\r"), " \t\r\v\f")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, val, ok := strings.Cut(line, "="); ok && key == "stack" {
			switch v := strings.TrimSpace(val); v {
			case "go", "npm", "python", "rust", "none":
				return v
			}
		}
	}
	return ""
}

// manifestStacks answers the analyzers a clone's supported manifests select,
// in the engine's detection order.
func manifestStacks(clone string) []string {
	var stacks []string
	has := func(name string) bool { _, err := os.Stat(filepath.Join(clone, name)); return err == nil }
	if has("go.mod") {
		stacks = append(stacks, "go")
	}
	if has("package.json") {
		stacks = append(stacks, "npm")
	}
	if has("pyproject.toml") || has("requirements.txt") {
		stacks = append(stacks, "python")
	}
	if has("Cargo.toml") {
		stacks = append(stacks, "rust")
	}
	return stacks
}
