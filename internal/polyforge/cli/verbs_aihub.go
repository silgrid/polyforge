package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ---------------------------------------------------------------------------
// file — create/edit/show work items (direct to aihub).
// ---------------------------------------------------------------------------

var cmdFile = &Command{
	Name:    "file",
	Group:   GroupData,
	Summary: "Create, edit, show or comment on a work item; manage its dependencies",
	Usage: "pf file create --project P --goal G [--repo NAME]... [--after ID]... [flags] | --stdin\n" +
		"pf file edit <id> [flags] | --stdin\n" +
		"pf file comment <id> --body TEXT [--reply-to COMMENT]\n" +
		"pf file show <id>\n" +
		"pf file dep add <blocking> <blocked>\n" +
		"pf file dep rm <blocking> <blocked>\n" +
		"pf file dep list <id>\n" +
		"pf file analysis confirm <id>\n" +
		"pf file analysis redo <id>\n\n" +
		"`pf file analysis redo <id>` re-schedules a queued or blocked\n" +
		"execution ticket's pre-submit analysis under the current prompt. The\n" +
		"old analysis (旧分析) is kept verbatim in the ticket's `## 历次分析` section; the\n" +
		"new draft stays unconfirmed until `pf file analysis confirm <id>`. A\n" +
		"claimed, running or terminal ticket, or one with a running analysis\n" +
		"task, is refused. The strong guarantee covers this machine's daemon\n" +
		"only; another machine relies on the unconfirmed marker.\n\n" +
		"Dependencies are `blocks` edges inside one project: the blocked work\n" +
		"item is not claimed until every blocker is WRAPPED. A blocker must be\n" +
		"an execution ticket (bundle bound). A failed or canceled blocker keeps\n" +
		"it waiting — remove the edge to proceed. --after is atomic: a bad\n" +
		"blocker creates nothing.\n\n" +
		"Comments are append-only: each carries author, server time, body and an\n" +
		"optional reply_to on the SAME work item. There is no edit and no delete.",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("file: expected create|edit|comment|show|dep (see `pf help file`)")
		}
		sub, rest := args[0], args[1:]
		switch sub {
		case "create":
			return fileCreate(ctx, rest)
		case "edit":
			return fileEdit(ctx, rest)
		case "comment":
			return fileComment(ctx, rest)
		case "show":
			return fileShow(ctx, rest)
		case "dep":
			return fileDep(ctx, rest)
		case "analysis":
			return fileAnalysis(ctx, rest)
		default:
			return fmt.Errorf("file: unknown action %q", sub)
		}
	},
}

// fileAnalysis serves the analysis section's owner gate:
//
//	pf file analysis confirm <id>
//	pf file analysis redo <id>
//
// `confirm` is the one command that removes an unconfirmed auto-analysis
// marker (polyforge#72, ruling 9): after it the auto-pull wave may claim
// the ticket again. `redo` (polyforge#233) asks this machine's daemon to
// re-schedule the analysis under the current prompt for a queued or
// blocked execution ticket — the old section is archived verbatim in
// `## 历次分析` and the new draft stays unconfirmed until the owner
// confirms it. Both keep the draft itself untouched; every write carries
// the content CAS so a concurrent edit elsewhere is never overwritten.
func fileAnalysis(ctx *Context, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("file analysis: expected `confirm <id>` or `redo <id>` — the owner's confirmation of the auto-scheduled analysis, or a re-scheduling of a stale one (see `pf help file`)")
	}
	switch args[0] {
	case "confirm":
		return fileAnalysisConfirm(ctx, args[1])
	case "redo":
		return fileAnalysisRedo(ctx, args[1])
	default:
		return fmt.Errorf("file analysis: unknown action %q — expected `confirm <id>` or `redo <id>` (see `pf help file`)", args[0])
	}
}

// fileAnalysisConfirm is `pf file analysis confirm <id>`.
func fileAnalysisConfirm(ctx *Context, name string) error {
	wi, err := ctx.aihub().GetWorkItem(name)
	if err != nil {
		return err
	}
	content := ""
	if wi.Content != nil {
		content = *wi.Content
	}
	confirmed, changed := workflow.ConfirmAnalysisSection(content)
	if !changed {
		return fmt.Errorf("file analysis confirm %s: no unconfirmed auto-analysis marker on the ticket — nothing to confirm (`pf file show %s` to read the section)", api.DisplayName(wi.Slug, wi.ID), name)
	}
	raw, err := ctx.aihub().PatchWorkItem(wi.ID, map[string]any{
		"content":             confirmed,
		"expected_updated_at": wi.UpdatedAt,
	})
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(v *WIView) {
		fmt.Fprintf(ctx.Stdout, "已确认分析（标记已去除；自动领取放行）：%s\n", api.DisplayName(v.Slug, v.ID))
	})
	return nil
}

// fileAnalysisRedo is `pf file analysis redo <id>`: the daemon owns the
// eligibility checks, the archive and the scheduling; the CLI only names
// the ticket and reports what came of the request.
func fileAnalysisRedo(ctx *Context, name string) error {
	wi, err := ctx.aihub().GetWorkItem(name)
	if err != nil {
		return err
	}
	display := api.DisplayName(wi.Slug, wi.ID)
	raw, err := ctx.daemon().do("POST", "/analysis/redo", "",
		map[string]any{"work_item_id": wi.ID, "project": wi.ProjectName}, nil)
	if err != nil {
		return fmt.Errorf("file analysis redo %s: %w", display, err)
	}
	var res struct {
		State     string `json:"state"`
		TaskID    string `json:"task_id"`
		RequestID string `json:"request_id"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("file analysis redo %s: the daemon's answer is unreadable: %w", display, err)
	}
	switch res.State {
	case "scheduled":
		fmt.Fprintf(ctx.Stdout, "已重新安排分析：%s（旧分析已归档到 `## 历次分析`；确认前自动领取跳过，见 `pf file analysis confirm`）\n", res.TaskID)
	case "queued":
		fmt.Fprintf(ctx.Stdout, "分析重排已排队（%s；daemon 会在有空位时自动派出）\n", res.RequestID)
	default:
		detail := strings.TrimSpace(res.Detail)
		if detail == "" {
			detail = "the daemon refused the request"
		}
		return fmt.Errorf("file analysis redo %s: %s", display, detail)
	}
	return nil
}

// osGetwd is the file-create inference's cwd seam: the production value
// is the process working directory; tests pin it to a fixture path.
var osGetwd = os.Getwd

// requiredProjectFlagError is the polyforge#113 required-flag error,
// byte-identical wherever inference cannot answer (no containing
// workspace, no yaml). It never mutates. It is the default Cell's shape
// (the Host file); a named Cell's invocation gets the same error naming
// its own file (requiredProjectFlagErrorIn, polyforge#17).
const requiredProjectFlagError = "file create: --project is required — there is no default project (polyforge#113); workspaces and their projects are declared in [workspace.\"<path>\"] sections of cli.toml (`pf doctor` checks the mapping)"

// requiredProjectFlagErrorIn is the #113 required-flag error naming the
// mapping file of the Cell the invocation routes to: the pinned Host-file
// shape for the default Cell, the routed Cell's own file otherwise.
func requiredProjectFlagErrorIn(file string) string {
	if file == cellcfg.HostFileName {
		return requiredProjectFlagError
	}
	return strings.Replace(requiredProjectFlagError, "sections of cli.toml", "sections of "+file, 1)
}

