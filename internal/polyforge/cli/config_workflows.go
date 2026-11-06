package cli

// config_workflows.go: `pf workflows [--cwd <dir>]` — the read-only
// workflow face. It parses the installed bundle files the
// daemon runs from on every call (no cache) and answers one JSON document:
// each bundle's steps in declared order with their candidates, a broken
// bundle in bundle_errors (never failing the verb), and —
// when --cwd resolves a mapped workspace — every mapped project's effective
// chain per step through workspace.ResolveSteps (the precedence chain's one
// place). Every degradation is a field: an unreachable daemon or an invalid
// .polyforge.yaml still exits 0 with a reason. Reach: the local operator
// and the pi-web panel through `pf`; a step agent is refused like every
// management verb (stepctx.go), and no daemon or aihub route is added.

import (
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

var cmdWorkflows = &Command{
	Name:    "workflows",
	Group:   GroupMgmt,
	Summary: "List the installed workflow lanes (read-only JSON), or validate lanes against the workflow contract",
	Usage: "pf workflows [--cwd <dir>]\n" +
		"pf [--json] workflows validate [--workspace <dir>] [<lane-dir>…]\n\n" +
		"The listing: every installed lane's steps, candidates and each mapped project's\n" +
		"effective chain (with --cwd, the workspace's user lanes too).\n" +
		"validate: the daemon's own lane loader judges each lane (its workflow.yaml; a lane\n" +
		"still a workflow.md is refused with its rewrite) — with no lane directory,\n" +
		"the lane set of the workspace (--workspace, else the mapped workspace holding the\n" +
		"working directory): the built-in lanes the daemon runs (its workflows dir; the\n" +
		"installed release's when the daemon does not answer) and the workspace's user\n" +
		"lanes (.polyforge/workflows/<lane>/). Every defect prints with its WFC code,\n" +
		"file:line and YAML path; any defect exits 1.",
	NoConfigLoad: true,
	Setup: func(fs *flag.FlagSet) {
		fs.String("cwd", "", "a directory inside a mapped workspace: adds its projects' effective chains")
		fs.String("workspace", "", "validate: the workspace whose lane set (built-in + its user lanes) is judged (a directory)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) > 0 && args[0] == "validate" {
			return runWorkflowsValidate(ctx, fs, args[1:])
		}
		if stringFlag(fs, "workspace") != "" {
			return fmt.Errorf("workflows: --workspace belongs to `pf workflows validate`")
		}
		return runWorkflows(ctx, fs, args)
	},
}

// daemonFacts is the part of one GET /status read the workspace faces use.
type daemonFacts struct {
	reachable    bool
	workflowsDir string
}

// readDaemonFacts reads the daemon's status once (a failed read answers
// reachable=false; nothing is cached).
func readDaemonFacts(ctx *Context) daemonFacts {
	raw, err := ctx.configDaemon().Status()
	if err != nil {
		return daemonFacts{}
	}
	var st struct {
		WorkflowsDir string `json:"workflows_dir"`
	}
	_ = json.Unmarshal(raw, &st)
	return daemonFacts{reachable: true, workflowsDir: st.WorkflowsDir}
}

// stepsWritable answers whether a steps.* write may land: the running
// daemon must be reachable and name its workflows dir (the lanes a
// written step id is checked against). reason names why not.
func stepsWritable(f daemonFacts) (bool, string) {
	if !f.reachable {
		return false, "the daemon is not reachable, so the step keys cannot be checked against its installed workflows; start it (pf daemon ensure) and retry"
	}
	if f.workflowsDir == "" {
		return false, "the daemon reports no workflows dir, so the step keys cannot be checked"
	}
	return true, ""
}

type workflowsDoc struct {
	WorkflowsDir    string                    `json:"workflows_dir"`
	DaemonReachable bool                      `json:"daemon_reachable"`
	Reason          string                    `json:"reason"`
	Workspace       *string                   `json:"workspace"`
	Projects        []string                  `json:"projects"`
	WriteCapable    bool                      `json:"write_capable"`
	WriteReason     string                    `json:"write_reason"`
	Task            map[string]effectiveChain `json:"task,omitempty"`
	Bundles         []workflowBundle          `json:"bundles"`
	BundleErrors    []workflowBundleError     `json:"bundle_errors"`
}

type workflowBundle struct {
	Name string `json:"name"`
	// Source is where the lane comes from: builtin (the daemon's release)
	// or workspace (a user lane of the --cwd workspace).
	Source   string           `json:"source"`
	Defaults workflowDefaults `json:"defaults"`
	Steps    []workflowStep   `json:"steps"`
}

type workflowDefaults struct {
	ModelCandidates []string `json:"model_candidates"`
	// Effective is the project-level fallback per mapped project: the
	// yaml's steps.<project>.default.models, else the bundle defaults.
	Effective map[string]effectiveChain `json:"effective,omitempty"`
}

type workflowStep struct {
	ID              string                    `json:"id"`
	ModelCandidates []string                  `json:"model_candidates,omitempty"`
	Effective       map[string]effectiveChain `json:"effective,omitempty"`
	// Skippable: the lane declares the step `skippable: true`
	//; Skip names each mapped project whose yaml
	// switches it off — through workspace.SkippedSteps, so a skip the
	// yaml writes for a step its lane does not declare skippable never
	// shows. Skip is set only when --cwd maps a workspace.
	Skippable bool            `json:"skippable,omitempty"`
	Skip      map[string]bool `json:"skip,omitempty"`
}

type effectiveChain struct {
	Models []string `json:"models"`
	From   string   `json:"from"`
}

type workflowBundleError struct {
	Bundle string `json:"bundle"`
	Reason string `json:"reason"`
}

func runWorkflows(ctx *Context, fs *flag.FlagSet, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("workflows: unexpected arguments %v (only --cwd <dir>, or `validate`)", args)
	}
	doc := workflowsDoc{Projects: []string{}, Bundles: []workflowBundle{}, BundleErrors: []workflowBundleError{}}
	if cwd := stringFlag(fs, "cwd"); cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return fmt.Errorf("workflows: %w", err)
		}
		v, err := loadConfigView(ctx)
		if err != nil {
			return err
		}
		if ws := v.eff.Mapping.MatchWorkspace(abs); ws != "" {
			doc.Workspace = &ws
			doc.Projects = append(doc.Projects, v.eff.Mapping.ProjectsOf(ws)...)
		}
	}
	ws := ""
	if doc.Workspace != nil {
		ws = *doc.Workspace
	}
	fms := fillWorkflows(&doc, readDaemonFacts(ctx), ws)
	if doc.Workspace != nil && doc.WorkflowsDir != "" {
		cfg, err := workspace.Load(*doc.Workspace, doc.Projects)
		if err != nil {
			doc.Reason = err.Error()
		} else {
			fillEffective(&doc, fms, cfg)
		}
	}
	return emitJSON(ctx, doc)
}

