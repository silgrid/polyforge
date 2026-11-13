package cli

// verbs_workflow.go: `pf workflows validate` — the workflow contract's
// validator (docs/reference/workflow-contract.md, "When a lane is judged").
// It reads every lane through the one lane loader the daemon runs
// (workflow.Load, and LoadUser for a workspace's user lane): the same
// keys, the same WFC-* codes, file:line and YAML path, every defect at
// once. With no lane directory it judges the lane set of the current
// workspace (--workspace, else the mapped workspace holding the working
// directory): the built-in lanes this machine's daemon runs (its workflows
// dir, else the installed release's own when the daemon does not answer)
// and that workspace's user lanes (.polyforge/workflows/<lane>/). The
// `workflow lanes` doctor row runs the same judgement over every mapped
// workspace and lists the queued work items a claim would refuse because
// their lane is not in their workspace's lane set (WFC-123).
//
// Reachability: CLI only — it reads files and the daemon's GET /status; it
// starts no daemon or aihub entry point a step agent could reach.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// laneReport is one lane's verdict in `pf --json workflows validate`.
type laneReport struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
	// Source is builtin or workspace (a user lane); "" for a lane
	// directory named on the command line outside any user lane root.
	Source string                   `json:"source,omitempty"`
	OK     bool                     `json:"ok"`
	Errors []workflow.ContractError `json:"errors"`
}

// validateReport is `pf --json workflows validate`'s document.
type validateReport struct {
	OK        bool   `json:"ok"`
	Contract  int    `json:"contract"`
	Workspace string `json:"workspace,omitempty"`
	LaneSet   string `json:"lane_set,omitempty"`
	// UserLanes is the judged workspace's user lane root.
	UserLanes string       `json:"user_lanes,omitempty"`
	Lanes     []laneReport `json:"lanes"`
}

// runWorkflowsValidate is `pf workflows validate` (args: what follows
// `validate`).
func runWorkflowsValidate(ctx *Context, fs *flag.FlagSet, args []string) error {
	rest, err := parseCommandArgs(fs, args)
	if err != nil {
		return err
	}
	if stringFlag(fs, "cwd") != "" {
		return fmt.Errorf("workflows validate: --cwd belongs to the listing; name the workspace with --workspace")
	}
	ws := stringFlag(fs, "workspace")
	if ws != "" {
		if st, err := os.Stat(ws); err != nil || !st.IsDir() {
			return fmt.Errorf("workflows validate: --workspace %s is not a directory", ws)
		}
		if ws, err = filepath.Abs(ws); err != nil {
			return fmt.Errorf("workflows validate: %w", err)
		}
	}
	rep := validateReport{Contract: workflow.ContractVersion}
	if len(rest) > 0 {
		if rep.Lanes, err = validateLaneDirs(ctx, rest); err != nil {
			return err
		}
	} else {
		root, m, err := laneSetRoot(ctx)
		if err != nil {
			return fmt.Errorf("workflows validate: %w; pass lane directories", err)
		}
		if ws == "" {
			if cwd, err := osGetwd(); err == nil {
				ws = m.MatchWorkspace(cwd)
			}
		}
		set := workflow.ForWorkspace(root, ws)
		rep.Workspace, rep.LaneSet, rep.UserLanes = ws, root, set.User
		lanes, err := set.Lanes()
		if err != nil {
			return fmt.Errorf("workflows validate: the lane set %s is unreadable: %w", root, err)
		}
		if len(lanes) == 0 {
			return fmt.Errorf("workflows validate: the lane set %s holds no lane", root)
		}
		rep.Lanes = laneReports(lanes)
	}
	rep.OK = true
	for _, l := range rep.Lanes {
		rep.OK = rep.OK && l.OK
	}
	if ctx.JSON {
		if err := emitJSON(ctx, rep); err != nil {
			return err
		}
	} else {
		if rep.LaneSet != "" {
			fmt.Fprintf(ctx.Stdout, "lane set %s (workflow contract %d)\n", rep.LaneSet, rep.Contract)
		}
		if rep.UserLanes != "" {
			fmt.Fprintf(ctx.Stdout, "user lanes %s (workspace %s)\n", rep.UserLanes, rep.Workspace)
		}
		for _, l := range rep.Lanes {
			where := l.Dir
			if l.Source == workflow.SourceWorkspace {
				where += " (user lane)"
			}
			if l.OK {
				fmt.Fprintf(ctx.Stdout, "ok    %s  %s\n", l.Name, where)
				continue
			}
			fmt.Fprintf(ctx.Stdout, "fail  %s  %s (%d contract error(s))\n", l.Name, where, len(l.Errors))
			for _, e := range l.Errors {
				fmt.Fprintf(ctx.Stdout, "      %s\n", e.Error())
			}
		}
	}
	if !rep.OK {
		return fmt.Errorf("workflows validate: lanes with contract errors — see the report above")
	}
	return nil
}