// inferProjectFromCwd is the file-create default: the process cwd
// resolved against the expanded [workspace] mapping of the routed Cell
// (profile.go's routedCellMapping) by containment (cwd equal to or inside
// the workspace root). Exactly one containing workspace that lists
// exactly ONE project answers it — the create proceeds as if --project
// had been passed. Every other shape asks for --project; inference never
// ambient-picks (the #113 principle).
func inferProjectFromCwd(ctx *Context) (string, error) {
	plan, file, err := ctx.routedCellMapping()
	if err != nil {
		return "", fmt.Errorf("file create: %w", err)
	}
	cwd, err := osGetwd()
	if err != nil || plan == nil || plan.Empty() {
		return "", errors.New(requiredProjectFlagErrorIn(file))
	}
	var containing []string
	for _, ws := range plan.Workspaces() {
		if cwd == ws || strings.HasPrefix(cwd, ws+string(os.PathSeparator)) {
			containing = append(containing, ws)
		}
	}
	switch {
	case len(containing) == 0:
		return "", errors.New(requiredProjectFlagErrorIn(file))
	case len(containing) > 1:
		return "", fmt.Errorf("file create: --project is required — the working directory is inside more than one mapped workspace (%s); pass --project explicitly (placement stays unambiguous, polyforge#113)", strings.Join(containing, ", "))
	}
	projects := plan.ProjectsOf(containing[0])
	if len(projects) != 1 {
		return "", fmt.Errorf("file create: --project is required — workspace %s maps %d projects (%s); pass --project explicitly (no default is invented)", containing[0], len(projects), strings.Join(projects, ", "))
	}
	return projects[0], nil
}

// requireMappedProject validates a placement verb's --project (file
// create, recall, remember) against the [workspace] mapping of the Cell
// the invocation routes to (polyforge#113; polyforge#17: the routed
// Cell's, not the Host file's). An unmapped (or unvalidatable — no
// mapping configured) project is a loud local error — wrapping the typed
// *mapping.UnmappedError, naming the projects the routed Cell does map
// and pointing at the [workspace] shape and `pf doctor` — and nothing
// is sent to aihub.
func requireMappedProject(ctx *Context, verb, project string) error {
	plan, file, err := ctx.routedCellMapping()
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	if plan.Empty() {
		return fmt.Errorf("%s: project %q cannot be placed: no [workspace] mapping is configured in %s — add a [workspace.\"<path>\"] section (`pf doctor` checks the mapping)", verb, project, file)
	}
	if _, err := plan.WorkspaceFor(project); err != nil {
		var ue *mapping.UnmappedError
		if errors.As(err, &ue) {
			ue.File = file
		}
		return fmt.Errorf("%s: %w; mapped here: %s", verb, err, strings.Join(plan.Projects(), ", "))
	}
	return nil
}

