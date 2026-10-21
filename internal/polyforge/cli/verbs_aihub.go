package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// ---------------------------------------------------------------------------
// file — create/edit/show work items (direct to aihub).
// ---------------------------------------------------------------------------

var cmdFile = &Command{
	Name:    "file",
	Group:   GroupData,
	Summary: "Create, edit or show a work item; manage its dependencies",
	Usage: "pf file create --project P --goal G [--repo NAME]... [--after ID]... [flags] | --stdin\n" +
		"pf file edit <id> [flags] | --stdin\n" +
		"pf file show <id>\n" +
		"pf file dep add <blocking> <blocked>\n" +
		"pf file dep rm <blocking> <blocked>\n" +
		"pf file dep list <id>\n\n" +
		"Dependencies are `blocks` edges inside one project: the blocked work\n" +
		"item is not claimed until every blocker is WRAPPED. A blocker must be\n" +
		"an execution ticket (bundle bound). A failed or canceled blocker keeps\n" +
		"it waiting — remove the edge to proceed. --after is atomic: a bad\n" +
		"blocker creates nothing.",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("file: expected create|edit|show|dep (see `pf help file`)")
		}
		sub, rest := args[0], args[1:]
		switch sub {
		case "create":
			return fileCreate(ctx, rest)
		case "edit":
			return fileEdit(ctx, rest)
		case "show":
			return fileShow(ctx, rest)
		case "dep":
			return fileDep(ctx, rest)
		default:
			return fmt.Errorf("file: unknown action %q", sub)
		}
	},
}

// osGetwd is the file-create inference's cwd seam: the production value
// is the process working directory; tests pin it to a fixture path.
var osGetwd = os.Getwd

// requiredProjectFlagError is the polyforge#113 required-flag error,
// byte-identical wherever inference cannot answer (no containing
// workspace, no yaml). It never mutates.
const requiredProjectFlagError = "file create: --project is required — there is no default project (polyforge#113); workspaces and their projects are declared in [workspace.\"<path>\"] sections of cli.toml (`pf doctor` checks the mapping)"

// inferProjectFromCwd is the file-create default: the process cwd
// resolved against the expanded [workspace] mapping of cli.toml by
// containment (cwd equal to or inside the workspace root). Exactly one
// containing workspace that lists exactly ONE project answers it — the
// create proceeds as if --project had been passed. Every other shape
// asks for --project; inference never ambient-picks (the #113
// principle).
func inferProjectFromCwd(ctx *Context) (string, error) {
	cwd, err := osGetwd()
	if err != nil || ctx.Cfg.Mapping == nil || ctx.Cfg.Mapping.Empty() {
		return "", fmt.Errorf(requiredProjectFlagError)
	}
	var containing []string
	for _, ws := range ctx.Cfg.Mapping.Workspaces() {
		if cwd == ws || strings.HasPrefix(cwd, ws+string(os.PathSeparator)) {
			containing = append(containing, ws)
		}
	}
	switch {
	case len(containing) == 0:
		return "", fmt.Errorf(requiredProjectFlagError)
	case len(containing) > 1:
		return "", fmt.Errorf("file create: --project is required — the working directory is inside more than one mapped workspace (%s); pass --project explicitly (placement stays unambiguous, polyforge#113)", strings.Join(containing, ", "))
	}
	projects := ctx.Cfg.Mapping.ProjectsOf(containing[0])
	if len(projects) != 1 {
		return "", fmt.Errorf("file create: --project is required — workspace %s maps %d projects (%s); pass --project explicitly (no default is invented)", containing[0], len(projects), strings.Join(projects, ", "))
	}
	return projects[0], nil
}