// validateLaneDirs judges the lane directories named on the command line:
// one inside a workspace's user lane root (<ws>/.polyforge/workflows/
// <lane>) as that workspace's user lane — over the built-in lanes the
// daemon runs (laneSetRoot), WFC-122 included — every other as a built-in
// lane, with the shared tree beside it. Every directory named is judged
// and reported: one under a user lane root that is no lane of the set (a
// missing directory, a typo, a `_`-prefixed one such as a `_shared` a
// workspace cannot have) is WFC-110, as a missing directory is anywhere
// else — never an empty, passing report.
func validateLaneDirs(ctx *Context, dirs []string) ([]laneReport, error) {
	out := make([]laneReport, 0, len(dirs))
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("workflows validate: %w", err)
		}
		ws, ok := userLaneWorkspace(abs)
		if !ok {
			lane, errs := workflow.Load(d)
			out = append(out, reportLane(workflow.SetLane{Name: filepath.Base(filepath.Clean(d)), Dir: d, Lane: lane, Errors: errs}))
			continue
		}
		root, _, err := laneSetRoot(ctx)
		if err != nil {
			return nil, fmt.Errorf("workflows validate: %s is a user lane of %s, judged over the built-in lanes: %w", d, ws, err)
		}
		lanes, err := workflow.ForWorkspace(root, ws).UserLanes()
		if err != nil {
			return nil, fmt.Errorf("workflows validate: the user lanes of %s are unreadable: %w", ws, err)
		}
		found := false
		for _, sl := range lanes {
			if sl.Dir == abs {
				out = append(out, reportLane(sl))
				found = true
			}
		}
		if !found {
			out = append(out, reportLane(notAUserLane(root, abs)))
		}
	}
	return out, nil
}

// notAUserLane judges a directory under a workspace's user lane root that
// the lane set does not list: a `_`-prefixed one is no lane (WFC-110: the
// set never runs it, and a workspace has no shared tree of its own); any
// other — missing, not a directory — is read by the user lane loader over
// the built-in shared tree, which names what is missing (WFC-110 for an
// absent workflow.yaml).
func notAUserLane(root, dir string) workflow.SetLane {
	name := filepath.Base(dir)
	sl := workflow.SetLane{Name: name, Dir: dir, Source: workflow.SourceWorkspace}
	if strings.HasPrefix(name, "_") {
		sl.Errors = []workflow.ContractError{{Code: workflow.CodeLaneFile, File: dir,
			Msg: "a `_`-prefixed directory under a workspace's user lanes is no lane — the lane set never runs it, and a workspace has no shared tree of its own (a user lane's definitions and fragments fall back on the built-in " + workflow.SharedDir + "/); rename it to a lane name or remove it"}}
		return sl
	}
	sl.Lane, sl.Errors = workflow.LoadUser(dir, filepath.Join(root, workflow.SharedDir))
	return sl
}

// userLaneWorkspace answers the workspace whose user lane root holds the
// lane directory dir (absolute, cleaned), if it is one.
func userLaneWorkspace(dir string) (string, bool) {
	root := filepath.Dir(dir)
	ws := filepath.Dir(filepath.Dir(root))
	return ws, workflow.UserLanesRoot(ws) == root
}

// laneReports renders the loader's verdict on every lane of a set.
func laneReports(lanes []workflow.SetLane) []laneReport {
	out := make([]laneReport, 0, len(lanes))
	for _, sl := range lanes {
		out = append(out, reportLane(sl))
	}
	return out
}