// fileCreate builds the create body from flags or from full stdin JSON.
func fileCreate(ctx *Context, args []string) error {
	f := subFlags(ctx, "file create")
	var (
		project   = f.String("project", "", "project name (required; validated against the [workspace] mapping)")
		goal      = f.String("goal", "", "one-line goal (<=500 chars)")
		content   optString
		labels    stringList
		priority  = f.String("priority", "", "low|normal|high|urgent")
		milestone optString
		bundle    = f.String("bundle", "", "bundle_name: non-empty makes an execution ticket")
		parent    = f.String("parent", "", "parent work item id")
		attrs     kvList
		repos     stringList
		after     stringList
		useStdin  = f.Bool("stdin", false, "read the full create-body JSON from stdin")
	)
	f.Var(&repos, "repo", "project repo this work item works in (repeatable; only these are cloned at claim)")
	f.Var(&after, "after", "execution ticket (id or slug) that must WRAP before this one is claimed (repeatable; a blocks edge, written atomically with the create)")
	f.Var(&content, "content", "markdown background; '-' reads stdin, '@file' reads a file")
	f.Var(&labels, "label", "label (repeatable)")
	f.Var(&milestone, "milestone", "milestone name")
	f.Var(&attrs, "attr", "business attr path=json-value (repeatable; a dotted path nests — split.origin=X stores {\"split\":{\"origin\":\"X\"}}, the same dot path pf queue's --attr filters on)")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("file create: unexpected arguments %v (file create takes flags only)", rest)
	}
	for _, a := range after {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("file create: --after needs a work item id or slug")
		}
	}

	var req aihubclient.CreateWorkItemRequest
	if *useStdin {
		raw, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return err
		}
		var body struct {
			aihubclient.CreateWorkItemRequest
			// the removed pre-v2.5.0 second array: refused, never dropped
			RepoInventoryBase json.RawMessage `json:"repo_inventory_base"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("stdin JSON: %w", err)
		}
		if b := strings.TrimSpace(string(body.RepoInventoryBase)); b != "" && b != "null" && b != "[]" {
			return fmt.Errorf("stdin JSON: repo_inventory_base is not supported; omit it (the work machine clones each repo's default branch)")
		}
		req = body.CreateWorkItemRequest
	}
	if *project != "" {
		req.Project = *project // flag fills a gap in the stdin body
	}
	if *goal != "" {
		req.Goal = *goal
	}
	if req.Project == "" {
		// polyforge#79 inference: the cwd → mapped workspace. It
		// answers a project, the #113 required-flag error (verbatim), or
		// the ambiguity error — never a silent skip.
		proj, err := inferProjectFromCwd(ctx)
		if err != nil {
			return err
		}
		req.Project = proj
	}
	if err := requireMappedProject(ctx, "file create", req.Project); err != nil {
		return err
	}
	if req.Goal == "" {
		return fmt.Errorf("file create: --goal is required (or use --stdin with a full JSON body)")
	}
	if content.IsSet() {
		s, err := readValueOrStdin(ctx, content.Val())
		if err != nil {
			return err
		}
		req.Content = &s
	}
	if len(labels) > 0 {
		req.Labels = labels
	}
	if *priority != "" {
		req.Priority = *priority
	}
	if milestone.IsSet() {
		req.Milestone = milestone.Ptr()
	}
	if *bundle != "" {
		req.BundleName = strPtr(*bundle)
	}
	if *parent != "" {
		req.ParentID = strPtr(*parent)
	}
	if len(repos) > 0 {
		if len(req.RepoInventory) > 0 {
			return fmt.Errorf("file create: --repo cannot be combined with a --stdin body that carries repo_inventory; declare the repos in one place")
		}
		inv := make([]map[string]string, 0, len(repos))
		for _, name := range repos {
			inv = append(inv, map[string]string{"name": name})
		}
		raw, err := json.Marshal(inv)
		if err != nil {
			return err
		}
		req.RepoInventory = raw
	}
	// --after travels in the create body: aihub writes the edges inside
	// the create transaction, so a bad blocker creates nothing.
	req.After = append(req.After, after...)
	if len(attrs) > 0 {
		built, err := attrsFromFlags(attrs)
		if err != nil {
			return err
		}
		req.Attrs = built
	}

	warnAcceptanceCarrier(ctx, req)
	raw, err := ctx.aihub().CreateWorkItem(req)
	if err != nil {
		return openOnBundleRefusal(ctx, req, err)
	}
	var createdID string
	finishAs(ctx, raw, func(wi *WIView) {
		fmt.Fprintln(ctx.Stdout)
		renderWIDetail(ctx.Stdout, wi)
	})
	// the created id is decoded from the answer itself: --json mode
	// runs no human renderer, and the notice must ride in both modes.
	var idDoc struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &idDoc) == nil {
		createdID = idDoc.ID
	}
	// polyforge#72 (ruling 2): a fresh execution ticket whose lane
	// requires acceptance and whose body carries no analysis section
	// gets one auto-scheduled by THIS machine's daemon — never a failed
	// create; the notice rides stderr in both modes (stdout is the
	// server's answer).
	scheduleAnalysisNotice(ctx, req, createdID)
	return nil
}

// scheduleAnalysisNotice asks the local daemon to schedule the created
// ticket's pre-submit analysis (AC1) and prints what came of it: the
// task id, the queued state, or the explicit 「未安排分析」 notice when
// the daemon cannot be reached or refused. Warn-only: the request, the
// created work item, the exit code and stdout stay untouched. In --json
// mode the notice is one machine-readable JSON line on stderr.
func scheduleAnalysisNotice(ctx *Context, req aihubclient.CreateWorkItemRequest, wiID string) {
	if wiID == "" || req.BundleName == nil || *req.BundleName == "" {
		return
	}
	if !laneOfCreate(ctx, req.Project, *req.BundleName).RequiresAcceptance() {
		return
	}
	content := ""
	if req.Content != nil {
		content = *req.Content
	}
	if workflow.HasAnalysis(content) {
		return
	}
	state, taskID, detail := "failed", "", ""
	raw, err := ctx.daemon().do("POST", "/analysis/enqueue", "",
		map[string]any{"work_item_id": wiID, "project": req.Project}, nil)
	if err != nil {
		detail = err.Error()
	} else {
		var res struct {
			State  string `json:"state"`
			TaskID string `json:"task_id"`
			Detail string `json:"detail"`
		}
		if jerr := json.Unmarshal(raw, &res); jerr != nil {
			state, detail = "failed", "the daemon's answer is unreadable: "+jerr.Error()
		} else {
			state, taskID, detail = res.State, res.TaskID, res.Detail
		}
	}
	switch state {
	case "scheduled":
		fmt.Fprintf(ctx.Stderr, "file create: 分析已安排：%s（daemon 的分析任务；完成后确认，见 `pf file analysis confirm`）\n", taskID)
	case "queued":
		fmt.Fprintf(ctx.Stderr, "file create: 分析已排队（任务槽满或执行暂停中），daemon 会在有空位时自动派出\n")
	case "not_needed":
		return
	default:
		if detail == "" {
			detail = "the daemon refused"
		}
		fmt.Fprintf(ctx.Stderr, "file create: 未安排分析：%s\n", detail)
	}
	if ctx.JSON {
		note := struct {
			Analysis struct {
				State  string `json:"state"`
				TaskID string `json:"task_id,omitempty"`
				Detail string `json:"detail,omitempty"`
			} `json:"analysis"`
		}{}
		note.Analysis.State, note.Analysis.TaskID, note.Analysis.Detail = state, taskID, detail
		if b, jerr := json.Marshal(note); jerr == nil {
			fmt.Fprintln(ctx.Stderr, string(b))
		}
	}
}

// laneOfCreate answers the bundle's lane as file create reads it —
// the one read the acceptance-carrier advisory and the analysis
// scheduling decision share (a silent skip on every unreadable shape,
// exactly the advisory's conventions). nil = not readable.
func laneOfCreate(ctx *Context, project, bundle string) *workflow.Lane {
	dir, ok, _ := workspaceCandidateScope(ctx)
	if !ok {
		return nil
	}
	plan, _, err := ctx.routedCellMapping()
	if err != nil {
		return nil
	}
	ws, _ := plan.WorkspaceFor(project)
	fms, _, err := workspace.Bundles(workflow.ForWorkspace(dir, ws), bundle)
	if err != nil || fms[bundle] == nil {
		return nil
	}
	return fms[bundle]
}

// warnAcceptanceCarrier puts the chosen bundle's step table in front of
// the filer of an execution ticket: every acceptance criterion must name
// the step that carries it. The table is read the way the CLI already
// reads it — the lane set of the project's workspace over the daemon's
// workflows dir, then the bundle's workflow.yaml; an
// unreachable daemon, no workflows dir or an unknown or torn bundle is a
// silent skip, and so is a lane declaring it requires no acceptance (its
// `acceptance` declaration: no criterion to carry). Warn only — the request, the created work item, the exit
// code and stdout are untouched. The text carries no braces: acceptance
// helpers slice JSON from the first one in merged output.
func warnAcceptanceCarrier(ctx *Context, req aihubclient.CreateWorkItemRequest) {
	if req.BundleName == nil || *req.BundleName == "" {
		return
	}
	bundle := *req.BundleName
	fm := laneOfCreate(ctx, req.Project, bundle)
	if fm == nil || len(fm.Steps) == 0 || !fm.RequiresAcceptance() {
		return
	}
	ids := make([]string, 0, len(fm.Steps))
	for _, st := range fm.Steps {
		ids = append(ids, st.ID)
	}
	fmt.Fprintf(ctx.Stderr, "file create: bundle steps: %s = %s; every acceptance criterion must name the step that carries it, written as <criterion> — carried by <step id>\n", bundle, strings.Join(ids, " -> "))
}

// openOnBundleRefusal answers err, the create's failure, except aihub's
// CORE-409 OPEN_ON_BUNDLE: the work item was filed exclusive — the daemon
// sets that for a lane declaring `exclusive: true` — and another work item
// of the project on the lane is unfinished. That refusal keeps its typed
// code and names the open work item and its status; one still queued that
// no machine ever claimed (no assignee: aihub sets it at the first claim)
// was filed but not submitted — its filer stopped in between, or is
// submitting it right now — so the refusal says when it was filed and how
// to run or drop it. When the open work item cannot be read back, aihub's
// own refusal (it names the work item and its status) is answered.
func openOnBundleRefusal(ctx *Context, req aihubclient.CreateWorkItemRequest, err error) error {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict || errorReason(ae) != "OPEN_ON_BUNDLE" || req.BundleName == nil {
		return err
	}
	items, lerr := ctx.aihub().ListWorkItems(aihubclient.WorkItemFilter{Project: req.Project, Bundle: *req.BundleName, Limit: 200})
	if lerr != nil {
		return err
	}
	var open *api.WorkItem // the one aihub names: the unfinished one first filed
	for i := range items {
		switch items[i].Status {
		case api.StatusWrapped, api.StatusFailed, api.StatusCanceled:
			continue
		}
		if open == nil || items[i].Seq < open.Seq {
			open = &items[i]
		}
	}
	if open == nil {
		return err
	}
	name := api.DisplayName(open.Slug, open.ID)
	msg := fmt.Sprintf("file create: %s of %s is %s on the lane %s, which declares exclusive: true — one unfinished work item at a time, nothing was filed", name, req.Project, open.Status, *req.BundleName)
	if open.Status == api.StatusQueued && open.Assignee == nil {
		msg += fmt.Sprintf("; it was filed at %s and no machine has claimed it: unless its filer is submitting it right now, run it with `pf submit %s` or drop it with `pf cancel %s`", open.CreatedAt, name, name)
	}
	return &api.Error{Code: ae.Code, Detail: msg, Extra: ae.Extra}
}

// errorReason reads a typed error's machine-readable sub-reason.
func errorReason(ae *api.Error) string {
	var m struct {
		Reason string `json:"reason"`
	}
	if len(ae.Extra) == 0 || json.Unmarshal(ae.Extra, &m) != nil {
		return ""
	}
	return m.Reason
}

// reasonErr renders a write failure — a dependency write
// (DEPENDENCY_CYCLE ...), a file edit (CHILD_RUNNING, IMMUTABLE ...) —
// with the server's reason next to the code, which the plain envelope
// line would otherwise drop.
func reasonErr(err error) error {
	var ae *api.Error
	if !errors.As(err, &ae) {
		return err
	}
	var extra struct {
		Reason string `json:"reason"`
	}
	if len(ae.Extra) > 0 && json.Unmarshal(ae.Extra, &extra) == nil && extra.Reason != "" {
		return &api.Error{Code: ae.Code, Detail: extra.Reason + ": " + ae.Detail, Extra: ae.Extra}
	}
	return err
}

// fileDep manages `blocks` edges (the only kind the CLI exposes,
// polyforge#161). add/rm take the blocking work item first: "A blocks
// B" is `pf file dep add A B`.
func fileDep(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("file dep: expected add|rm|list (see `pf help file`)")
	}
	sub, rest := args[0], args[1:]
	f := subFlags(ctx, "file dep "+sub)
	rest, err := parseCommandArgs(f, rest)
	if err != nil {
		return err
	}
	var (
		raw  json.RawMessage
		self string
	)
	switch sub {
	case "add", "rm":
		if len(rest) != 2 {
			return fmt.Errorf("file dep %s: expected <blocking> <blocked> (\"A blocks B\" is `pf file dep %s A B`)", sub, sub)
		}
		blocking, blocked := rest[0], rest[1]
		if sub == "add" {
			raw, err = ctx.aihub().AddBlocksDependency(blocking, blocked)
		} else {
			raw, err = ctx.aihub().RemoveBlocksDependency(blocking, blocked)
		}
		if err != nil {
			if ctx.JSON {
				return err
			}
			return reasonErr(err)
		}
		self = blocked
	case "list":
		if len(rest) != 1 {
			return fmt.Errorf("file dep list: expected exactly one work item id")
		}
		self = rest[0]
		if raw, err = ctx.aihub().DependenciesRaw(self); err != nil {
			return err
		}
	default:
		return fmt.Errorf("file dep: unknown action %q (expected add|rm|list)", sub)
	}
	finishAs(ctx, raw, func(d *depsView) {
		fmt.Fprintf(ctx.Stdout, "%s\n", self)
		renderDeps(ctx.Stdout, d)
	})
	return nil
}

// fileEdit issues a PATCH with only the flags actually provided.
func fileEdit(ctx *Context, args []string) error {
	f := subFlags(ctx, "file edit")
	var (
		goal      optString
		content   optString
		labels    stringList
		priority  optString
		milestone optString
		waitingOn optString
		bundle    optString
		parent    optString
		useStdin  = f.Bool("stdin", false, "read the full PATCH-body JSON from stdin")
	)
	f.Var(&bundle, "bundle", "bundle_name (empty string unbinds); aihub accepts it only while the work item is queued and never claimed")
	f.Var(&goal, "goal", "new goal")
	f.Var(&content, "content", "new markdown background; '-' reads stdin, '@file' reads a file")
	f.Var(&labels, "label", "labels (repeatable; replaces the whole set)")
	f.Var(&priority, "priority", "low|normal|high|urgent")
	f.Var(&milestone, "milestone", "milestone (empty string clears it)")
	f.Var(&waitingOn, "waiting-on", "waiting_on value")
	f.Var(&parent, "parent", `only "" — detaches the work item from its parent (aihub refuses a running one; re-parenting is not supported)`)
	// No --attr: attrs are set at create only — aihub's PATCH whitelist
	// (core patchableFields) refuses the key with CORE-400 IMMUTABLE.
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 && !*useStdin {
		return fmt.Errorf("file edit: expected exactly one work item id")
	}
	var id string
	if len(rest) == 1 {
		id = rest[0]
	}

	body := map[string]any{}
	if *useStdin {
		raw, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return fmt.Errorf("stdin JSON: %w", err)
		}
		if v, ok := body["id"].(string); ok && id == "" {
			id = v
		}
		delete(body, "id")
	}
	if id == "" {
		return fmt.Errorf("file edit: work item id required")
	}
	// polyforge#77: aihub only clears parent_id (re-parenting is
	// IMMUTABLE); a non-empty value is refused here, before the wire.
	if parent.IsSet() && parent.Val() != "" {
		return fmt.Errorf(`file edit: --parent only detaches (--parent ""); re-parenting %s under %s is not supported`, id, parent.Val())
	}
	if goal.IsSet() {
		body["goal"] = goal.Val()
	}
	if content.IsSet() {
		s, err := readValueOrStdin(ctx, content.Val())
		if err != nil {
			return err
		}
		body["content"] = s
	}
	if len(labels) > 0 {
		body["labels"] = []string(labels)
	}
	if priority.IsSet() {
		body["priority"] = priority.Val()
	}
	if milestone.IsSet() {
		body["milestone"] = milestone.Ptr()
	}
	if waitingOn.IsSet() {
		body["waiting_on"] = waitingOn.Ptr()
	}
	if bundle.IsSet() {
		if bundle.Val() == "" {
			body["bundle_name"] = nil // JSON null unbinds the name
		} else {
			body["bundle_name"] = bundle.Val()
		}
	}
	if parent.IsSet() {
		body["parent_id"] = nil // JSON null detaches the child
	}
	raw, err := ctx.aihub().PatchWorkItem(id, body)
	if err != nil {
		if ctx.JSON {
			return err // --json keeps the server envelope verbatim
		}
		return reasonErr(err)
	}
	finishAs(ctx, raw, func(wi *WIView) {
		fmt.Fprintln(ctx.Stdout)
		renderWIDetail(ctx.Stdout, wi)
	})
	return nil
}

// fileComment appends one comment to a work item. There is no edit and
// no delete: this is the only comment write.
func fileComment(ctx *Context, args []string) error {
	f := subFlags(ctx, "file comment")
	var (
		body    optString
		replyTo optString
	)
	f.Var(&body, "body", "comment text ('-' reads stdin, '@file' reads a file)")
	f.Var(&replyTo, "reply-to", "id of an earlier comment on the SAME work item")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("file comment: expected exactly one work item id")
	}
	if !body.IsSet() {
		return fmt.Errorf("file comment: --body is required")
	}
	text, err := readValueOrStdin(ctx, body.Val())
	if err != nil {
		return err
	}
	req := api.AddCommentRequest{Body: text}
	if replyTo.IsSet() {
		req.ReplyTo = replyTo.Val()
	}
	raw, err := ctx.aihub().AddComment(rest[0], req)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(c *api.Comment) {
		fmt.Fprintln(ctx.Stdout)
		renderComment(ctx.Stdout, *c)
	})
	return nil
}

// fileShow reads the detail record.
func fileShow(ctx *Context, args []string) error {
	f := subFlags(ctx, "file show")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("file show: expected exactly one work item id")
	}
	raw, err := ctx.aihub().GetWorkItemRaw(rest[0])
	if err != nil {
		return err
	}
	// The human face appends the dependency block (polyforge#161); --json
	// stays the verbatim work-item body (it carries waiting_for).
	var deps *depsView
	if !ctx.JSON {
		draw, err := ctx.aihub().DependenciesRaw(rest[0])
		if err != nil {
			return err
		}
		deps = &depsView{}
		if err := json.Unmarshal(draw, deps); err != nil {
			return fmt.Errorf("file show: dependencies: %w", err)
		}
	}
	finishAs(ctx, raw, func(wi *WIView) {
		fmt.Fprintln(ctx.Stdout)
		renderWIDetail(ctx.Stdout, wi)
		renderDeps(ctx.Stdout, deps)
	})
	return nil
}

// readValueOrStdin resolves a value flag: "-" reads all stdin, "@path"
// reads a file, anything else is the literal value.
func readValueOrStdin(ctx *Context, v string) (string, error) {
	switch {
	case v == "-":
		b, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case strings.HasPrefix(v, "@"):
		b, err := os.ReadFile(strings.TrimPrefix(v, "@"))
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return v, nil
	}
}

// parseAttrValue decodes an --attr flag value: JSON when it parses, plain
// string otherwise.
func parseAttrValue(v string) (any, error) {
	var out any
	if err := json.Unmarshal([]byte(v), &out); err == nil {
		return out, nil
	}
	return v, nil
}

// attrsFromFlags builds one attrs object from a create's --attr flags
// (polyforge#174): a key is an attrs path — a key with dots nests as
// the path it names (split.origin=X stores {"split":{"origin":"X"}}),
// the same dot path `pf queue --attr` filters on, and a key without
// dots stays one top-level key. Paths that share only a prefix merge;
// a path assigned twice (even with the same value), a path assigned
// beside its sub-path (an object VALUE beside its sub-path included:
// a flag's JSON value is never a container to descend into), or an
// empty segment (a..b, a leading or trailing dot) refuse the whole
// create. The collisions are decided on the declared paths alone,
// before anything is built, so the refusal never depends on flag
// order (review r1#1).
func attrsFromFlags(attrs kvList) (map[string]any, error) {
	segsOf := make([][]string, len(attrs))
	for i, kv := range attrs {
		segs := strings.Split(kv.Key, ".")
		for _, seg := range segs {
			if seg == "" {
				return nil, fmt.Errorf("--attr %s: %q is not an attrs path (empty segment)", kv.Key, kv.Key)
			}
		}
		segsOf[i] = segs
	}
	for i := 0; i < len(segsOf); i++ {
		for j := i + 1; j < len(segsOf); j++ {
			if pathSegmentsEqual(segsOf[i], segsOf[j]) {
				return nil, fmt.Errorf("--attr %s: the path %q is assigned twice", attrs[i].Key, attrs[i].Key)
			}
			if pathOverlaps(segsOf[i], segsOf[j]) {
				return nil, fmt.Errorf("--attr %s: the path %q is assigned beside its sub-path %q — a parent and its sub-path cannot both be assigned", attrs[i].Key, attrs[i].Key, attrs[j].Key)
			}
		}
	}
	root := map[string]any{}
	for i, kv := range attrs {
		v, err := parseAttrValue(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("--attr %s: %w", kv.Key, err)
		}
		cur := root
		for k, seg := range segsOf[i] {
			if k == len(segsOf[i])-1 {
				cur[seg] = v
				break
			}
			nxt, ok := cur[seg].(map[string]any)
			if !ok {
				nxt = map[string]any{}
				cur[seg] = nxt
			}
			cur = nxt
		}
	}
	return root, nil
}

// pathSegmentsEqual answers whether two paths are the same segments.
func pathSegmentsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pathOverlaps answers whether one path is a strict segment-prefix of
// the other (equal paths are pathSegmentsEqual's own case).
func pathOverlaps(a, b []string) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == len(a) && n == len(b) {
		return false
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// queue — WI list + ready-queue (direct to aihub).
// ---------------------------------------------------------------------------

var cmdQueue = &Command{
	Name:    "queue",
	Group:   GroupData,
	Summary: "List work items / show the ready queue",
	Usage: "pf queue [--project P] [--status S] [--bundle L] [--attr <path>=<value>]... [--limit N]\n" +
		"pf queue --ready [--project P]\n\n" +
		"Without --project: one merged list over ALL mapped projects (the\n" +
		"[workspace] mapping in cli.toml); --project P is passed to aihub\n" +
		"unvalidated (read-only: the mapping governs placement, not knowledge).\n" +
		"--bundle and --attr pass straight through as aihub's list filters\n" +
		"(?bundle=L, ?attr=<path>:<value>; each --attr is split at its first\n" +
		"'=', the rest is the value verbatim, aihub checks the path), e.g.\n" +
		"pf queue --bundle <lane> --attr x.y=z.",
	Setup: func(fs *flag.FlagSet) {
		fs.String("project", "", "project name (optional: absent = all mapped projects, merged; present = passed to aihub unvalidated)")
		fs.Bool("ready", false, "show the ready queue instead of the list")
		fs.String("status", "", "filter: queued|running|paused|blocked|wrapped|failed|canceled")
		fs.String("bundle", "", "filter: the lane (bundle) name, passed to aihub as ?bundle= (list mode only)")
		fs.Var(&stringList{}, "attr", "filter <path>=<value>: the attr at the dot path is that string, passed to aihub as "+
			"?attr=<path>:<value> (repeatable, all must hold; split at the first '='; list mode only)")
		fs.Int("limit", 0, "cap the list at N items (1..200; default list mode only — "+
			"cannot be combined with --ready; omitted = the server's default cap)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("queue: unexpected arguments %v", args)
		}
		// Local flag validation, ahead of any config resolution or wire
		// call (same discipline as recall's --limit): a typo fails loudly
		// here and no request is sent. Unlike recall there is no CLI-side
		// default — an omitted --limit stays 0, so ?limit= is left off
		// the wire and the server's own default cap applies. An explicit
		// out-of-range value (including 0) is still refused loudly: Visit
		// tells "user typed it" apart from "flag not passed".
		limit := intFlag(fs, "limit")
		var limitSet bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "limit" {
				limitSet = true
			}
		})
		if limitSet {
			// The combination restriction is structural, so it is reported
			// ahead of the value range.
			if boolFlag(fs, "ready") {
				return fmt.Errorf("queue: --limit cannot be combined with --ready " +
					"(it caps the default list mode only)")
			}
			if limit < 1 || limit > 200 {
				return fmt.Errorf("queue: --limit must be 1..200")
			}
		}
		filter := aihubclient.WorkItemFilter{Status: stringFlag(fs, "status"), Bundle: stringFlag(fs, "bundle"), Limit: limit}
		for _, a := range *fs.Lookup("attr").Value.(*stringList) {
			path, value, ok := strings.Cut(a, "=")
			if !ok || path == "" {
				return fmt.Errorf("queue: --attr %q is not <path>=<value>", a)
			}
			filter.Attrs = append(filter.Attrs, path+":"+value)
		}
		if boolFlag(fs, "ready") && (filter.Bundle != "" || len(filter.Attrs) > 0) {
			return fmt.Errorf("queue: --bundle/--attr cannot be combined with --ready " +
				"(they filter the default list mode only)")
		}
		proj := stringFlag(fs, "project")
		// polyforge#18: the mapping this verb merges over, marks against and
		// names in its empty-mapping refusal is the routed Cell's
		// (profile.go's routedCellMapping), never the Host file's alone —
		// a named Cell's queue never silently lists another Cell's
		// projects. An unreadable routed mapping is a loud refusal, exactly
		// like the placement verbs'.
		plan, mapFile, err := ctx.routedCellMapping()
		if err != nil {
			return err
		}
		if proj == "" {
			// polyforge#113: no ambient default project exists. Without
			// --project the verb covers ALL mapped projects in one merged
			// presentation; with no mapping at all it fails loudly with
			// guidance — it never silently lists nothing.
			if plan.Empty() {
				return fmt.Errorf("queue: --project is required — no [workspace] mapping is configured in %s; add a [workspace.\"<path>\"] section (`pf doctor` checks the mapping) or pass --project P", mapFile)
			}
			return queueMerged(ctx, plan, boolFlag(fs, "ready"), filter)
		}
		// An explicit --project passes through to aihub UNVALIDATED
		// (mapped or not — exactly one query; the mapping governs
		// placement, not knowledge).
		var raw json.RawMessage
		if boolFlag(fs, "ready") {
			raw, err = ctx.aihub().ReadyQueueRaw(proj)
		} else {
			filter.Project = proj
			raw, err = ctx.aihub().ListWorkItemsFilteredRaw(filter)
		}
		if err != nil {
			return err
		}
		// Both faces render the items segment; --json is the raw server
		// body (no acceptance_missing / analysis_missing: those are the
		// merged document's).
		finishAs(ctx, raw, func(l *itemList[WIView]) {
			markNeedsInteraction(l.Items, laneResolver(readDaemonFacts(ctx), plan))
			fmt.Fprintf(ctx.Stdout, "%d work item(s)\n", len(l.Items))
			renderWITable(ctx.Stdout, l.Items)
			renderMissingLanes(ctx, plan, l.Items)
		})
		return nil
	},
}

// queueMerged is queue without --project over a non-empty mapping: one
// merged presentation of every mapped project's list (or ready queue),
// each row's project identifiable (PROJECT column; --json emits ONE
// merged items array — records already carry their project server-side).
// --limit and the other filters apply per-project (each query is one
// server call); a
// per-project fetch failure fails the verb naming the project — fake
// coverage is worse than none. Determinism: mapping declaration order,
// then the server's own order within each project.
func queueMerged(ctx *Context, m *mapping.Plan, ready bool, filter aihubclient.WorkItemFilter) error {
	projects := m.Projects()
	var items []WIView
	for _, p := range projects {
		// polyforge#30 (AC6): the default view lists no deactivated
		// project — one project read decides, and its list is never even
		// queried. A status read that fails falls through to the list
		// query, which answers with its own error, exactly as before.
		if raw, err := ctx.aihub().GetProject(p); err == nil {
			var proj struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &proj) == nil && proj.Status == api.ProjectStatusDeactivated {
				continue
			}
		}
		var (
			raw json.RawMessage
			err error
		)
		if ready {
			raw, err = ctx.aihub().ReadyQueueRaw(p)
		} else {
			filter.Project = p
			raw, err = ctx.aihub().ListWorkItemsFilteredRaw(filter)
		}
		if err != nil {
			return fmt.Errorf("queue: project %s: %w", p, err)
		}
		var l itemList[WIView]
		if err := json.Unmarshal(raw, &l); err != nil {
			return fmt.Errorf("queue: project %s: %w", p, err)
		}
		items = append(items, l.Items...)
	}
	// a queued execution ticket without acceptance or without an
	// analysis section on a lane requiring one is marked (the
	// auto-pull wave skips it): the STATUS cell, acceptance_missing and
	// analysis_missing — judged through the routed Cell's mapping m.
	markNeedsInteraction(items, laneResolver(readDaemonFacts(ctx), m))
	if ctx.JSON {
		// ONE merged document; lanes_missing lists the queued items a
		// claim refuses because their lane is not in the daemon's lane
		// set (WFC-123; absent when there is none or the daemon is down).
		doc := struct {
			Items        []WIView      `json:"items"`
			Total        int           `json:"total"`
			LanesMissing []laneMissing `json:"lanes_missing,omitempty"`
		}{Items: items, Total: len(items), LanesMissing: queueMissingLanes(ctx, m, items)}
		if b, err := json.Marshal(doc); err == nil {
			ctx.Out(b)
		}
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "%d work item(s)\n", len(items))
	renderWITable(ctx.Stdout, items)
	renderMissingLanes(ctx, m, items)
	return nil
}

// queueMissingLanes answers the queued items of items bound to a lane
// their workspace's lane set does not carry (WFC-123): a claim refuses
// each and it stays queued. nil when the daemon does not answer (its lane
// set is unknown) or none is. The lane sets come through the routed
// Cell's mapping m.
func queueMissingLanes(ctx *Context, m *mapping.Plan, items []WIView) []laneMissing {
	f := readDaemonFacts(ctx)
	if !f.reachable || f.workflowsDir == "" {
		return nil
	}
	return missingLanes(projectLaneSets(f.workflowsDir, m), items)
}

// renderMissingLanes prints the WFC-123 line under the human queue table.
func renderMissingLanes(ctx *Context, m *mapping.Plan, items []WIView) {
	if rows := queueMissingLanes(ctx, m, items); len(rows) > 0 {
		fmt.Fprintf(ctx.Stdout, "\n%s\n", missingLanesText(rows))
	}
}

// ---------------------------------------------------------------------------
// cancel — terminate a non-active WI (direct to aihub).
// ---------------------------------------------------------------------------

var cmdCancel = &Command{
	Name:    "cancel",
	Group:   GroupData,
	Summary: "Cancel (abandon) a queued/paused/blocked work item",
	Usage:   "pf cancel <id>",
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("cancel: expected exactly one work item id")
		}
		raw, err := ctx.aihub().CancelWorkItem(args[0])
		if err != nil {
			return err
		}
		finishAs(ctx, raw, func(wi *api.WorkItem) {
			fmt.Fprintf(ctx.Stdout, "cancelled %s (status %s)\n", wi.Slug, wi.Status)
		})
		return nil
	},
}

// ---------------------------------------------------------------------------
// recall — memory retrieval (direct to the aihub memory face).
// ---------------------------------------------------------------------------

var cmdRecall = &Command{
	Name:    "recall",
	Group:   GroupData,
	Summary: "Search project memories (or browse them, with no query)",
	Usage:   "pf recall --project P <query> [--any] [--type T] [--tags a,b] [--limit N] [--writer-kind session|step]\n       pf recall --project P [--after CURSOR] [--sort updated|created] [--type T] [--tags a,b] [--limit N] [--writer-kind session|step]",
	Setup: func(fs *flag.FlagSet) {
		fs.String("project", "", "project name (required; validated against the [workspace] mapping)")
		fs.String("q", "", "query text (alternative to the positional argument); none = browse mode")
		fs.String("type", "", "memory type filter: exact (experience.debug), prefix (experience.*), or * (all types)")
		fs.String("tags", "", "comma-separated tags")
		fs.Int("limit", 20, "max results / browse page size (1..200; default 20)")
		fs.Bool("any", false, "ranked any-term match (memories sharing ANY of the query's words, best first) instead of the default every-word match; for long queries")
		// No default for --sort: aihub refuses sort on a keyword search
		// (MEM-400), so a default would break every keyword recall; browse
		// without --sort is aihub's own default, updated.
		fs.String("after", "", "browse mode: continue after a previous page's next cursor")
		fs.String("sort", "", "browse mode: updated (aihub's default) or created, both descending")
		fs.String("writer-kind", "", "writer filter: session or step (memories the daemon stamped as a session or a step dispatch)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		q := stringFlag(fs, "q")
		if q == "" {
			q = strings.Join(args, " ")
		}
		browse := strings.TrimSpace(q) == ""
		after, sort := stringFlag(fs, "after"), stringFlag(fs, "sort")
		// The writer filter is the #107 wire name verbatim, validated
		// locally: an illegal value fails loudly here and no request is
		// sent (an omitted flag is no filter at all — nothing default
		// rides the wire).
		writerKind := stringFlag(fs, "writer-kind")
		switch writerKind {
		case "":
		case "session", "step":
		default:
			return fmt.Errorf("recall: --writer-kind must be session or step (got %q)", writerKind)
		}
		// Local flag validation, ahead of any config resolution or wire
		// call: a refusal fails loudly here and no request is sent.
		if browse && boolFlag(fs, "any") {
			return fmt.Errorf("recall: --any ranks a keyword search and needs a query; browse mode (no query, the human direct-aihub path) lists by --sort instead")
		}
		if ctx.Task != "" {
			// Browse is the human direct-aihub shape: the
			// daemon's task memory proxy is search-only.
			if browse {
				return fmt.Errorf("recall: a query is required inside a task — browse mode (no query, --after, --sort) is the human direct-aihub path; task agents search only")
			}
			if after != "" || sort != "" {
				return fmt.Errorf("recall: --after and --sort are browse flags of the human direct-aihub path; a task agent searches only")
			}
			if writerKind != "" {
				return fmt.Errorf("recall: --writer-kind is a filter of the human direct-aihub path; a task agent searches only")
			}
		}
		// The default-20 limit is the CLI's product choice (aihub's
		// browse default is 20 too; its keyword default 50 stays a wire
		// backstop for other callers).
		limit := intFlag(fs, "limit")
		if limit < 1 || limit > 200 {
			return fmt.Errorf("recall: --limit must be 1..200")
		}
		memType, err := recallTypeFilter(stringFlag(fs, "type"))
		if err != nil {
			return err
		}
		proj := stringFlag(fs, "project")
		if proj == "" {
			return fmt.Errorf("recall: --project is required — there is no default project (polyforge#113); the routing skill derives it from the WI under discussion")
		}
		if err := requireMappedProject(ctx, "recall", proj); err != nil && ctx.Task == "" {
			return err
		}
		var tagList []string
		if tags := stringFlag(fs, "tags"); tags != "" {
			tagList = strings.Split(tags, ",")
		}
		match := ""
		if boolFlag(fs, "any") {
			match = "any"
		}
		if browse {
			q = ""
		}
		// A keyword search forwards an explicitly given --after/--sort
		// too: aihub answers the mixed shape with MEM-400 itself.
		raw, err := ctx.searchMemories(proj, q, memType, tagList, limit, match, sort, after, writerKind)
		if err != nil {
			return err
		}
		// AC3 (polyforge#119): a server older than the writer filter
		// (polyforge#107) silently ignores it, so the REQUESTED filter is
		// proven against every returned item BEFORE any output — a mismatch
		// (another kind, or no kind at all on a pre-writer row) refuses
		// with nothing shown. The check's provable boundary (Q5): an empty
		// page and a page whose every item matches are accepted — a silent
		// filter that happens to match cannot be detected, and no probing
		// request is made to try.
		if writerKind != "" {
			if err := checkWriterKindConsistency(raw, writerKind); err != nil {
				return err
			}
		}
		if browse {
			finishAs(ctx, raw, func(p *memoryPage) { renderMemoryPage(ctx.Stdout, p) })
			return nil
		}
		finishAs(ctx, raw, func(l *itemList[memorySearchHit]) {
			fmt.Fprintf(ctx.Stdout, "%d hit(s)\n", len(l.Items))
			renderMemories(ctx.Stdout, l.Items)
		})
		return nil
	},
}

// checkWriterKindConsistency proves one filtered recall's answer against
// the requested --writer-kind value (AC3, polyforge#119): every returned
// item's writer_kind must equal want. The first item that does not — a
// different kind, or none at all — answers the refusal naming it, so a
// server that silently ignored the filter never shows its unfiltered
// rows as if they were filtered. An empty items list passes (the
// boundary the owner accepted, Q5).
func checkWriterKindConsistency(raw json.RawMessage, want string) error {
	var page struct {
		Items []struct {
			ID         string  `json:"id"`
			WriterKind *string `json:"writer_kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return fmt.Errorf("recall: the server's answer is not a memory page: %w", err)
	}
	for _, it := range page.Items {
		if it.WriterKind != nil && *it.WriterKind == want {
			continue
		}
		id := it.ID
		if id == "" {
			id = "(no id)"
		}
		return fmt.Errorf("recall: the server did not apply the --writer-kind %s filter (memory %s is not a %s write): the aihub is older than the writer filter and ignored it — nothing was shown; drop --writer-kind or upgrade the aihub", want, id, want)
	}
	return nil
}

