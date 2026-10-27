package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// Rendering rules:
//   - --json: emit the server's response body as-is (raw passthrough).
//   - human (default): per-verb renderers below; anything without a
//     dedicated renderer falls back to pretty-printed JSON.
//   - errors: "pf: CORE-409: detail" with the code prominent; in --json
//     mode the typed envelope is re-emitted instead.

// emit prints the command payload: raw passthrough in --json mode ONLY.
func (ctx *Context) emit(raw []byte) {
	if !ctx.JSON {
		return
	}
	if len(raw) > 0 {
		ctx.Out(raw)
	}
}

// finish prints the command payload: raw passthrough in --json mode, the
// human renderer closure otherwise.
func (ctx *Context) finish(raw json.RawMessage, human func()) {
	ctx.emit(raw)
	if !ctx.JSON {
		human()
	}
}

// finishLine is finish with a one-line human answer.
func (ctx *Context) finishLine(raw json.RawMessage, format string, args ...any) {
	ctx.finish(raw, func() { fmt.Fprintf(ctx.Stdout, format, args...) })
}

// finishAs is finish with the human renderer over the payload decoded as
// T; a payload that does not decode renders nothing.
func finishAs[T any](ctx *Context, raw json.RawMessage, human func(*T)) {
	ctx.finish(raw, func() {
		var v T
		if json.Unmarshal(raw, &v) == nil {
			human(&v)
		}
	})
}

// finishOrRaw is finishAs whose fallback — a payload that does not decode
// as T, or a renderer answering false — is the pretty-printed raw body.
func finishOrRaw[T any](ctx *Context, raw json.RawMessage, human func(*T) bool) {
	ctx.finish(raw, func() {
		var v T
		if json.Unmarshal(raw, &v) != nil || !human(&v) {
			printJSON(ctx.Stdout, raw)
		}
	})
}

// itemList is the {"items":[...]} list answer shape.
type itemList[T any] struct {
	Items []T `json:"items"`
}

// Out writes to stdout.
func (ctx *Context) Out(data []byte) {
	ctx.Stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Fprintln(ctx.Stdout)
	}
}

// printJSON pretty-prints raw JSON for the human fallback.
func printJSON(w io.Writer, raw []byte) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		fmt.Fprintln(w, string(raw))
		return
	}
	buf.WriteTo(w)
	fmt.Fprintln(w)
}

// printTable renders a simple aligned text table. The last column is
// not padded (no trailing whitespace on output lines).
func printTable(w io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	head := make([]string, len(headers))
	for i, h := range headers {
		if i == len(headers)-1 {
			head[i] = h // last column: no padding (no trailing whitespace)
		} else {
			head[i] = pad(h, widths[i])
		}
	}
	fmt.Fprintln(w, strings.Join(head, "  "))
	for i := range headers {
		fmt.Fprint(w, strings.Repeat("-", widths[i]))
		if i < len(headers)-1 {
			fmt.Fprint(w, "  ")
		}
	}
	fmt.Fprintln(w)
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			if i == len(widths)-1 {
				cells[i] = cell
			} else {
				cells[i] = pad(cell, widths[i])
			}
		}
		fmt.Fprintln(w, strings.TrimRight(strings.Join(cells, "  "), " "))
	}
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// truncate shortens s for table cells.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// nullable renders a *string for tables.
func nullable(v *string) string {
	if v == nil {
		return "-"
	}
	return *v
}

// ---------------------------------------------------------------------------
// Human renderers.
// ---------------------------------------------------------------------------

// WIView is one rendered WI record: the wire work item plus the v1.5
// session audit fields, decoded ADDITIVELY from the raw server body
// (api.WorkItem does not carry them; an absent field renders "-").
// Rendering uses the session fingerprint — the
// PUBLIC handle — never the secret or its hash.
type WIView struct {
	api.WorkItem
	SessionFingerprint string `json:"session_fingerprint,omitempty"`
	SessionMachine     string `json:"session_machine,omitempty"`
	SessionStartedAt   string `json:"session_started_at,omitempty"`
	CleanPause         bool   `json:"clean_pause,omitempty"`
	// WaitingFor is the server-derived list of unfinished `blocks`
	// blockers of a queued work item (polyforge#161).
	WaitingFor []waitingBlocker `json:"waiting_for,omitempty"`
}

