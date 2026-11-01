package cli

// config_workflows.go: `pf workflows [--cwd <dir>]` — the read-only
// workflow face. It parses the installed bundle files the
// daemon runs from on every call (no cache) and answers one JSON document:
// each bundle's steps in declared order with their candidates and declared
// attrs, a broken bundle in bundle_errors (never failing the verb), and —
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
	Name:         "workflows",
	Group:        GroupMgmt,
	Summary:      "List the installed workflow bundles: steps, candidates, declared attrs and each mapped project's effective chain (read-only JSON)",
	Usage:        "pf workflows [--cwd <dir>]",
	NoConfigLoad: true,
	Setup: func(fs *flag.FlagSet) {
		fs.String("cwd", "", "a directory inside a mapped workspace: adds its projects' effective chains")
	},
	Run: runWorkflows,
}

// StepsAttrsMinimumVersion is the pf/daemon release whose loaders accept
// step attrs (the floor a refusal names).
const StepsAttrsMinimumVersion = "2.20.15"

// stepsAttrsCapabilityReason is the refusal of a step-key write (and the
// read face's write_reason) while the running daemon lacks the capability.
const stepsAttrsCapabilityReason = "the running daemon does not accept step attrs in .polyforge.yaml; this schema needs pf/daemon " + StepsAttrsMinimumVersion + " or newer — restart the daemon after upgrading"

// daemonFacts is the part of one GET /status read the workspace faces use.
type daemonFacts struct {
	reachable    bool
	workflowsDir string
	capabilities []string
}

// readDaemonFacts reads the daemon's status once (a failed read answers
// reachable=false; nothing is cached).
func readDaemonFacts(ctx *Context) daemonFacts {
	raw, err := ctx.configDaemon().Status()
	if err != nil {
		return daemonFacts{}
	}
	var st struct {
		WorkflowsDir string   `json:"workflows_dir"`
		Capabilities []string `json:"capabilities"`
	}
	_ = json.Unmarshal(raw, &st)
	return daemonFacts{reachable: true, workflowsDir: st.WorkflowsDir, capabilities: st.Capabilities}
}

// stepsWriteCapability answers whether a steps.* write may land: the
// running daemon must be reachable (its workflows dir is what the strict
// loader checks step ids and attrs against) and report
// CapabilityStepsAttrs. reason names the mismatch when it may not.
func stepsWriteCapability(f daemonFacts) (bool, string) {
	if !f.reachable {
		return false, "the daemon is not reachable, so the step keys cannot be checked against its installed workflows; start it (pf daemon ensure) and retry"
	}
	for _, c := range f.capabilities {
		if c == CapabilityStepsAttrs {
			if f.workflowsDir == "" {
				return false, "the daemon reports no workflows dir, so the step keys cannot be checked"
			}
			return true, ""
		}
	}
	return false, stepsAttrsCapabilityReason
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
	Name     string           `json:"name"`
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
	Attrs           []workflowAttr            `json:"attrs,omitempty"`
	Effective       map[string]effectiveChain `json:"effective,omitempty"`
}

type workflowAttr struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default any    `json:"default"`
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
		return fmt.Errorf("workflows: unexpected arguments %v (only --cwd <dir>)", args)
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
	fms := fillWorkflows(&doc, readDaemonFacts(ctx))
	if doc.Workspace != nil && doc.WorkflowsDir != "" {
		cfg, err := workspace.Load(*doc.Workspace, doc.WorkflowsDir, doc.Projects)
		if err != nil {
			doc.Reason = err.Error()
		} else {
			fillEffective(&doc, fms, cfg)
		}
	}
	return emitJSON(ctx, doc)
}

// fillWorkflows sets the daemon facts and the parsed bundles of doc and
// answers the parsed frontmatter by bundle name.
func fillWorkflows(doc *workflowsDoc, facts daemonFacts) map[string]*workflow.Frontmatter {
	doc.DaemonReachable = facts.reachable
	doc.WriteCapable, doc.WriteReason = stepsWriteCapability(facts)
	switch {
	case !facts.reachable:
		doc.Reason = "the daemon is not reachable, so its installed workflows dir is unknown; start it (pf daemon ensure)"
		return nil
	case facts.workflowsDir == "":
		doc.Reason = "the daemon reports no workflows dir"
		return nil
	}
	doc.WorkflowsDir = facts.workflowsDir
	fms, bad, err := workspace.Bundles(facts.workflowsDir, "")
	if err != nil {
		doc.Reason = fmt.Sprintf("the workflows dir %s is unreadable: %v", facts.workflowsDir, err)
		return nil
	}
	for _, name := range sortedKeys(fms) {
		fm := fms[name]
		b := workflowBundle{Name: name, Steps: []workflowStep{}}
		if fm.Defaults != nil {
			b.Defaults.ModelCandidates = fm.Defaults.ModelCandidates
		}
		for _, s := range fm.Steps {
			st := workflowStep{ID: s.ID, ModelCandidates: s.ModelCandidates}
			for _, n := range sortedKeys(s.Attrs) {
				st.Attrs = append(st.Attrs, workflowAttr{Name: n, Type: s.Attrs[n].Type, Default: s.Attrs[n].Default})
			}
			b.Steps = append(b.Steps, st)
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
func fillEffective(doc *workflowsDoc, fms map[string]*workflow.Frontmatter, cfg *workspace.Config) {
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
		fallback := &workflow.Frontmatter{Defaults: fm.Defaults, Steps: []workflow.FrontmatterStep{{ID: workspace.DefaultKey}}}
		for _, project := range doc.Projects {
			for j, r := range workspace.ResolveSteps(b.Name, fm, cfg, project, nil) {
				set(&b.Steps[j].Effective, project, r.Models, r.ModelsFrom)
			}
			r := workspace.ResolveSteps(b.Name, fallback, cfg, project, nil)[0]
			set(&b.Defaults.Effective, project, r.Models, r.ModelsFrom)
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