// recallTypeFilter validates the --type form and returns the value to
// send on the wire. Exactly three forms are legal: a plain value
// (exact match), a trailing ".*" (prefix match — experience.* is
// interpreted by the store as the prefix "experience."), and a lone
// "*" (all types, normalized to no filter at all, exactly as if --type
// had been omitted). Any other "*" placement (*.debug, exp*rience,
// experience*, an empty prefix in .*) is a typo we refuse loudly
// instead of sending.
func recallTypeFilter(memType string) (string, error) {
	bad := fmt.Errorf("recall: --type must be a plain value (e.g. experience.debug), "+
		"a trailing .* prefix (e.g. experience.*), or * for all types; got %q", memType)
	switch {
	case memType == "" || memType == "*":
		return "", nil
	case strings.HasSuffix(memType, ".*"):
		if prefix := strings.TrimSuffix(memType, ".*"); prefix != "" && !strings.Contains(prefix, "*") {
			return memType, nil
		}
		return "", bad
	case strings.Contains(memType, "*"):
		return "", bad
	default:
		return memType, nil
	}
}

// ---------------------------------------------------------------------------
// remember — memory write face (the v1 CLI's read verb is `recall`; this
// is its write counterpart on the human side).
// ---------------------------------------------------------------------------

var cmdRemember = &Command{
	Name:    "remember",
	Group:   GroupData,
	Summary: "Store a project memory (the write face of `recall`)",
	Usage: "pf remember --project P --type T [--title S] [--visibility V] [--tags a,b] " +
		"[--work-item ID] <content | - (stdin) | @file>",
	Setup: func(fs *flag.FlagSet) {
		fs.String("project", "", "project name (required; validated against the [workspace] mapping)")
		fs.String("type", "", "memory type (required, e.g. experience.debug, fact.note)")
		fs.String("title", "", "memory title")
		fs.String("visibility", "project", "admin|private|project|team|public")
		fs.String("tags", "", "comma-separated tags")
		fs.String("work-item", "", "attributing work item id or <project>#<seq> slug (optional)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		memType := stringFlag(fs, "type")
		if strings.TrimSpace(memType) == "" {
			return fmt.Errorf("remember: --type is required (e.g. experience.debug)")
		}
		proj := stringFlag(fs, "project")
		if proj == "" {
			return fmt.Errorf("remember: --project is required — there is no default project (polyforge#113)")
		}
		if err := requireMappedProject(ctx, "remember", proj); err != nil {
			return err
		}
		// The content: positional text, '-' for stdin, '@file' for a file.
		var content string
		switch {
		case len(args) == 1 && args[0] == "-":
			raw, err := io.ReadAll(ctx.Stdin)
			if err != nil {
				return err
			}
			content = string(raw)
		case len(args) == 1 && strings.HasPrefix(args[0], "@"):
			raw, err := os.ReadFile(strings.TrimPrefix(args[0], "@"))
			if err != nil {
				return err
			}
			content = string(raw)
		case len(args) >= 1:
			content = strings.Join(args, " ")
		default:
			return fmt.Errorf("remember: content is required (positional, '-' for stdin or '@file')")
		}
		if strings.TrimSpace(content) == "" {
			return fmt.Errorf("remember: content is required")
		}
		body := map[string]any{
			"project":    proj,
			"type":       memType,
			"content":    content,
			"visibility": stringFlag(fs, "visibility"),
		}
		if t := stringFlag(fs, "title"); t != "" {
			body["title"] = t
		}
		if tags := stringFlag(fs, "tags"); tags != "" {
			body["tags"] = strings.Split(tags, ",")
		}
		if wi := stringFlag(fs, "work-item"); wi != "" {
			body["work_item_id"] = wi
		}
		var raw json.RawMessage
		if err := ctx.aihub().Do("POST", "/api/v1/memories", body, &raw); err != nil {
			return err
		}
		finishAs(ctx, raw, func(m *api.Memory) {
			if m.ID != "" {
				fmt.Fprintf(ctx.Stdout, "stored %s (type %s, visibility %s)\n", m.ID, m.Type, m.Visibility)
			}
		})
		return nil
	},
}

// ---------------------------------------------------------------------------
// memory — per-id memory read (the body face `recall` cannot serve:
// recall returns metadata per hit; a memory's body only comes back by
// id, from the server's per-id face).
// ---------------------------------------------------------------------------

var cmdMemory = &Command{
	Name:    "memory",
	Group:   GroupData,
	Summary: "Show one stored memory (metadata + body, by id)",
	Usage:   "pf memory show <id> [--max-bytes N]",
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("memory: expected show (see `pf help memory`)")
		}
		sub, rest := args[0], args[1:]
		switch sub {
		case "show":
			return memoryShow(ctx, rest)
		default:
			return fmt.Errorf("memory: unknown action %q", sub)
		}
	},
}