type waitingBlocker struct {
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

// waitingText renders a waiting_for list: "polyforge#3 (running), ...".
func waitingText(list []waitingBlocker) string {
	parts := make([]string, 0, len(list))
	for _, b := range list {
		parts = append(parts, fmt.Sprintf("%s (%s)", b.Slug, b.Status))
	}
	return strings.Join(parts, ", ")
}

// depsView is the two-sided dependency view (GET
// /work-items/{id}/dependencies).
type depsView struct {
	Blocking  []depEdge `json:"blocking"`
	BlockedBy []depEdge `json:"blocked_by"`
}

type depEdge struct {
	WI     string `json:"wi_id"`
	Kind   string `json:"kind"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

// renderDeps renders the `blocks` edges (the only kind the CLI exposes;
// polyforge#161) of a work item. nil renders nothing.
func renderDeps(w io.Writer, d *depsView) {
	if d == nil {
		return
	}
	side := func(edges []depEdge) string {
		var parts []string
		for _, e := range edges {
			if e.Kind != "blocks" {
				continue
			}
			name := e.Slug
			if name == "" {
				name = e.WI
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", name, orDash(e.Status)))
		}
		if len(parts) == 0 {
			return "-"
		}
		return strings.Join(parts, ", ")
	}
	fmt.Fprintf(w, "  dependencies:\n")
	fmt.Fprintf(w, "    after:  %s\n", side(d.BlockedBy))
	fmt.Fprintf(w, "    before: %s\n", side(d.Blocking))
}

// wiRow renders the shared per-WI cells (no segment). The PROJECT
// column is the #113 merged-presentation requirement: without it a
// multi-project list could not tell a row's project apart. The SESSION
// column is the v1.5 public fingerprint of the current holder ("-"
// when unheld).
func wiRow(it WIView) []string {
	slug := it.Slug
	if slug == "" {
		slug = it.ID
	}
	sess := "-"
	if it.SessionFingerprint != "" {
		sess = it.SessionFingerprint
	}
	return []string{
		slug, orDash(it.ProjectName), it.Status, sess, it.Priority, nullable(it.BundleName), truncate(it.Goal, 60),
	}
}

// renderWITable renders a WI list table. When any row waits for an
// unfinished blocker (polyforge#161 waiting_for), a WAITING-FOR column
// is inserted before GOAL; otherwise the table keeps its usual shape.
func renderWITable(w io.Writer, items []WIView) {
	waiting := false
	for _, it := range items {
		if len(it.WaitingFor) > 0 {
			waiting = true
			break
		}
	}
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		row := wiRow(it)
		if waiting {
			last := len(row) - 1
			row = append(row[:last:last], orDash(waitingText(it.WaitingFor)), row[last])
		}
		rows = append(rows, row)
	}
	headers := []string{"SLUG", "PROJECT", "STATUS", "SESSION", "PRIORITY", "BUNDLE", "GOAL"}
	if waiting {
		headers = []string{"SLUG", "PROJECT", "STATUS", "SESSION", "PRIORITY", "BUNDLE", "WAITING-FOR", "GOAL"}
	}
	printTable(w, headers, rows)
}

// renderWIDetail renders the detail record. The v1.5 session block
// (fingerprint, machine, owner, started-at, clean_pause — the m4 public
// audit fields) renders whenever the server body carries them.
func renderWIDetail(w io.Writer, wi *WIView) {
	name := wi.Slug
	if name == "" {
		name = wi.ID
	}
	fmt.Fprintf(w, "%s (%s)\n", name, wi.ID)
	fmt.Fprintf(w, "  status:   %s (priority %s)\n", wi.Status, wi.Priority)
	fmt.Fprintf(w, "  project:  %s\n", wi.ProjectName)
	fmt.Fprintf(w, "  goal:     %s\n", wi.Goal)
	if wi.Milestone != nil {
		fmt.Fprintf(w, "  milestone: %s\n", *wi.Milestone)
	}
	if len(wi.Labels) > 0 {
		fmt.Fprintf(w, "  labels:   %s\n", strings.Join(wi.Labels, ","))
	}
	fmt.Fprintf(w, "  bundle:   %s\n", nullable(wi.BundleName))
	if wi.Assignee != nil {
		fmt.Fprintf(w, "  assignee: %s\n", *wi.Assignee)
	}
	if wi.WaitingOn != nil {
		fmt.Fprintf(w, "  waiting:  %s\n", *wi.WaitingOn)
	}
	if len(wi.WaitingFor) > 0 {
		fmt.Fprintf(w, "  waiting-for: %s (only a wrapped blocker releases; `pf file dep rm` removes an edge)\n", waitingText(wi.WaitingFor))
	}
	if wi.SessionFingerprint != "" {
		fmt.Fprintf(w, "  session:  %s", wi.SessionFingerprint)
		if wi.SessionMachine != "" {
			fmt.Fprintf(w, " (machine %s)", orDash(wi.SessionMachine))
		}
		if wi.SessionStartedAt != "" {
			fmt.Fprintf(w, " since %s", wi.SessionStartedAt)
		}
		fmt.Fprintln(w)
	}
	if wi.CleanPause {
		fmt.Fprintf(w, "  clean-pause: yes (operator pause; restart will not auto-resume)\n")
	}
	fmt.Fprintf(w, "  created:  %s\n", wi.CreatedAt)
	if wi.ClosedAt != nil {
		fmt.Fprintf(w, "  closed:   %s\n", *wi.ClosedAt)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// memorySearchHit is the memory search answer row as the server sends it
// (memory.Entry plus the excerpt extension): there is no `content` on the
// wire — only content_excerpt, which SearchMemories always asks for.
type memorySearchHit struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	Title          *string  `json:"title"`
	ContentExcerpt string   `json:"content_excerpt"`
	UpdatedAt      string   `json:"updated_at"`
	Tags           []string `json:"tags"`
}

// renderMemories renders a memory search result: the SUMMARY column is
// the title when the memory has one, else the excerpt's first line.
func renderMemories(w io.Writer, items []memorySearchHit) {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		tags := "-"
		if len(it.Tags) > 0 {
			tags = strings.Join(it.Tags, ",")
		}
		summary := firstLine(it.ContentExcerpt)
		if it.Title != nil && strings.TrimSpace(*it.Title) != "" {
			summary = *it.Title
		}
		rows = append(rows, []string{it.ID, it.Type, truncate(orDash(summary), 50), it.UpdatedAt, tags})
	}
	printTable(w, []string{"ID", "TYPE", "SUMMARY", "UPDATED", "TAGS"}, rows)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// renderReloadStatus is the cli.toml hot-reload line: keys pending a
// daemon restart and keys an explicit daemon flag overrides (workbench#3).
func renderReloadStatus(w io.Writer, r *ReloadStatus) {
	if r == nil {
		return
	}
	var parts []string
	if len(r.Pending) > 0 {
		parts = append(parts, "pending restart: "+strings.Join(r.Pending, ", "))
	}
	if len(r.Overridden) > 0 {
		parts = append(parts, "overridden by flag: "+strings.Join(r.Overridden, ", "))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(w, "reload: %s\n", strings.Join(parts, "; "))
}

// renderStatus renders the daemon status() bookkeeping list. The
// WORKSPACE line is the #103 owner face: the active capsule base the
// daemon was launched with (-workspace; capsules live at
// <workspace>/capsules/<project>-<seq>) — exactly one line, no annotation. The
// QUESTION column is the D6 owner face: any job the daemon reports with a
// pending question shows its question id ("-" otherwise), so an agent
// blocked on a human is impossible to miss at a glance. The SPAWN column
// is the v1.5 B2 derivation counter and SESSION the public fingerprint of
// the job's session ("-" when unheld). STEP-AGE and LAST-WORK are the
// owner liveness face: mm:ss since the current dispatch began, and since
// its harness last did REAL work ("-" when no dispatch is in flight or
// no work signal exists yet). UI-noise records never refresh LAST-WORK,
// so a wedged-but-chattering job reads as stale — the pilot's 2.7-hour
// double-blind wedge had no such surface.
func renderStatus(w io.Writer, s *StatusResponse) {
	fmt.Fprintf(w, "daemon: %s", s.MachineID)
	if s.DisplayName != "" {
		fmt.Fprintf(w, " (%s)", s.DisplayName)
	}
	fmt.Fprintf(w, "  auto: %s  max: %d", onOff(s.Auto), s.Queue.MaxConcurrent)
	if s.ShuttingDown {
		fmt.Fprint(w, "  (shutting down)")
	}
	fmt.Fprintf(w, "  queue: %d pending / %d running\n",
		s.Queue.Pending, s.Queue.Running)
	renderModelHealthSummary(w, s.ModelHealth)
	renderReloadStatus(w, s.Reload)
	renderTasksSummary(w, s.Tasks)
	// polyforge#113: the workspace SET, sorted for stable display — a
	// multi-workspace daemon must not present one workspace as if it
	// were the only one.
	wsSet := append([]string(nil), s.Workspaces...)
	sort.Strings(wsSet)
	fmt.Fprintf(w, "workspaces: %s\n\n", strings.Join(wsSet, ", "))
	showOrigin, showCandidate := false, false
	for _, j := range s.Jobs {
		showOrigin = showOrigin || j.Origin != nil
		showCandidate = showCandidate || j.Candidate != ""
	}
	rows := make([][]string, 0, len(s.Jobs))
	for _, j := range s.Jobs {
		pid := "-"
		if j.AgentPID > 0 {
			pid = strconv.Itoa(j.AgentPID)
		}
		question := "-"
		if j.QuestionID != "" {
			question = j.QuestionID
		}
		sess := "-"
		if j.SessionFingerprint != "" {
			sess = j.SessionFingerprint
		}
		spawn := "-"
		if j.SpawnN > 0 {
			spawn = "#" + strconv.Itoa(j.SpawnN)
		}
		stepAge, lastWork := "-", "-"
		if j.StepAgeSec != nil {
			stepAge = mmssAgo(*j.StepAgeSec)
		}
		if j.LastWorkAgoSec != nil {
			lastWork = mmssAgo(*j.LastWorkAgoSec)
		}
		origin := "-"
		if j.Origin != nil {
			origin = j.Origin.Kind
			if j.Origin.ChatID != "" {
				origin += ":" + j.Origin.ChatID
			} else if j.Origin.Kind == "chat" { // polyforge#208: chat-less chat origin (runtime.OriginChat with no id)
				origin += ":none"
			}
		}
		row := []string{api.DisplayName(j.Slug, j.WorkItemID), j.Status, sess, spawn, pid, question, stepAge, lastWork}
		if showCandidate {
			row = append(row, orDash(j.Candidate))
		}
		if showOrigin {
			row = append(row, origin)
		}
		rows = append(rows, row)
	}
	head := []string{"WORK ITEM", "STATUS", "SESSION", "SPAWN", "AGENT PID", "QUESTION", "STEP-AGE", "LAST-WORK"}
	if showCandidate { // polyforge#176: the candidate a dispatch runs on, effort included
		head = append(head, "CANDIDATE")
	}
	if showOrigin { // the column appears only when some job has one
		head = append(head, "ORIGIN")
	}
	printTable(w, head, rows)
}

// mmssAgo renders whole seconds as the mm:ss figure the STEP-AGE and
// LAST-WORK columns carry (minutes grow unbounded past an hour —
// 97:30 reads faster than 1h37m on a scan).
func mmssAgo(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

// renderUsers renders the M1 list.
func renderUsers(w io.Writer, items []api.User) {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, []string{it.ID, it.UserType, it.Role, it.DisplayName})
	}
	printTable(w, []string{"ID", "TYPE", "ROLE", "DISPLAY NAME"}, rows)
}

// renderApiKeys renders the M2 list (metadata only, never material).
func renderApiKeys(w io.Writer, raw json.RawMessage) {
	var l struct {
		Items []struct {
			ID        string  `json:"id"`
			UserID    string  `json:"user_id"`
			ProjectID *string `json:"project_id"`
			RevokedAt *string `json:"revoked_at"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		printJSON(w, raw)
		return
	}
	rows := make([][]string, 0, len(l.Items))
	for _, it := range l.Items {
		rows = append(rows, []string{it.ID, it.UserID, nullable(it.ProjectID), nullable(it.RevokedAt)})
	}
	printTable(w, []string{"ID", "USER", "SCOPE", "REVOKED"}, rows)
}

