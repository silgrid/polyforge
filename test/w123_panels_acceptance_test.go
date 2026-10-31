package test

// w123_panels_acceptance_test.go: the e2e acceptance suite for
//'s pf-side half — `pf recall` browse mode (human
// direct-to-aihub) and the bounded prompt summary on GET /task/list.
//
// Authored pre-code against the capsule's artifacts/spec.md (and the route
// and field names the plan fixes). Every kept scenario is an
// assertion-level RED on the pre-implementation tree: `--after`/`--sort`
// do not exist yet (a flag-parse refusal, never a build error), an empty
// query is refused as "a query is required", and the daemon list row has
// no prompt_summary key. Everything runs through the REAL binaries this
// repo's acceptance harness builds: the real aihub + real daemon on their
// own sockets, the real on-disk aihub.db for created/updated ordering, and
// the real `pf` CLI.
//
// Scenario map (document order is artifacts/e2e-scenarios.md):
//   R1 First page: 20 rows, updated-desc, total + next_cursor
//   R2 Cursor walk covers exactly total, no duplicate, no skipped id
//   R3 sort=updated|created; any other sort is MEM-400
//   R4 --limit defaults to 20; out-of-range refused before any request
//   R5 keyword + --after/--sort is MEM-400; plain keyword recall unchanged
//   R6 --any with no query is refused locally before any request
//   R7 Browse reachability documented in the verb page and the route table
//   T1 A long multi-line prompt yields a <=120 code-point first-line summary
//   T2 An empty prompt yields an empty summary and the list still reads
//
// Existing behaviour (regression only, no scenario authored): the daemon
// proxies already refuse an empty q before forwarding and already drop
// after/sort (internal/polyforge/daemon/stepmem_test.go, tasks_test.go);
// `/task/list`+`/task/show` reachability is unchanged.

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// w123Page is the browse answer's passthrough shape.
type w123Page struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	Total      int     `json:"total"`
	NextCursor *string `json:"next_cursor"`
}

// w123Cfg writes the human-path cli.toml mapping the CLI needs for a
// mapped project (the human path refuses an unmapped project locally).
func w123Cfg(t *testing.T) string {
	t.Helper()
	return mappingConfigPath(t, t.TempDir(), []string{v1Project})
}

// w123Run runs the REAL pf CLI once and answers combined output + exit
// code (no assertion: several scenarios pin refusals by exit code/text).
func w123Run(t *testing.T, inst *v1Install, cfg string, extraEnv []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", cfg}, args...)...)
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PF_SERVER=") || strings.HasPrefix(kv, "PF_API_KEY=") ||
			strings.HasPrefix(kv, "PF_USER_ID=") || strings.HasPrefix(kv, "PF_DAEMON_SOCKET=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PF_SERVER="+inst.url, "PF_API_KEY="+inst.adminKey, "PF_USER_ID=usr_root")
	env = append(env, extraEnv...)
	cmd.Env = env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// w123Browse runs `pf --json recall` and decodes the passthrough page.
func w123Browse(t *testing.T, inst *v1Install, cfg string, args ...string) w123Page {
	t.Helper()
	out, code := w123Run(t, inst, cfg, nil, append([]string{"--json", "recall", "--project", v1Project}, args...)...)
	if code != 0 {
		t.Fatalf("pf recall %v: exit %d: %s", args, code, out)
	}
	var page w123Page
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("recall --json answer is not the browse page: %v (%s)", err, out)
	}
	return page
}

func w123IDs(page w123Page) []string {
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func w123Seed(t *testing.T, inst *v1Install, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		ids = append(ids, w103Memory(t, inst, v1Project, "doc.note",
			"w123 browse row", "w123 browse body", nil, "project", nil))
	}
	return ids
}