// fillWorkflows sets the daemon facts and the parsed bundles of doc — the
// lane set of the workspace ws (the built-in lanes alone for "") — and
// answers the parsed frontmatter by bundle name.
func fillWorkflows(doc *workflowsDoc, facts daemonFacts, ws string) map[string]*workflow.Lane {
	doc.DaemonReachable = facts.reachable
	doc.WriteCapable, doc.WriteReason = stepsWritable(facts)
	switch {
	case !facts.reachable:
		doc.Reason = "the daemon is not reachable, so its installed workflows dir is unknown; start it (pf daemon ensure)"
		return nil
	case facts.workflowsDir == "":
		doc.Reason = "the daemon reports no workflows dir"
		return nil
	}
	doc.WorkflowsDir = facts.workflowsDir
	set := workflow.ForWorkspace(facts.workflowsDir, ws)
	fms, bad, err := workspace.Bundles(set, "")
	if err != nil {
		doc.Reason = fmt.Sprintf("the workflows dir %s is unreadable: %v", facts.workflowsDir, err)
		return nil
	}
	for _, name := range sortedKeys(fms) {
		fm := fms[name]
		_, source, _ := set.Resolve(name)
		b := workflowBundle{Name: name, Source: source, Steps: []workflowStep{}}
		if fm.Defaults != nil {
			b.Defaults.ModelCandidates = fm.Defaults.ModelCandidates
		}
		for _, s := range fm.Steps {
			b.Steps = append(b.Steps, workflowStep{ID: s.ID, ModelCandidates: s.ModelCandidates, Skippable: s.Skippable})
		}
		doc.Bundles = append(doc.Bundles, b)
	}
	for _, name := range sortedKeys(bad) {
		doc.BundleErrors = append(doc.BundleErrors, workflowBundleError{Bundle: name, Reason: bad[name].Error()})
	}
	return fms
}

// fillEffective adds every mapped project's effective chains through the
// one precedence implementation (workspace.ResolveSteps, TaskModels): per
// step, the bundle's project-level fallback (`default` — resolved as a
// step that declares no list of its own) and the bundle-less task lane.
// An entry is absent when no level governs (the harness default runs).
// It also marks the steps each project skips through
// workspace.SkippedSteps, the judgment the claim fixes.
func fillEffective(doc *workflowsDoc, fms map[string]*workflow.Lane, cfg *workspace.Config) {
	set := func(m *map[string]effectiveChain, project string, list []string, from string) {
		if from == "" {
			return
		}
		if *m == nil {
			*m = map[string]effectiveChain{}
		}
		(*m)[project] = effectiveChain{Models: nonNil(list), From: from}
	}
	for i := range doc.Bundles {
		b := &doc.Bundles[i]
		fm := fms[b.Name]
		fallback := &workflow.Lane{Defaults: fm.Defaults, Steps: []workflow.Step{{ID: workspace.DefaultKey}}}
		for _, project := range doc.Projects {
			for j, r := range workspace.ResolveSteps(b.Name, fm, cfg, project, nil) {
				set(&b.Steps[j].Effective, project, r.Models, r.ModelsFrom)
			}
			r := workspace.ResolveSteps(b.Name, fallback, cfg, project, nil)[0]
			set(&b.Defaults.Effective, project, r.Models, r.ModelsFrom)
			skipped := map[string]bool{}
			for _, id := range workspace.SkippedSteps(cfg, fm, project) {
				skipped[id] = true
			}
			for j := range b.Steps {
				if skipped[b.Steps[j].ID] {
					if b.Steps[j].Skip == nil {
						b.Steps[j].Skip = map[string]bool{}
					}
					b.Steps[j].Skip[project] = true
				}
			}
		}
	}
	for _, project := range doc.Projects {
		list, from := workspace.TaskModels(cfg, project)
		set(&doc.Task, project, list, from)
	}
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
