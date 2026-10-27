package cli

// config_workspace.go: the workspace `.polyforge.yaml` read/write surface
// (workbench#22). Reads are live (no cache): the cwd→workspace resolution is
// mapping.Plan.MatchWorkspace, the file is parsed by the one strict
// workspace loader, and the declared step candidates come from the installed
// bundles the daemon reports. Writes are byte-preserving line edits of the
// summarize block, serialized by the same sibling flock discipline as the
// cli.toml writer and validated by workspace.Load before anything lands.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
		return emitJSON(ctx, map[string]any{"mapped": false, "workspace": nil, "yaml": nil, "summarize": nil, "steps": nil, "steps_complete": false})
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

	summarize := map[string]any{
		"models": workspaceValue{
			Key: "summarize.models", Value: workspace.SummarizeModels(cfg),
			Source: sourceIf(cfg != nil && cfg.Summarize != nil), Class: WorkspaceConfigClass("summarize.models"), Schema: WorkspaceConfigSchema("summarize.models"),
		},
		"language": workspaceValue{
			Key: "summarize.language", Value: workspace.SummarizeLanguage(cfg),
			Source: sourceIf(cfg != nil && cfg.SummarizeLanguage != ""), Class: WorkspaceConfigClass("summarize.language"), Schema: WorkspaceConfigSchema("summarize.language"),
		},
		"interval_sec": workspaceValue{
			Key: "summarize.interval_sec", Value: workspace.SummarizeIntervalSec(cfg),
			Source: sourceIf(cfg != nil && cfg.SummarizeIntervalSec != 0), Class: WorkspaceConfigClass("summarize.interval_sec"), Schema: WorkspaceConfigSchema("summarize.interval_sec"),
		},
	}

	steps := declaredStepCandidates(cfg, projects, workflowsDir, stepsComplete)
	out := map[string]any{
		"mapped":         true,
		"workspace":      ws,
		"yaml":           yamlFact,
		"summarize":      summarize,
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
	raw, err := ctx.configDaemon().Status()
	if err != nil {
		return "", false, "the daemon is not reachable, so the installed bundle step ids are unknown; only the steps the file declares are listed"
	}
	var st struct {
		WorkflowsDir string `json:"workflows_dir"`
	}
	if json.Unmarshal(raw, &st) != nil || st.WorkflowsDir == "" {
		return "", false, "the daemon reports no workflows dir, so the installed bundle step ids are unknown; only the steps the file declares are listed"
	}
	return st.WorkflowsDir, true, ""
}

func declaredStepCandidates(cfg *workspace.Config, projects []string, workflowsDir string, complete bool) map[string]any {
	steps := map[string]any{}
	for _, project := range projects {
		declared := map[string][]string(nil)
		if cfg != nil {
			declared = cfg.Models[project]
		}
		entry := map[string]any{}
		if complete && workflowsDir != "" {
			ids, err := workflow.StepIDs(workflowsDir)
			if err == nil {
				for id := range ids {
					entry[id] = candidateEntry(declared, id)
				}
			}
		}
		for id, models := range declared {
			if _, ok := entry[id]; !ok {
				entry[id] = map[string]any{"declared": true, "models": models}
			}
		}
		steps[project] = entry
	}
	return steps
}

func candidateEntry(declared map[string][]string, id string) map[string]any {
	if models, ok := declared[id]; ok {
		return map[string]any{"declared": true, "models": models}
	}
	return map[string]any{"declared": false, "models": nil}
}