// requireMappedProject validates a placement verb's --project (file
// create, recall, remember) against the loaded mapping (polyforge#113).
// An unmapped (or unvalidatable — no mapping configured) project is a
// loud local error — wrapping the typed *mapping.UnmappedError, naming
// the projects this machine does map and pointing at the [workspace]
// shape and `pf doctor` — and nothing is sent to aihub.
func requireMappedProject(ctx *Context, verb, project string) error {
	if ctx.Cfg.Mapping.Empty() {
		return fmt.Errorf("%s: project %q cannot be placed: no [workspace] mapping is configured in cli.toml — add a [workspace.\"<path>\"] section (`pf doctor` checks the mapping)", verb, project)
	}
	if _, err := ctx.Cfg.Mapping.WorkspaceFor(project); err != nil {
		return fmt.Errorf("%s: %w; mapped here: %s", verb, err, strings.Join(ctx.Cfg.Mapping.Projects(), ", "))
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
	f.Var(&attrs, "attr", "business attr key=json-value (repeatable)")
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
		req.Attrs = map[string]any{}
		for _, kv := range attrs {
			v, err := parseAttrValue(kv.Value)
			if err != nil {
				return fmt.Errorf("--attr %s: %w", kv.Key, err)
			}
			req.Attrs[kv.Key] = v
		}
	}

	raw, err := ctx.aihub().CreateWorkItem(req)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(wi *WIView) {
		fmt.Fprintln(ctx.Stdout)
		renderWIDetail(ctx.Stdout, wi)
	})
	return nil
}

