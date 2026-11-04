package cli

// config_workspace.go: the workspace `.polyforge.yaml` read/write surface
//. Reads are live (no cache): the cwd→workspace resolution is
// mapping.Plan.MatchWorkspace, the file is parsed by the one strict
// workspace loader, and the declared step candidates come from the installed
// bundles the daemon reports. The read carries the mapping, the projects and
// the step chains/attrs only — summarize and memory_policy are cli.toml
// facts read through `pf config list`. Writes are
// byte-preserving line edits of the steps block, serialized by the same sibling flock discipline as the
// cli.toml writer and validated by workspace.Load before anything lands.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func runConfigWorkspace(ctx *Context, args []string) error {
	cwd := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cwd":
			if i+1 >= len(args) {
				return fmt.Errorf("config workspace: --cwd needs a directory")
			}
			cwd = args[i+1]
			i++
		default:
			return fmt.Errorf("config workspace: unexpected argument %q (only --cwd <dir>)", args[i])
		}
	}
	if cwd == "" {
		return fmt.Errorf("config workspace: --cwd <dir> is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("config workspace: %w", err)
	}
	v, err := loadConfigView(ctx)
	if err != nil {
		return err
	}
	ws := v.eff.Mapping.MatchWorkspace(abs)
	if ws == "" {
		return emitJSON(ctx, map[string]any{"mapped": false, "workspace": nil, "yaml": nil, "steps": nil, "steps_complete": false})
	}
	out, err := readWorkspaceConfig(ctx, v, ws)
	if err != nil {
		return err
	}
	// This verb is the panel's machine face: it always answers one JSON
	// document (with or without the global --json flag), like the other
	// read verbs the panel spawns.
	return emitJSON(ctx, out)
}

type workspaceValue struct {
	Key    string         `json:"key,omitempty"`
	Value  any            `json:"value"`
	Source string         `json:"source"`
	Class  ConfigClass    `json:"class,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
}

func readWorkspaceConfig(ctx *Context, v *configView, ws string) (map[string]any, error) {
	projects := v.eff.Mapping.ProjectsOf(ws)
	workflowsDir, stepsComplete, reason := workspaceCandidateScope(ctx)
	cfg, err := workspace.Load(ws, workflowsDir, projects)
	if err != nil {
		return nil, err
	}
	path := workspace.PathFor(ws)
	raw, rerr := os.ReadFile(path)
	exists := rerr == nil
	if rerr != nil && !os.IsNotExist(rerr) {
		return nil, fmt.Errorf("workspace %s: %w", path, rerr)
	}
	yamlFact := map[string]any{"path": path, "exists": exists}
	if exists {
		yamlFact["sha256"] = sha256Hex(raw)
	}

	steps := declaredStepCandidates(cfg, projects, workflowsDir, stepsComplete)
	out := map[string]any{
		"mapped":         true,
		"workspace":      ws,
		"yaml":           yamlFact,
		"steps":          steps,
		"steps_complete": stepsComplete,
	}
	if reason != "" {
		out["steps_reason"] = reason
	}
	return out, nil
}

func sourceIf(fromFile bool) string {
	if fromFile {
		return "file"
	}
	return "default"
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// workspaceCandidateScope answers the installed bundles' workflows dir from
// the daemon (the one machine fact the CLI cannot reconstruct locally) and
// whether the candidate list is complete.
func workspaceCandidateScope(ctx *Context) (dir string, complete bool, reason string) {
	f := readDaemonFacts(ctx)
	if !f.reachable {
		return "", false, "the daemon is not reachable, so the installed bundle step ids are unknown; only the steps the file declares are listed"
	}
	if f.workflowsDir == "" {
		return "", false, "the daemon reports no workflows dir, so the installed bundle step ids are unknown; only the steps the file declares are listed"
	}
	return f.workflowsDir, true, ""
}

// declaredStepCandidates answers the per-project step map of the read
// face: every installed step id (when the daemon named the workflows
// dir), every key the file declares, and always the project-level
// `default` and the bundle-less `task` rows. Each entry carries
// `declared` + `models` (a declared-empty list stays declared, never
// absent), its `source` (`file` | `default`) and `class` (`live`), and
// its `attrs`: every attr an installed bundle declares for the step id
// (the declaration's default when the file does not override it) plus
// every override the file declares.
func declaredStepCandidates(cfg *workspace.Config, projects []string, workflowsDir string, complete bool) map[string]any {
	steps := map[string]any{}
	var ids map[string]bool
	var decls map[string]map[string]workflow.AttrDecl
	if complete && workflowsDir != "" {
		ids, _ = workflow.StepIDs(workflowsDir)
		decls, _ = workflow.StepAttrs(workflowsDir)
	}
	for _, project := range projects {
		var declared map[string][]string
		var overrides map[string]map[string]any
		if cfg != nil {
			declared, overrides = cfg.Models[project], cfg.Attrs[project]
		}
		keys := map[string]bool{workspace.DefaultKey: true, workspace.TaskKey: true}
		for id := range ids {
			keys[id] = true
		}
		for id := range declared {
			keys[id] = true
		}
		for id := range overrides {
			keys[id] = true
		}
		entry := map[string]any{}
		for id := range keys {
			entry[id] = stepEntryRead(project, id, declared, overrides[id], decls[id])
		}
		steps[project] = entry
	}
	return steps
}

func stepEntryRead(project, id string, declared map[string][]string, overrides map[string]any, decls map[string]workflow.AttrDecl) map[string]any {
	models, ok := declared[id]
	e := map[string]any{"declared": ok, "models": nil, "source": sourceIf(ok), "class": WorkspaceConfigClass("steps." + project + "." + id + ".models")}
	if ok {
		e["models"] = models
	}
	attrs := map[string]any{}
	for name, d := range decls {
		attrs[name] = attrRead(project, id, name, overrides, &d)
	}
	for name := range overrides {
		if _, ok := decls[name]; !ok {
			attrs[name] = attrRead(project, id, name, overrides, nil)
		}
	}
	if len(attrs) > 0 {
		e["attrs"] = attrs
	}
	return e
}

func attrRead(project, id, name string, overrides map[string]any, d *workflow.AttrDecl) map[string]any {
	v, ok := overrides[name]
	a := map[string]any{"declared": ok, "source": sourceIf(ok), "class": WorkspaceConfigClass("steps." + project + "." + id + ".attrs." + name)}
	switch {
	case ok:
		a["value"] = v
	case d != nil:
		a["value"] = d.Default
	}
	if d != nil {
		a["default"] = d.Default
		a["schema"] = map[string]any{"kind": d.Type}
	}
	return a
}

func orList(declared, fallback []string) []string {
	if declared != nil {
		return declared
	}
	return fallback
}