// defaultMemoryShowCap is the human-mode body cap: 256 KiB. A larger
// body is truncated with the exact notice below; --max-bytes 0 lifts the
// cap entirely. The --json body is never capped at any value.
const defaultMemoryShowCap = 262144

// memoryMetaView is the human header's decode of the memory.Entry
// metadata JSON (additive: unknown fields are ignored — the raw bytes
// stay the --json face's authority).
type memoryMetaView struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Title            string          `json:"title"`
	OwnerUserID      string          `json:"owner_user_id"`
	OwnerDisplayName string          `json:"owner_display_name"`
	WriterKind       *string         `json:"writer_kind"`
	WriterDetails    json.RawMessage `json:"writer_details"`
	Visibility       string          `json:"visibility"`
	UpdatedAt        string          `json:"updated_at"`
	ContentLen       int64           `json:"content_len"`
}

// memoryShow reads one memory by id: metadata from the default JSON face
// (GET /api/v1/memories/{id}) and the body from ?format=content — two
// requests, both through the aihub client's shared auth/typed-error
// path, never a bypass. Human mode prints the metadata header, a blank
// line, then the body capped at --max-bytes (default 256 KiB, 0 =
// unlimited), ending with the exact truncation notice when capped.
// --json emits the server metadata verbatim plus one CLI-added top-level
// "content" string carrying the full, never-truncated body. There is no
// --project flag: ids are globally unique and visibility is enforced
// server-side, so an unknown or invisible id answers the server's 404 as
// the typed CLI error (no probing, no special-casing).
func memoryShow(ctx *Context, args []string) error {
	return memoryShowWith(ctx, "memory show", args, ctx.readMemory)
}