// TestW123RecallBrowseFirstPage: R1.
func TestW123RecallBrowseFirstPage(t *testing.T) {
	inst := newV1Install(t)
	ids := w123Seed(t, inst, 25)
	page := w123Browse(t, inst, w123Cfg(t))
	got := w123IDs(page)
	if len(got) != 20 {
		t.Fatalf("a browse page without --limit = %d rows, want the default 20", len(got))
	}
	if page.Total != 25 {
		t.Fatalf("browse total = %d, want 25 (the whole project)", page.Total)
	}
	if page.NextCursor == nil || *page.NextCursor == "" {
		t.Fatalf("a 25-entry browse page of 20 must carry next_cursor (answer has none)")
	}
	if got[0] != ids[24] {
		t.Fatalf("default browse order = %s first, want the newest-updated memory %s", got[0], ids[24])
	}
}

// TestW123RecallBrowseWalkCursor: R2.
func TestW123RecallBrowseWalkCursor(t *testing.T) {
	inst := newV1Install(t)
	ids := w123Seed(t, inst, 45)
	cfg := w123Cfg(t)
	first := w123Browse(t, inst, cfg)
	total := first.Total
	if total != 45 {
		t.Fatalf("browse total = %d, want 45", total)
	}
	seen := map[string]int{}
	var walk []string
	page := first
	for n := 0; n < 10; n++ {
		for _, id := range w123IDs(page) {
			seen[id]++
			walk = append(walk, id)
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		page = w123Browse(t, inst, cfg, "--after", *page.NextCursor)
	}
	if len(walk) != total {
		t.Fatalf("the walk returned %d entries, want exactly total=%d (no skip, no duplicate)", len(walk), total)
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("the walk saw %s %d times, want exactly once", id, seen[id])
		}
	}
}

// TestW123RecallBrowseSort: R3.
func TestW123RecallBrowseSort(t *testing.T) {
	inst := newV1Install(t)
	m1 := w103Memory(t, inst, v1Project, "doc.note", "w123 sort one", "b1", nil, "project", nil)
	m2 := w103Memory(t, inst, v1Project, "doc.note", "w123 sort two", "b2", nil, "project", nil)
	m3 := w103Memory(t, inst, v1Project, "doc.note", "w123 sort three", "b3", nil, "project", nil)
	cfg := w123Cfg(t)
	w103Exec(t, w103DB(t, inst), `UPDATE memories SET created_at=?1 WHERE id=?2`, "2020-01-01T00:00:00Z", m3)
	if got := strings.Join(w123IDs(w123Browse(t, inst, cfg, "--sort", "updated")), ","); got != strings.Join([]string{m3, m2, m1}, ",") {
		t.Fatalf("--sort updated = %s, want newest-updated first %s", got, strings.Join([]string{m3, m2, m1}, ","))
	}
	if got := strings.Join(w123IDs(w123Browse(t, inst, cfg, "--sort", "created")), ","); got != strings.Join([]string{m2, m1, m3}, ",") {
		t.Fatalf("--sort created = %s, want creation-descending %s (the backdated entry last)", got, strings.Join([]string{m2, m1, m3}, ","))
	}
	out, code := w123Run(t, inst, cfg, nil, "recall", "--project", v1Project, "--sort", "asc")
	if code == 0 || !strings.Contains(out, "MEM-400") {
		t.Fatalf("--sort asc = exit %d %q, want aihub's MEM-400 refusal", code, out)
	}
	if !strings.Contains(out, "updated") || !strings.Contains(out, "created") {
		t.Fatalf("the MEM-400 refusal must name both legal sort values: %q", out)
	}
}