// renderMachines renders the daemon liveness list (M3, admin read
// face): RUNTIME is the daemon's reported go version ("-" when it has
// none), LIVE WI the bare count of live work item ids, and STATE the
// server-computed stale projection (STALE vs alive). The aihub-side
// registration keeps its machine vocabulary; the user-facing wording
// names daemons.
func renderMachines(w io.Writer, list *aihubclient.MachinesList) {
	fmt.Fprintf(w, "daemons: %d (stale threshold %ds)\n", list.Count, list.StaleAfterSeconds)
	rows := make([][]string, 0, len(list.Machines))
	for _, m := range list.Machines {
		age := "?"
		if m.HeartbeatAgeSeconds != nil {
			age = fmt.Sprintf("%ds", *m.HeartbeatAgeSeconds)
		}
		rows = append(rows, []string{
			m.ID, orDash(m.Role), orDash(m.Launcher), m.Mode, orDash(m.RuntimeVersion),
			strconv.Itoa(len(m.LiveWIIDs)), age, m.LastHeartbeatAt,
			map[bool]string{true: "STALE", false: "alive"}[m.Stale],
		})
	}
	printTable(w, []string{"ID", "ROLE", "LAUNCHER", "MODE", "RUNTIME", "LIVE WI", "HEARTBEAT AGE", "LAST HEARTBEAT", "STATE"}, rows)
}