// memoryShowWith is memoryShow reading through read (`pf step memory
// show` reads through the step's call, verbs_step.go).
func memoryShowWith(ctx *Context, name string, args []string, read func(id string) (json.RawMessage, []byte, error)) error {
	f := subFlags(ctx, name)
	maxBytes := f.Int("max-bytes", defaultMemoryShowCap,
		"human-mode body cap in bytes (default 262144; 0 = unlimited)")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%s: expected exactly one memory id", name)
	}
	capBytes := *maxBytes
	if capBytes < 0 {
		return fmt.Errorf("%s: --max-bytes must be >= 0 (0 = unlimited)", name)
	}
	id := rest[0]
	metaRaw, body, err := read(id)
	if err != nil {
		return err
	}
	var meta memoryMetaView
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return fmt.Errorf("memory show: metadata decode: %w", err)
	}
	if ctx.JSON {
		doc, err := memoryJSONWithContent(metaRaw, string(body))
		if err != nil {
			return err
		}
		ctx.Out(doc)
		return nil
	}
	// Human mode: metadata header, blank line, the (capped) body.
	fmt.Fprintf(ctx.Stdout, "id: %s\n", meta.ID)
	fmt.Fprintf(ctx.Stdout, "type: %s\n", meta.Type)
	fmt.Fprintf(ctx.Stdout, "title: %s\n", orDash(meta.Title))
	fmt.Fprintf(ctx.Stdout, "author: %s\n", memoryAuthor(meta.OwnerDisplayName, meta.OwnerUserID))
	fmt.Fprintf(ctx.Stdout, "writer: %s\n", memoryWriterLine(meta.WriterKind, meta.WriterDetails))
	fmt.Fprintf(ctx.Stdout, "visibility: %s\n", meta.Visibility)
	fmt.Fprintf(ctx.Stdout, "updated: %s\n", meta.UpdatedAt)
	fmt.Fprintf(ctx.Stdout, "content_len: %d\n", meta.ContentLen)
	fmt.Fprintln(ctx.Stdout)
	shown := body
	truncated := false
	if capBytes > 0 && len(body) > capBytes {
		shown = body[:capBytes] // byte cap: the notice counts bytes, not runes
		truncated = true
	}
	fmt.Fprint(ctx.Stdout, string(shown))
	if !strings.HasSuffix(string(shown), "\n") {
		fmt.Fprintln(ctx.Stdout)
	}
	if truncated {
		fmt.Fprintf(ctx.Stdout, "[truncated: showed %d of %d bytes — use --max-bytes 0 for the full body]\n", capBytes, len(body))
	}
	return nil
}