// TestW123RecallBrowseLimit: R4.
func TestW123RecallBrowseLimit(t *testing.T) {
	inst := newV1Install(t)
	w123Seed(t, inst, 25)
	cfg := w123Cfg(t)
	if n := len(w123IDs(w123Browse(t, inst, cfg, "--limit", "3"))); n != 3 {
		t.Fatalf("--limit 3 = %d rows, want 3", n)
	}
	// No --limit asks for 20 (R1 pins the value; this pins it again on a
	// second, fresh call so the default is not request-order luck).
	if n := len(w123IDs(w123Browse(t, inst, cfg))); n != 20 {
		t.Fatalf("no --limit = %d rows, want 20", n)
	}
	// Out-of-range limits are refused LOCALLY: the server address is
	// unreachable, so a request would fail with a connection error instead
	// of the 1..200 refusal.
	unreachable := []string{"PF_SERVER=http://127.0.0.1:1"}
	for _, bad := range []string{"0", "201", "-4"} {
		out, code := w123Run(t, inst, cfg, unreachable, "recall", "--project", v1Project, "--limit", bad)
		if code == 0 {
			t.Fatalf("--limit %s must be refused; it succeeded: %q", bad, out)
		}
		if !strings.Contains(out, "1..200") {
			t.Fatalf("--limit %s refusal = %q, want the local 1..200 bound message", bad, out)
		}
		if strings.Contains(strings.ToLower(out), "connection refused") || strings.Contains(out, "dial") {
			t.Fatalf("--limit %s was checked only after a request: %q", bad, out)
		}
	}
}

// TestW123KeywordMixRefused: R5.
func TestW123KeywordMixRefused(t *testing.T) {
	inst := newV1Install(t)
	w103Memory(t, inst, v1Project, "experience.debug", "w123 needle", "the w123needle retry lesson", nil, "project", nil)
	cfg := w123Cfg(t)
	for _, extra := range [][]string{{"--after", "cur-1"}, {"--sort", "created"}} {
		out, code := w123Run(t, inst, cfg, nil, append([]string{"recall", "--project", v1Project, "w123needle"}, extra...)...)
		if code == 0 || !strings.Contains(out, "MEM-400") {
			t.Fatalf("keyword + %v = exit %d %q, want MEM-400 (no keyword results)", extra, code, out)
		}
		if strings.Contains(out, "hit(s)") || strings.Contains(out, "w123 needle") {
			t.Fatalf("the mixed shape returned keyword results: %q", out)
		}
	}
	out, code := w123Run(t, inst, cfg, nil, "recall", "--project", v1Project, "w123needle")
	if code != 0 || !strings.Contains(out, "1 hit(s)") {
		t.Fatalf("a plain keyword recall must keep working: exit %d %q", code, out)
	}
}