// reportLane renders the loader's verdict on one lane.
func reportLane(sl workflow.SetLane) laneReport {
	errs := sl.Errors
	if errs == nil {
		errs = []workflow.ContractError{}
	}
	return laneReport{Name: sl.Name, Dir: sl.Dir, Source: sl.Source, OK: len(errs) == 0, Errors: errs}
}

// laneSetRoot answers the built-in lanes root a validation judges against
// — the running daemon's workflows dir, else the installed release's — and
// the config's workspace mapping (empty when the config does not load).
func laneSetRoot(ctx *Context) (string, *mapping.Plan, error) {
	m := &mapping.Plan{}
	cfg, err := LoadConfig(ctx.ConfigPath)
	if err == nil {
		ctx.Cfg = cfg
		if cfg.Mapping != nil {
			m = cfg.Mapping
		}
		if f := readDaemonFacts(ctx); f.reachable && f.workflowsDir != "" {
			return f.workflowsDir, m, nil
		}
	}
	home, _ := os.UserHomeDir()
	release := filepath.Join(DataDir(home), "release", "current", "workflows")
	if st, err := os.Stat(release); err == nil && st.IsDir() {
		return release, m, nil
	}
	return "", nil, fmt.Errorf("no lane set — the daemon does not answer and no release is installed at %s", release)
}

// laneMissing is one work item a claim refuses because its lane is not in
// its workspace's lane set (WFC-123).
type laneMissing struct {
	ID     string `json:"id"`
	Slug   string `json:"slug,omitempty"`
	Bundle string `json:"bundle"`
	Code   string `json:"code"`
}

// projectLaneSets answers the lane set of a project's work items: the
// built-in lanes under root and the user lanes of the workspace the
// mapping maps the project to.
func projectLaneSets(root string, m *mapping.Plan) func(project string) workflow.LaneSet {
	return func(project string) workflow.LaneSet {
		ws, err := m.WorkspaceFor(project)
		if err != nil {
			ws = ""
		}
		return workflow.ForWorkspace(root, ws)
	}
}

// laneResolver answers, over the daemon facts f and the workspace mapping
// m, the lane a project's work item bound to bundle runs on (its project's
// lane set, projectLaneSets). nil when the lane cannot be read — the
// daemon does not answer or names no workflows dir, the lane is not in
// the set or its load is refused — which a caller reads as a lane
// requiring acceptance ((*workflow.Lane).RequiresAcceptance).
func laneResolver(f daemonFacts, m *mapping.Plan) func(project, bundle string) *workflow.Lane {
	return func(project, bundle string) *workflow.Lane {
		if !f.reachable || f.workflowsDir == "" {
			return nil
		}
		lane, _, err := projectLaneSets(f.workflowsDir, m)(project).Load(bundle)
		if err != nil {
			return nil
		}
		return lane
	}
}

// laneOf is laneResolver over one fresh read of the daemon's facts.
func laneOf(ctx *Context, project, bundle string) *workflow.Lane {
	return laneResolver(readDaemonFacts(ctx), ctx.Cfg.Mapping)(project, bundle)
}