// memoryAuthor answers a memory row's author for the table and the
// show header: the display name the server joins live from its users
// (polyforge#107), the stable owner id when the server names none (a
// pre-#107 aihub), "-" when neither rides.
func memoryAuthor(displayName, ownerID string) string {
	if displayName != "" {
		return displayName
	}
	if ownerID != "" {
		return ownerID
	}
	return "-"
}

// memoryWriterLine renders the writer of memory show's header: the kind
// with its VALUED details — "kind (k=v, ...)", keys sorted, scalars
// bare (numbers, booleans), anything nested as compact JSON. A detail
// whose value is null or the empty string is not listed at all (Q2:
// 有值的细节 — a key every test of which was skipped renders the kind
// alone); "-" on a row written before the writer columns existed.
func memoryWriterLine(kind *string, details json.RawMessage) string {
	if kind == nil {
		return "-"
	}
	var d map[string]json.RawMessage
	if json.Unmarshal(details, &d) != nil || d == nil {
		return *kind
	}
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var v any
		if err := json.Unmarshal(d[k], &v); err != nil {
			parts = append(parts, k+"="+string(d[k]))
			continue
		}
		switch t := v.(type) {
		case string:
			if t == "" {
				continue // unvalued: not listed
			}
			parts = append(parts, k+"="+t)
		case nil:
			continue // unvalued: not listed
		default:
			parts = append(parts, k+"="+string(d[k]))
		}
	}
	if len(parts) == 0 {
		return *kind
	}
	return *kind + " (" + strings.Join(parts, ", ") + ")"
}

// memoryJSONWithContent merges the fetched body into the server's
// metadata JSON as one CLI-added top-level "content" string member (the
// #132 --json shape: server metadata verbatim plus the full body). The
// server's bytes are kept verbatim — a map round-trip would reorder keys
// and reformat whitespace — by inserting the member before the object's
// closing brace. A non-object metadata body is a loud server-contract
// failure, never silently rewrapped.
func memoryJSONWithContent(metaRaw json.RawMessage, content string) (json.RawMessage, error) {
	esc, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	s := bytes.TrimRight(metaRaw, " \t\r\n")
	if len(s) > 0 && s[len(s)-1] == '}' {
		out := make([]byte, 0, len(s)+len(`,"content":`)+len(esc))
		out = append(out, s[:len(s)-1]...)
		out = append(out, `,"content":`...)
		out = append(out, esc...)
		out = append(out, '}')
		return out, nil
	}
	return nil, fmt.Errorf("memory show: metadata is not a JSON object (unexpected server response)")
}

// ---------------------------------------------------------------------------
// v1.5 session-model faces.
// ---------------------------------------------------------------------------