// renderProjects renders the M3 list.
func renderProjects(w io.Writer, items []api.Project) {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		vis := "private"
		if it.Visible {
			vis = "visible"
		}
		rows = append(rows, []string{it.ID, it.Name, vis})
	}
	printTable(w, []string{"ID", "NAME", "DIRECTORY"}, rows)
}

// renderModelHealthSummary renders the additive model-health block of
// `pf ops status` after the queue line: every current warning with its
// numbers and the pointer to `pf ops models`, or one explicit silence
// line (never an omitted block).
func renderModelHealthSummary(w io.Writer, s *modelhealth.Summary) {
	if s == nil {
		fmt.Fprintln(w, "model health: no model-health summary from this daemon")
		return
	}
	if len(s.Warnings) == 0 {
		fmt.Fprintln(w, "model health: no model-health warnings")
		return
	}
	for _, warning := range s.Warnings {
		step := warning.Step
		if step == "" {
			step = "-"
		}
		numbers := fmt.Sprintf("%d model death(s)", warning.ModelDeaths)
		if warning.Dispatches > 0 {
			numbers += fmt.Sprintf(" / %d dispatch(es), %.1f%%", warning.Dispatches, warning.Rate*100)
		}
		fmt.Fprintf(w, "model health: WARN %s step %s — %s (%s)\n",
			modelhealth.Key(warning.Model, warning.Effort), step, numbers, warning.Rule)
	}
	fmt.Fprintln(w, "model health: see `pf ops models` for the full profile")
}