// depErr renders a dependency write failure with the server's reason
// (DEPENDENCY_CYCLE ...) next to the code, which the plain envelope
// line would otherwise drop.
func depErr(err error) error {
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
			return depErr(err)
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
		useStdin  = f.Bool("stdin", false, "read the full PATCH-body JSON from stdin")
	)
	f.Var(&bundle, "bundle", "bundle_name (empty string unbinds); aihub accepts it only while the work item is queued and never claimed")
	f.Var(&goal, "goal", "new goal")
	f.Var(&content, "content", "new markdown background; '-' reads stdin, '@file' reads a file")
	f.Var(&labels, "label", "labels (repeatable; replaces the whole set)")
	f.Var(&priority, "priority", "low|normal|high|urgent")
	f.Var(&milestone, "milestone", "milestone (empty string clears it)")
	f.Var(&waitingOn, "waiting-on", "waiting_on value")
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
	raw, err := ctx.aihub().PatchWorkItem(id, body)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(wi *WIView) {
		fmt.Fprintln(ctx.Stdout)
		renderWIDetail(ctx.Stdout, wi)
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

// ---------------------------------------------------------------------------
// queue — WI list + ready-queue (direct to aihub).
// ---------------------------------------------------------------------------

var cmdQueue = &Command{
	Name:    "queue",
	Group:   GroupData,
	Summary: "List work items / show the ready queue",
	Usage: "pf queue [--project P] [--status S] [--limit N]\n" +
		"pf queue --ready [--project P]\n\n" +
		"Without --project: one merged list over ALL mapped projects (the\n" +
		"[workspace] mapping in cli.toml); --project P is passed to aihub\n" +
		"unvalidated (read-only: the mapping governs placement, not knowledge).",
	Setup: func(fs *flag.FlagSet) {
		fs.String("project", "", "project name (optional: absent = all mapped projects, merged; present = passed to aihub unvalidated)")
		fs.Bool("ready", false, "show the ready queue instead of the list")
		fs.String("status", "", "filter: queued|running|paused|blocked|wrapped|failed|canceled")
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
		proj := stringFlag(fs, "project")
		if proj == "" {
			// polyforge#113: no ambient default project exists. Without
			// --project the verb covers ALL mapped projects in one merged
			// presentation; with no mapping at all it fails loudly with
			// guidance — it never silently lists nothing.
			if ctx.Cfg.Mapping.Empty() {
				return fmt.Errorf("queue: --project is required — no [workspace] mapping is configured in cli.toml; add a [workspace.\"<path>\"] section (`pf doctor` checks the mapping) or pass --project P")
			}
			return queueMerged(ctx, boolFlag(fs, "ready"), stringFlag(fs, "status"), limit)
		}
		// An explicit --project passes through to aihub UNVALIDATED
		// (mapped or not — exactly one query; the mapping governs
		// placement, not knowledge).
		var (
			raw json.RawMessage
			err error
		)
		if boolFlag(fs, "ready") {
			raw, err = ctx.aihub().ReadyQueueRaw(proj)
		} else {
			raw, err = ctx.aihub().ListWorkItemsRaw(proj, stringFlag(fs, "status"), limit)
		}
		if err != nil {
			return err
		}
		// Both faces render the items segment; --json is the raw server body.
		finishAs(ctx, raw, func(l *itemList[WIView]) {
			fmt.Fprintf(ctx.Stdout, "%d work item(s)\n", len(l.Items))
			renderWITable(ctx.Stdout, l.Items)
		})
		return nil
	},
}

// queueMerged is queue without --project over a non-empty mapping: one
// merged presentation of every mapped project's list (or ready queue),
// each row's project identifiable (PROJECT column; --json emits ONE
// merged items array — records already carry their project server-side).
// --limit applies per-project (each query is one server call); a
// per-project fetch failure fails the verb naming the project — fake
// coverage is worse than none. Determinism: mapping declaration order,
// then the server's own order within each project.
func queueMerged(ctx *Context, ready bool, status string, limit int) error {
	projects := ctx.Cfg.Mapping.Projects()
	var items []WIView
	for _, p := range projects {
		raw, err := ctx.aihub().ListWorkItemsRaw(p, status, limit)
		if ready {
			raw, err = ctx.aihub().ReadyQueueRaw(p)
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
	if ctx.JSON {
		// ONE merged document.
		doc := struct {
			Items []WIView `json:"items"`
			Total int      `json:"total"`
		}{Items: items, Total: len(items)}
		if b, err := json.Marshal(doc); err == nil {
			ctx.Out(b)
		}
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "%d work item(s)\n", len(items))
	renderWITable(ctx.Stdout, items)
	return nil
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
	Summary: "Search project memories",
	Usage:   "pf recall --project P <query> [--any] [--type T] [--tags a,b] [--limit N]",
	Setup: func(fs *flag.FlagSet) {
		fs.String("project", "", "project name (required; validated against the [workspace] mapping)")
		fs.String("q", "", "query text (alternative to the positional argument)")
		fs.String("type", "", "memory type filter: exact (experience.debug), prefix (experience.*), or * (all types)")
		fs.String("tags", "", "comma-separated tags")
		fs.Int("limit", 20, "max results (1..200; default 20)")
		fs.Bool("any", false, "ranked any-term match (memories sharing ANY of the query's words, best first) instead of the default every-word match; for long queries")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		q := stringFlag(fs, "q")
		if q == "" {
			q = strings.Join(args, " ")
		}
		if strings.TrimSpace(q) == "" {
			return fmt.Errorf("recall: a query is required (positional or --q)")
		}
		// Local flag validation, ahead of any config resolution or wire
		// call: a typo fails loudly here and no request is sent. The
		// default-20 limit is the CLI's product choice; the endpoint's
		// own 50 default stays a wire backstop for other callers.
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
		if err := requireMappedProject(ctx, "recall", proj); err != nil && ctx.Step == "" {
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
		raw, err := ctx.searchMemories(proj, q, memType, tagList, limit, match)
		if err != nil {
			return err
		}
		finishAs(ctx, raw, func(l *itemList[memorySearchHit]) {
			fmt.Fprintf(ctx.Stdout, "%d hit(s)\n", len(l.Items))
			renderMemories(ctx.Stdout, l.Items)
		})
		return nil
	},
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
		if err := requireMappedProject(ctx, "remember", proj); err != nil && ctx.Step == "" {
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
		if ctx.Step != "" {
			return stepRemember(ctx, fs, memType, proj, content)
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
	ID         string `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"`
	UpdatedAt  string `json:"updated_at"`
	ContentLen int64  `json:"content_len"`
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
	f := subFlags(ctx, "memory show")
	maxBytes := f.Int("max-bytes", defaultMemoryShowCap,
		"human-mode body cap in bytes (default 262144; 0 = unlimited)")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("memory show: expected exactly one memory id")
	}
	capBytes := *maxBytes
	if capBytes < 0 {
		return fmt.Errorf("memory show: --max-bytes must be >= 0 (0 = unlimited)")
	}
	id := rest[0]
	metaRaw, body, err := ctx.readMemory(id)
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