// TestW123RecallAnyBrowseRefused: R6.
func TestW123RecallAnyBrowseRefused(t *testing.T) {
	inst := newV1Install(t)
	// The server is unreachable and no mapping exists: the refusal must
	// happen at flag validation, before either the config read or a request.
	out, code := w123Run(t, inst, hermeticConfigPath(t), []string{"PF_SERVER=http://127.0.0.1:1"},
		"recall", "--project", v1Project, "--any")
	if code == 0 {
		t.Fatalf("--any with no query must be refused locally; it succeeded: %q", out)
	}
	if !strings.Contains(out, "--any") && !strings.Contains(out, "human") {
		t.Fatalf("the refusal must name --any / the browse human path: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "connection refused") || strings.Contains(out, "no [workspace] mapping") {
		t.Fatalf("--any was validated only after config/network access: %q", out)
	}
}

// TestW123RecallBrowseReachabilityDocumented: R7.
func TestW123RecallBrowseReachabilityDocumented(t *testing.T) {
	recall := strings.ToLower(w103Doc(t, "skills/polyforge/verbs/recall.md"))
	routes := strings.ToLower(w103Doc(t, "docs/reference/uds-routes.md"))
	for name, doc := range map[string]string{"recall.md": recall, "uds-routes.md": routes} {
		if !strings.Contains(doc, "browse") {
			t.Fatalf("%s does not mention browse mode", name)
		}
		if !strings.Contains(doc, "human") {
			t.Fatalf("%s does not say browse is the human direct-aihub path", name)
		}
	}
	if !strings.Contains(recall, "--after") || !strings.Contains(recall, "--sort") {
		t.Fatalf("recall.md does not document the browse flags --after/--sort")
	}
	if !strings.Contains(recall, "step") || !strings.Contains(recall, "task") {
		t.Fatalf("recall.md does not state that a step/task agent cannot browse")
	}
	if !strings.Contains(routes, "step") || !strings.Contains(routes, "task") {
		t.Fatalf("uds-routes.md does not name the step/task memory rows that stay search-only")
	}
}

// w123SeedTask writes one stored task record the running daemon's manager
// lists (the report store is the manager's own source; no spawn needed).
func w123SeedTask(t *testing.T, d *v1Daemon, tk task.Task) {
	t.Helper()
	if tk.CreatedAt.IsZero() {
		tk.CreatedAt = time.Now().UTC()
	}
	if err := task.NewStore(d.dataDir).Save(tk); err != nil {
		t.Fatalf("seed task %s: %v", tk.ID, err)
	}
}

// w123TaskRow decodes one `/task/list` row by id.
func w123TaskRow(t *testing.T, raw, id string) map[string]any {
	t.Helper()
	var doc struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("pf task list --json answer is not a {tasks:[...]} document: %v (%s)", err, raw)
	}
	for _, row := range doc.Tasks {
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("the list misses the seeded task %s: %s", id, raw)
	return nil
}

// TestW123TaskListPromptSummary: T1.
func TestW123TaskListPromptSummary(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.start()
	prompt := strings.Repeat("字", 130) + " TAIL-SECRET\nsecond line PROMPT-SECRET"
	w123SeedTask(t, d, task.Task{
		ID: "task_1234123412341234", State: task.StateCompleted,
		Spec: task.Spec{Project: v1Project, Prompt: prompt, Candidates: []string{"pi:e2e/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
		Report: "REPORT-SECRET body\nreport end",
	})

	raw := d.pf(t, "task", "list", "--json")
	row := w123TaskRow(t, raw, "task_1234123412341234")
	summary, ok := row["prompt_summary"].(string)
	if !ok {
		t.Fatalf("a list row carries no prompt_summary string: %s", raw)
	}
	want := strings.Repeat("字", 120)
	if summary != want {
		t.Fatalf("prompt_summary = %q (%d code points), want the first line cut at 120 code points %q",
			summary, len([]rune(summary)), want)
	}
	for _, leak := range []string{"TAIL-SECRET", "PROMPT-SECRET", "REPORT-SECRET", "second line"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("the list row leaks %q (only the bounded summary may leave): %s", leak, raw)
		}
	}
	if p, present := row["prompt"]; present && p != "" {
		t.Fatalf("the list row still carries the full prompt: %v", p)
	}

	show := d.pf(t, "task", "show", "task_1234123412341234", "--json")
	for _, want := range []string{"TAIL-SECRET", "PROMPT-SECRET", "REPORT-SECRET"} {
		if !strings.Contains(show, want) {
			t.Fatalf("pf task show must still answer the full prompt and report (%q missing): %s", want, show)
		}
	}
}

// TestW123TaskListEmptyPromptSummary: T2.
func TestW123TaskListEmptyPromptSummary(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.start()
	w123SeedTask(t, d, task.Task{
		ID: "task_5678567856785678", State: task.StateFailed,
		Spec: task.Spec{Project: v1Project, Prompt: " \n\t \n", Candidates: []string{"pi:e2e/m"},
			MaxDuration: time.Minute, MaxTokens: 1000},
	})

	raw := d.pf(t, "task", "list", "--json")
	row := w123TaskRow(t, raw, "task_5678567856785678")
	summary, present := row["prompt_summary"]
	if !present {
		t.Fatalf("an empty-prompt row must still carry prompt_summary (empty): %s", raw)
	}
	if summary != "" {
		t.Fatalf("an empty/whitespace prompt summary = %v, want the empty string", summary)
	}
}