// renderModels renders the model+effort x step profile table. The
// MODEL column is the model+effort key (an empty effort renders as the
// model alone, never a trailing ':'), the window is stated above it and
// non-model deaths sit beside the model rate.
func renderModels(w io.Writer, p *modelhealth.Profile) {
	fmt.Fprintf(w, "window: %s (from %s to %s)\n", p.Window.Duration, p.Window.From, p.Window.To)
	if len(p.Warnings) > 0 {
		for _, warning := range p.Warnings {
			step := warning.Step
			if step == "" {
				step = "-"
			}
			numbers := fmt.Sprintf("%d model death(s)", warning.ModelDeaths)
			if warning.Dispatches > 0 {
				numbers += fmt.Sprintf(" / %d dispatch(es), %.1f%%", warning.Dispatches, warning.Rate*100)
			}
			fmt.Fprintf(w, "warning: %s step %s — %s (%s)\n",
				modelhealth.Key(warning.Model, warning.Effort), step, numbers, warning.Rule)
		}
	}
	rows := make([][]string, 0, len(p.Cells))
	for _, c := range p.Cells {
		mean := "-"
		if c.MeanWallMs > 0 {
			mean = (time.Duration(c.MeanWallMs) * time.Millisecond).Truncate(time.Second).String()
		}
		last := c.LastCause
		if last == "" {
			last = "-"
		}
		rows = append(rows, []string{
			modelhealth.Key(c.Model, c.Effort), c.Step,
			strconv.Itoa(c.Dispatches), strconv.Itoa(c.Successes), strconv.Itoa(c.ModelDeaths),
			fmt.Sprintf("%.1f%%", c.Rate*100), mean, strconv.Itoa(c.NonModelDeaths), last,
		})
	}
	printTable(w, []string{"MODEL", "STEP", "DISPATCHES", "SUCCESSES", "MODEL-DEATHS", "RATE", "MEAN-WALL", "NON-MODEL", "LAST-CAUSE"}, rows)
}

// renderTasksSummary prints the one-line task summary of `pf ops status`
// (workbench#40); nothing for an older daemon that carries none.
func renderTasksSummary(w io.Writer, t *task.Summary) {
	if t == nil {
		return
	}
	fmt.Fprintf(w, "tasks: %d/%d running (completed %d, capped %d, failed %d, interrupted %d)\n",
		t.Running, t.Limit, t.Completed, t.Capped, t.Failed, t.Interrupted)
}