// missingLanes answers the queued items of items bound to a lane their
// project's lane set (setOf) does not carry, in id order.
func missingLanes(setOf func(project string) workflow.LaneSet, items []WIView) []laneMissing {
	var out []laneMissing
	for _, it := range items {
		if it.Status != api.StatusQueued || it.BundleName == nil || *it.BundleName == "" {
			continue
		}
		if _, _, ok := setOf(it.ProjectName).Resolve(*it.BundleName); !ok {
			out = append(out, laneMissing{ID: it.ID, Slug: it.Slug, Bundle: *it.BundleName, Code: workflow.CodeLaneMissing})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// missingLanesText renders the WFC-123 rows as one line.
func missingLanesText(rows []laneMissing) string {
	parts := make([]string, 0, len(rows))
	for _, m := range rows {
		name := m.Slug
		if name == "" {
			name = m.ID
		}
		parts = append(parts, fmt.Sprintf("%s (lane %s)", name, m.Bundle))
	}
	return fmt.Sprintf("%s: %d queued work item(s) bound to a lane outside their workspace's lane set — a claim refuses them and they stay queued: %s; re-bind each to a lane of the set (pf file edit <wi> --bundle <lane>) or cancel it",
		workflow.CodeLaneMissing, len(rows), strings.Join(parts, ", "))
}

// workflowLanesCheck is the `workflow lanes` doctor row: the daemon's
// built-in lanes load through the one lane loader (fail naming every
// defect: a release defect), every mapped workspace's user lanes load
// (warn naming every defect: only that lane is unavailable), and no queued
// work item of a mapped project is bound to a lane its workspace's lane
// set does not carry (warn listing each, WFC-123). skip while the daemon
// does not answer (its built-in lanes are unknown).
func workflowLanesCheck(r *doctorRun) (string, string) {
	if !r.sockOK {
		return "skip", "not run: the daemon does not answer, so its lane set is unknown (`pf workflows validate <lane-dir>` judges a lane directly)"
	}
	root := r.status.WorkflowsDir
	if root == "" {
		return "skip", "not run: the daemon reports no workflows dir"
	}
	builtin, err := (workflow.LaneSet{Builtin: root}).Lanes()
	if err != nil {
		return "fail", fmt.Sprintf("the lane set %s is unreadable: %v", root, err)
	}
	var bad []string
	for _, l := range builtin {
		for _, e := range l.Errors {
			bad = append(bad, e.Error())
		}
	}
	if len(bad) > 0 {
		return "fail", fmt.Sprintf("%d contract error(s) in the lane set %s (contract %d): %s — `pf workflows validate` prints them; a claim of a work item on such a lane is refused",
			len(bad), root, workflow.ContractVersion, strings.Join(bad, "; "))
	}
	ok := fmt.Sprintf("%d lane(s) in %s load under workflow contract %d", len(builtin), root, workflow.ContractVersion)
	var warns, userBad []string
	userOK := 0
	for _, ws := range r.cfg.Mapping.Workspaces() {
		lanes, err := workflow.ForWorkspace(root, ws).UserLanes()
		if err != nil {
			userBad = append(userBad, fmt.Sprintf("the user lanes of %s are unreadable: %v", ws, err))
			continue
		}
		for _, l := range lanes {
			if len(l.Errors) == 0 {
				userOK++
				continue
			}
			for _, e := range l.Errors {
				userBad = append(userBad, e.Error())
			}
		}
	}
	if userOK > 0 {
		ok += fmt.Sprintf(", and %d user lane(s) of the mapped workspaces", userOK)
	}
	if len(userBad) > 0 {
		warns = append(warns, fmt.Sprintf("%d contract error(s) in the mapped workspaces' user lanes: %s — only such a lane is unavailable (a claim of a work item on it is refused); `pf workflows validate --workspace <dir>` prints them", len(userBad), strings.Join(userBad, "; ")))
	}
	if !r.reachOK {
		if len(warns) > 0 {
			return "warn", ok + "; " + strings.Join(warns, "; ")
		}
		return "pass", ok + "; the queued work items were not read: " + r.reachMsg
	}
	client := newProxyClient(r.cfg.DaemonSocket, "")
	var items []WIView
	var unread []string
	for _, p := range r.cfg.Mapping.Projects() {
		raw, err := client.ListWorkItemsRaw(p, api.StatusQueued, 0)
		var l itemList[WIView]
		if err == nil {
			err = json.Unmarshal(raw, &l)
		}
		if err != nil {
			unread = append(unread, fmt.Sprintf("%s (%v)", p, err))
			continue
		}
		items = append(items, l.Items...)
	}
	if rows := missingLanes(projectLaneSets(root, r.cfg.Mapping), items); len(rows) > 0 {
		warns = append(warns, missingLanesText(rows))
	}
	if len(warns) > 0 {
		return "warn", ok + "; " + strings.Join(warns, "; ")
	}
	if len(unread) > 0 {
		return "pass", ok + "; no queued work item on a missing lane among the projects read — not read: " + strings.Join(unread, ", ")
	}
	return "pass", ok + "; no queued work item is bound to a missing lane"
}
