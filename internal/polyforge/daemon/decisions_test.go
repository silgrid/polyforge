package daemon

// decisions_test.go: the decision feed — every decision
// kind the retired chat watcher woke a chat for becomes a feed entry from
// the daemon's own observations; resolution, dedupe, dismiss, persistence
// and the hub reconcile.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

func testFeed(t *testing.T) *decisionFeed {
	t.Helper()
	f, err := newDecisionFeed(filepath.Join(t.TempDir(), decisionsFile))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func evAt(wi, kind, body string) protocol.EventEntry {
	e := protocol.EventEntry{WI: wi, Kind: kind, At: "2026-11-09T10:00:00Z"}
	if body != "" {
		e.Body = json.RawMessage(body)
	}
	return e
}

func newest(f *decisionFeed) protocol.Decision { return f.List().Decisions[0] }

// TestEveryDecisionKindBecomesAFeedEntry: every kind the decision table
// classifies (watcher.Kinds — a kind added there without a producer path
// fails here) reaches the feed through the daemon's own event log and its
// observer, as its producer emits it: the event of that kind, the driver's
// terminal event carrying a workflow status, the runtime's release
// (job_finished) carrying a close's reason, or — for a status only aihub
// reports (canceled) — the hub reconcile's record.
func TestEveryDecisionKindBecomesAFeedEntry(t *testing.T) {
	td := newTestDaemon(t, nil)
	log := td.obs.Events()
	bodies := map[string]map[string]any{
		"awaiting_owner":      {"step": "grill", "question_id": "q-1", "question": "free text"},
		"carrier_push_failed": {"repo": "app"},
	}
	for i, kind := range watcher.Kinds() {
		wi := fmt.Sprintf("wi_kind%02d", i)
		var err error
		switch {
		case kind == "terminal":
			err = log.Append(wi, "terminal", map[string]any{"status": "something_new"})
		case kind == "canceled":
			err = td.feed.RecordStatus(td.profile(), wi, "", kind) // the hub reconcile's record
		case kind == "wrapped" || kind == "failed" || kind == "paused":
			err = log.Append(wi, observe.EvJobFinished, map[string]any{"reason": kind})
		default:
			if _, isEvent := watcher.Classify(kind, nil); isEvent {
				body := bodies[kind]
				if body == nil {
					body = map[string]any{}
				}
				err = log.Append(wi, kind, body)
			} else {
				// a driver workflow status: the terminal event carries it
				err = log.Append(wi, "terminal", map[string]any{"status": kind})
			}
		}
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		want, ok := watcher.Classify(kind, nil)
		if !ok || kind == "terminal" {
			want, ok = watcher.StatusDecision(kind)
		}
		if kind == "terminal" {
			want, ok = watcher.Classify("terminal", json.RawMessage(`{"status":"something_new"}`))
		}
		if !ok {
			t.Fatalf("%s: the table does not classify its own kind", kind)
		}
		var got []protocol.Decision
		for _, e := range td.feed.List().Decisions {
			if e.WI == wi {
				got = append(got, e)
			}
		}
		if len(got) != 1 {
			t.Fatalf("%s: feed entries = %+v", kind, got)
		}
		e := got[0]
		if e.Class != want.Class || e.Source != kind || e.Attention != want.Attention || e.Phrase == "" ||
			e.Profile != td.profile() || e.Kind != protocol.DecisionWorkItem {
			t.Fatalf("%s: entry = %+v, want class %s attention %v", kind, e, want.Class, want.Attention)
		}
		if strings.Contains(e.Phrase, "free text") || e.Text != "" {
			t.Fatalf("%s: an event body leaked into the entry: %+v", kind, e)
		}
		if kind == "awaiting_owner" && e.QuestionID != "q-1" || kind == "carrier_push_failed" && e.Repo != "app" {
			t.Fatalf("%s: identifier missing: %+v", kind, e)
		}
	}
}

func TestNonDecisionsAndDaemonScopeEventsAreIgnored(t *testing.T) {
	f := testFeed(t)
	for _, e := range []protocol.EventEntry{evAt("wi_1", "step_started", ""), evAt("wi_1", "rewind", ""),
		evAt("", "model_unhealthy", `{}`), evAt("", "submit_dropped", `{}`)} {
		f.Ingest("default", e, nil)
	}
	if got := f.List(); len(got.Decisions) != 0 {
		t.Fatalf("feed = %+v", got)
	}
}

// TestResolution: the step's result resolves the pending question, a
// later state supersedes it, and a job start resolves everything
// announced before.
func TestResolution(t *testing.T) {
	f := testFeed(t)
	f.Ingest("default", evAt("wi_1", "awaiting_owner", `{"question_id":"q-1"}`), nil)
	// the driver re-announces the same pending question after a restart:
	// one entry, not two
	f.Ingest("default", evAt("wi_1", "awaiting_owner", `{"question_id":"q-1","resumed":true}`), nil)
	if got := f.List(); len(got.Decisions) != 1 || got.Attention != 1 {
		t.Fatalf("re-announced question duplicated: %+v", got)
	}
	f.Ingest("default", evAt("wi_1", "step_done", `{"step":"fix"}`), nil)
	if e := newest(f); !e.Resolved || f.List().Attention != 0 {
		t.Fatalf("the question of a step that delivered its result is still open: %+v", e)
	}
	// a new question of the same work item is a new entry
	f.Ingest("default", evAt("wi_1", "awaiting_owner", `{"question_id":"q-2"}`), nil)
	f.Ingest("default", evAt("wi_1", "terminal", `{"status":"failed_gate"}`), nil)
	feed := f.List()
	if len(feed.Decisions) != 3 || !feed.Decisions[1].Resolved || feed.Decisions[0].Class != "failed" || feed.Attention != 1 {
		t.Fatalf("a terminal state must supersede the pending question: %+v", feed)
	}
	// occurrences are recorded every time
	f.Ingest("default", evAt("wi_1", "submit_rejected", `{}`), nil)
	f.Ingest("default", evAt("wi_1", "submit_rejected", `{}`), nil)
	if got := f.List(); got.Attention != 3 {
		t.Fatalf("attention = %d, want failed + two rejects", got.Attention)
	}
	// the work item runs again: every earlier entry is over
	f.Ingest("default", evAt("wi_1", "job_started", `{"spawn_n":1}`), nil)
	if got := f.List(); got.Attention != 0 {
		t.Fatalf("job_started left open entries: %+v", got)
	}
	// another Cell's equal literal is another work item
	f.Ingest("alpha", evAt("wi_1", "blocked", `{}`), nil)
	f.Ingest("default", evAt("wi_1", "job_started", `{}`), nil)
	if e := newest(f); e.Profile != "alpha" || e.Resolved {
		t.Fatalf("a default-Cell start resolved an alpha entry: %+v", e)
	}
}

func TestDismiss(t *testing.T) {
	f := testFeed(t)
	f.Ingest("default", evAt("wi_1", "blocked", `{}`), nil)
	f.Ingest("default", evAt("wi_2", "submit_dropped", `{}`), nil)
	f.Ingest("default", evAt("wi_3", "candidates_exhausted", `{}`), nil)
	if n, err := f.Dismiss([]int64{2}, false); err != nil || n != 1 {
		t.Fatalf("dismiss = %d %v", n, err)
	}
	if got := f.List(); got.Attention != 2 || !got.Decisions[1].Dismissed {
		t.Fatalf("after one dismiss: %+v", got)
	}
	if n, _ := f.Dismiss([]int64{2}, false); n != 0 {
		t.Fatalf("a repeated dismiss counted %d", n)
	}
	if n, err := f.Dismiss(nil, true); err != nil || n != 2 {
		t.Fatalf("dismiss all = %d %v", n, err)
	}
	if got := f.List(); got.Attention != 0 {
		t.Fatalf("after dismiss all: %+v", got)
	}
}

func TestFeedPersistsAndIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), decisionsFile)
	f, err := newDecisionFeed(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	// a closed entry older than the age bound is pruned on the next write
	// (an open one never is: TestPruningKeepsOpenEntries)
	f.doc.Entries = append(f.doc.Entries, protocol.Decision{ID: 1, At: now.Add(-decisionFeedMaxAge - time.Hour).Format(time.RFC3339),
		Kind: protocol.DecisionWorkItem, WI: "wi_old", Class: "blocked", Source: "blocked", Attention: true, Resolved: true})
	f.doc.Next = 2
	for i := 0; i < decisionFeedMax+5; i++ {
		f.Ingest("default", protocol.EventEntry{WI: fmt.Sprintf("wi_%d", i), Kind: "daemon_restarted"}, nil)
	}
	g, err := newDecisionFeed(path)
	if err != nil {
		t.Fatal(err)
	}
	got := g.List()
	if len(got.Decisions) != decisionFeedMax {
		t.Fatalf("retained %d entries, want %d", len(got.Decisions), decisionFeedMax)
	}
	for _, e := range got.Decisions {
		if e.WI == "wi_old" {
			t.Fatal("an entry past the age bound survived")
		}
	}
	// ids never repeat across a reopen, even after pruning
	g.Ingest("default", evAt("wi_new", "submit_dropped", ""), nil)
	if e := newest(g); e.ID != int64(decisionFeedMax+5+2) {
		t.Fatalf("next id = %d", e.ID)
	}
}

func TestTaskEntries(t *testing.T) {
	f := testFeed(t)
	for _, st := range []task.State{task.StateCompleted, task.StateFailed, task.StateCapped, task.StateInterrupted} {
		if err := f.RecordTask("default", task.Task{ID: "t_" + string(st), State: st, Report: "the report"}); err != nil {
			t.Fatal(err)
		}
	}
	feed := f.List()
	if len(feed.Decisions) != 4 || feed.Attention != 3 {
		t.Fatalf("feed = %+v", feed)
	}
	done := feed.Decisions[3]
	if done.Kind != protocol.DecisionTask || done.TaskID != "t_completed" || done.Attention ||
		!strings.Contains(done.Text, "the report") || !strings.Contains(done.Text, "pf task show t_completed") {
		t.Fatalf("completed task entry = %+v", done)
	}
}

// TestReconcileReadsTheHubForParkedWorkItems: a blocked work item with no
// local job is canceled at the hub (`pf cancel` never passes the daemon):
// the reconcile records canceled; a paused one that runs again resolves.
func TestReconcileReadsTheHubForParkedWorkItems(t *testing.T) {
	td := newTestDaemon(t, nil)
	d := td.Daemon
	f := d.feed
	_ = f.RecordStatus(d.profile(), "wi_b", "proj#2", api.StatusBlocked)
	_ = f.RecordStatus(d.profile(), "wi_p", "proj#3", api.StatusPaused)
	_ = f.RecordStatus(d.profile(), "wi_c", "proj#4", api.StatusCanceled)
	if got := f.trackedFor(d.profile()); strings.Join(got, ",") != "wi_b,wi_p" {
		t.Fatalf("tracked = %v", got)
	}
	td.hub.mu.Lock()
	td.hub.status["wi_b"] = api.StatusCanceled
	td.hub.status["wi_p"] = api.StatusQueued
	td.hub.status["wi_c"] = api.StatusQueued
	td.hub.mu.Unlock()
	d.reconcileDecisions()
	feed := f.List()
	if e := feed.Decisions[0]; e.WI != "wi_b" || e.Class != "canceled" {
		t.Fatalf("newest = %+v", e)
	}
	for _, e := range feed.Decisions {
		if (e.WI == "wi_b" && e.Class == "blocked" || e.WI == "wi_p") && !e.Resolved {
			t.Fatalf("not resolved: %+v", e)
		}
	}
	if got := f.trackedFor(d.profile()); len(got) != 0 {
		t.Fatalf("still tracked: %v", got)
	}
	if td.hub.gets["wi_c"] != 0 {
		t.Fatal("a final work item must not be read again")
	}
}

// TestDecisionRoutesServeTheFeed: an event appended to the daemon's log
// reaches GET /decisions through the observer; POST /decisions/dismiss
// marks it.
func TestDecisionRoutesServeTheFeed(t *testing.T) {
	td := newTestDaemon(t, nil)
	if err := td.obs.Events().Append("wi_r", "submit_dropped", map[string]any{"reason": "x"}); err != nil {
		t.Fatal(err)
	}
	code, raw := stepGet(t, td.sock, "/decisions")
	var feed protocol.DecisionFeed
	if code != 200 || json.Unmarshal([]byte(raw), &feed) != nil || feed.Attention != 1 || len(feed.Decisions) != 1 {
		t.Fatalf("GET /decisions = %d %s", code, raw)
	}
	id := feed.Decisions[0].ID
	if code, raw := stepDo(t, td.sock, "POST", "/decisions/dismiss", `{}`); code != 400 {
		t.Fatalf("an empty dismiss must be refused: %d %s", code, raw)
	}
	if code, raw := stepDo(t, td.sock, "POST", "/decisions/dismiss", `{"ids":[-1]}`); code != 400 {
		t.Fatalf("a bad id must be refused: %d %s", code, raw)
	}
	code, raw = stepDo(t, td.sock, "POST", "/decisions/dismiss", fmt.Sprintf(`{"ids":[%d]}`, id))
	if code != 200 || !strings.Contains(raw, `"dismissed":1`) {
		t.Fatalf("dismiss = %d %s", code, raw)
	}
	if _, raw := stepGet(t, td.sock, "/decisions"); !strings.Contains(raw, `"attention":0`) {
		t.Fatalf("after dismiss: %s", raw)
	}
}

// TestMachineRoleRoute (AC5): the interactive role request and its
// --force takeover no longer need `pf chat init`.
func TestMachineRoleRoute(t *testing.T) {
	td := newTestDaemon(t, nil)
	if code, raw := stepDo(t, td.sock, "POST", "/machine/role", `{"role":"interactive","force":true}`); code != 200 {
		t.Fatalf("role = %d %s", code, raw)
	}
	role, _, force := td.faceHeartbeat()
	if role != api.MachineRoleInteractive || !force {
		t.Fatalf("heartbeat projection = %s force=%v", role, force)
	}
	if _, _, force := td.faceHeartbeat(); force {
		t.Fatal("force rides one heartbeat only")
	}
	for _, body := range []string{`{"role":"boss"}`, `{"role":"runner","force":true}`} {
		if code, raw := stepDo(t, td.sock, "POST", "/machine/role", body); code != 400 {
			t.Fatalf("%s = %d %s", body, code, raw)
		}
	}
}

// ---- review: the actual end, pruning, synchronous tasks, a corrupt feed ----

// TestJobFinishedRecordsTheActualEnd (M1): the runtime's release names the
// reason a close really ended with — a wrap whose push failed or whose
// hand-off record is unreadable ends blocked, a bounded fail ends blocked —
// and a supersede (another holder, a hub cancel of a held job) resolves what
// stood open. A reason another source already recorded is not recorded twice.
func TestJobFinishedRecordsTheActualEnd(t *testing.T) {
	f := testFeed(t)
	f.Ingest("default", evAt("wi_wrap", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	if e := newest(f); e.WI != "wi_wrap" || e.Class != "blocked" || !e.Open() {
		t.Fatalf("a degraded wrap = %+v", e)
	}
	if got := f.trackedFor("default"); strings.Join(got, ",") != "wi_wrap" {
		t.Fatalf("the blocked end must be tracked: %v", got)
	}
	// a terminal completed followed by the release (wrapped): one entry
	f.Ingest("default", evAt("wi_done", "terminal", `{"status":"completed"}`), nil)
	f.Ingest("default", evAt("wi_done", observe.EvJobFinished, `{"reason":"wrapped"}`), nil)
	// a terminal failed_gate followed by the release (failed): one entry
	f.Ingest("default", evAt("wi_fail", "terminal", `{"status":"failed_gate"}`), nil)
	f.Ingest("default", evAt("wi_fail", observe.EvJobFinished, `{"reason":"failed"}`), nil)
	// the driver's blocked event followed by the bounded-fail release
	f.Ingest("default", evAt("wi_bf", "blocked", `{}`), nil)
	f.Ingest("default", evAt("wi_bf", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	count := map[string]int{}
	for _, e := range f.List().Decisions {
		count[e.WI]++
	}
	if count["wi_done"] != 1 || count["wi_fail"] != 1 || count["wi_bf"] != 1 {
		t.Fatalf("a release already recorded must not repeat: %v", count)
	}
	// a bounded-fail block with no driver event before it still appears
	f.Ingest("default", evAt("wi_bf2", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	if e := newest(f); e.WI != "wi_bf2" || e.Class != "blocked" {
		t.Fatalf("bounded fail = %+v", e)
	}
	// a supersede (a hub cancel of a held job) resolves the open question
	f.Ingest("default", evAt("wi_q", "awaiting_owner", `{"question_id":"q-1"}`), nil)
	f.Ingest("default", evAt("wi_q", observe.EvJobFinished, `{"reason":"superseded"}`), nil)
	for _, e := range f.List().Decisions {
		if e.WI == "wi_q" && e.Open() {
			t.Fatalf("superseded left an open entry: %+v", e)
		}
	}
}

// TestWrapThatEndsBlockedReachesTheFeedAsBlocked (M1, the daemon end to end):
// a wrap whose hand-off record cannot be read closes blocked; the feed shows
// blocked (tracked by the hub reconcile), never a completed.
func TestWrapThatEndsBlockedReachesTheFeedAsBlocked(t *testing.T) {
	td := newTestDaemon(t, nil)
	wi := "wi_m1wrap"
	td.submitClaim(t, wi)
	ji, ok := td.rt.Info(wi)
	if !ok || ji.CapsuleDir == "" {
		t.Fatalf("no capsule: %+v", ji)
	}
	// the hand-off record is the call ledger (workflow contract 4): one
	// that cannot be read (here a directory in its place)
	if err := os.MkdirAll(filepath.Join(ji.CapsuleDir, capsule.StepCallsJSONL), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := td.client.Wrap(wi, "wrap"); err != nil {
		t.Fatalf("wrap: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var classes []string
	for time.Now().Before(deadline) {
		classes = classes[:0]
		for _, e := range td.feed.List().Decisions {
			if e.WI == wi {
				classes = append(classes, e.Class)
			}
		}
		if len(classes) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Join(classes, ",") != "blocked" {
		t.Fatalf("the feed recorded %v for a wrap that ended blocked", classes)
	}
	if got := td.feed.trackedFor(td.profile()); strings.Join(got, ",") != wi {
		t.Fatalf("tracked = %v", got)
	}
}

// TestPruningKeepsOpenEntries (minor 1): the bound drops resolved and
// dismissed entries first, and never an open one, whatever its age.
func TestPruningKeepsOpenEntries(t *testing.T) {
	f := testFeed(t)
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	old := now.Add(-decisionFeedMaxAge - time.Hour).Format(time.RFC3339)
	f.Ingest("default", protocol.EventEntry{WI: "wi_open", Kind: "awaiting_owner", At: old, Body: json.RawMessage(`{"question_id":"q-1"}`)}, nil)
	f.Ingest("default", protocol.EventEntry{WI: "wi_oldinfo", Kind: "daemon_restarted", At: old}, nil)
	for i := 0; i < decisionFeedMax+20; i++ {
		f.Ingest("default", protocol.EventEntry{WI: fmt.Sprintf("wi_r%d", i), Kind: "daemon_restarted"}, nil)
	}
	got := f.List()
	if len(got.Decisions) > decisionFeedMax {
		t.Fatalf("retained %d > %d", len(got.Decisions), decisionFeedMax)
	}
	found := false
	for _, e := range got.Decisions {
		if e.WI == "wi_open" && e.Open() {
			found = true
		}
		if e.WI == "wi_oldinfo" {
			t.Fatal("an old informational entry survived the age cut")
		}
	}
	if !found || got.Attention != 1 {
		t.Fatalf("the open needs_input was pruned (attention %d)", got.Attention)
	}
}

// TestSynchronousTasksAreNoDecision (minor 2): a task the daemon ran for a
// caller that waits on it (message analyze / prompt) is that caller's
// answer, not a background-task decision.
func TestSynchronousTasksAreNoDecision(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.recordTask(task.Task{ID: "task_00000000000000a1", State: task.StateCompleted, Spec: task.Spec{Synchronous: true}})
	td.recordTask(task.Task{ID: "task_00000000000000a2", State: task.StateCompleted})
	got := td.feed.List().Decisions
	if len(got) != 1 || got[0].TaskID != "task_00000000000000a2" {
		t.Fatalf("feed = %+v", got)
	}
}

// TestCorruptFeedIsQuarantined (minor 3): an unreadable decisions.json never
// stops the daemon — it is set aside as decisions.json.corrupt-<ts> and the
// feed starts empty.
func TestCorruptFeedIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, decisionsFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := newDecisionFeed(path)
	if err != nil || len(f.List().Decisions) != 0 {
		t.Fatalf("a corrupt feed must start empty: %v", err)
	}
	aside, _ := filepath.Glob(path + ".corrupt-*")
	if len(aside) != 1 {
		t.Fatalf("the corrupt file was not set aside: %v", aside)
	}
	if raw, _ := os.ReadFile(aside[0]); string(raw) != "{not json" {
		t.Fatalf("the set-aside copy = %q", raw)
	}
	f.Ingest("default", evAt("wi_1", "submit_dropped", ""), nil)
	if _, err := newDecisionFeed(path); err != nil {
		t.Fatalf("the fresh feed is readable again: %v", err)
	}
}

// TestMachineRoleIfUnsetKeepsAnExplicitChoice (review minor 5): the
// session-start adapters ask `pf ops role interactive --if-unset` on every
// start, compact and clear; that never undoes an operator's explicit
// `pf ops role runner` (nor an explicit interactive), and still moves a
// daemon nobody chose a role for.
func TestMachineRoleIfUnsetKeepsAnExplicitChoice(t *testing.T) {
	td := newTestDaemon(t, nil)
	if code, raw := stepDo(t, td.sock, "POST", "/machine/role", `{"role":"interactive","if_unset":true}`); code != 200 || !strings.Contains(raw, `"applied":true`) {
		t.Fatalf("an unset role takes the request: %d %s", code, raw)
	}
	if role, _, _ := td.faceHeartbeat(); role != api.MachineRoleInteractive {
		t.Fatalf("role = %s", role)
	}
	if code, raw := stepDo(t, td.sock, "POST", "/machine/role", `{"role":"runner"}`); code != 200 {
		t.Fatalf("explicit runner: %d %s", code, raw)
	}
	if code, raw := stepDo(t, td.sock, "POST", "/machine/role", `{"role":"interactive","if_unset":true}`); code != 200 || !strings.Contains(raw, `"applied":false`) {
		t.Fatalf("an explicit choice stands: %d %s", code, raw)
	}
	if role, _, _ := td.faceHeartbeat(); role != api.MachineRoleRunner {
		t.Fatalf("the adapter undid the operator's runner: %s", role)
	}
	if code, _ := stepDo(t, td.sock, "POST", "/machine/role", `{"role":"interactive","if_unset":true,"force":true}`); code != 400 {
		t.Fatal("--if-unset never forces")
	}
}

// TestExplicitRoleSurvivesARestart (PR #860 review 2, minor 1): the
// operator's explicit `pf ops role runner` is persisted in the data dir, so
// after a daemon restart (an upgrade, a hook-triggered `pf daemon ensure`)
// the adapters' `--if-unset interactive` still leaves it alone.
func TestExplicitRoleSurvivesARestart(t *testing.T) {
	var data string
	td1 := newTestDaemon(t, func(c *Config) { data = c.DataDir })
	if code, raw := stepDo(t, td1.sock, "POST", "/machine/role", `{"role":"runner"}`); code != 200 {
		t.Fatalf("explicit runner: %d %s", code, raw)
	}
	shutdownAndWait(t, td1)
	td2 := newTestDaemon(t, func(c *Config) {
		c.DataDir = data
		c.SocketPath = filepath.Join(filepath.Dir(data), "s2.sock")
	})
	if code, raw := stepDo(t, td2.sock, "POST", "/machine/role", `{"role":"interactive","if_unset":true}`); code != 200 || !strings.Contains(raw, `"applied":false`) {
		t.Fatalf("after a restart the explicit choice must stand: %d %s", code, raw)
	}
	if role, _, _ := td2.faceHeartbeat(); role != api.MachineRoleRunner {
		t.Fatalf("role after restart = %s", role)
	}
}

// TestReleaseSupersedesTheEarlierEnd (PR #860 review 2, minors 2–4): the
// runtime's release is the close's final word — a terminal completed
// recorded moments earlier is superseded by a blocked release; a supersede
// records an informational superseded entry; a hub cancel or block learned
// at restart (notOurs) is recorded; a local seal the hub never confirmed is
// no claim about the work item's state.
func TestReleaseSupersedesTheEarlierEnd(t *testing.T) {
	f := testFeed(t)
	f.Ingest("default", evAt("wi_w", "terminal", `{"status":"completed"}`), nil)
	f.Ingest("default", evAt("wi_w", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	var open, closed []string
	for _, e := range f.List().Decisions {
		if e.WI != "wi_w" {
			continue
		}
		if e.Resolved {
			closed = append(closed, e.Class)
		} else {
			open = append(open, e.Class)
		}
	}
	if strings.Join(open, ",") != "blocked" || strings.Join(closed, ",") != "completed" {
		t.Fatalf("open %v, superseded %v", open, closed)
	}
	// a supersede: the open question resolves, an informational entry says so
	f.Ingest("default", evAt("wi_s", "awaiting_owner", `{"question_id":"q-1"}`), nil)
	f.Ingest("default", evAt("wi_s", observe.EvJobFinished, `{"reason":"superseded"}`), nil)
	if e := newest(f); e.WI != "wi_s" || e.Class != "superseded" || e.Attention || e.Open() {
		t.Fatalf("superseded = %+v", e)
	}
	// notOurs at restart: the hub says canceled / blocked
	f.Ingest("default", evAt("wi_c", observe.EvJobFinished, `{"reason":"canceled"}`), nil)
	if e := newest(f); e.WI != "wi_c" || e.Class != "canceled" {
		t.Fatalf("hub canceled = %+v", e)
	}
	f.Ingest("default", evAt("wi_b", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	if e := newest(f); e.WI != "wi_b" || e.Class != "blocked" || !e.Open() {
		t.Fatalf("hub blocked = %+v", e)
	}
	// a re-queued work item: its open entries resolve, nothing is recorded
	f.Ingest("default", evAt("wi_b", observe.EvJobFinished, `{"reason":"queued"}`), nil)
	if e := newest(f); e.WI != "wi_b" || !e.Resolved {
		t.Fatalf("re-queued = %+v", e)
	}
	// an unconfirmed local seal records superseded, not the requested end
	f.Ingest("default", evAt("wi_l", observe.EvJobFinished, `{"reason":"wrapped","unconfirmed":true}`), nil)
	if e := newest(f); e.WI != "wi_l" || e.Class != "superseded" {
		t.Fatalf("unconfirmed local seal = %+v", e)
	}
}

// TestUnconfirmedEndNeverHidesABlock (PR #860 review 3): a local seal the
// hub never confirmed (the status unknown) keeps an open blocked entry
// open and tracked — the one-minute aihub re-read corrects it — and an
// unconfirmed blocked or paused end is recorded as such (tracked); a
// confirmed blocked release from aihub's status is blocked too.
func TestUnconfirmedEndNeverHidesABlock(t *testing.T) {
	f := testFeed(t)
	// an open block, then a local seal of a wrap the hub never confirmed
	f.Ingest("default", evAt("wi_a", "blocked", `{}`), nil)
	f.Ingest("default", evAt("wi_a", observe.EvJobFinished, `{"reason":"wrapped","unconfirmed":true}`), nil)
	blockedOpen := false
	for _, e := range f.List().Decisions {
		if e.WI == "wi_a" && e.Class == "blocked" && e.Open() {
			blockedOpen = true
		}
	}
	if !blockedOpen {
		t.Fatalf("an unconfirmed end resolved the open block: %+v", f.List().Decisions)
	}
	// an unconfirmed blocked end (STALE_SESSION, status unknown) is blocked
	f.Ingest("default", evAt("wi_b", observe.EvJobFinished, `{"reason":"blocked","unconfirmed":true}`), nil)
	if e := newest(f); e.WI != "wi_b" || e.Class != "blocked" || !e.Open() {
		t.Fatalf("unconfirmed blocked = %+v", e)
	}
	f.Ingest("default", evAt("wi_p", observe.EvJobFinished, `{"reason":"paused","unconfirmed":true}`), nil)
	if e := newest(f); e.WI != "wi_p" || e.Class != "paused" {
		t.Fatalf("unconfirmed paused = %+v", e)
	}
	// the crash-mid-seal case with aihub saying blocked: the release is confirmed blocked
	f.Ingest("default", evAt("wi_c", "awaiting_owner", `{"question_id":"q"}`), nil)
	f.Ingest("default", evAt("wi_c", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	if e := newest(f); e.WI != "wi_c" || e.Class != "blocked" || !e.Open() {
		t.Fatalf("confirmed blocked = %+v", e)
	}
	got := strings.Join(f.trackedFor("default"), ",")
	if got != "wi_a,wi_b,wi_c,wi_p" {
		t.Fatalf("tracked = %s (every held block or pause is re-read at aihub)", got)
	}
}

// TestConcurrentRoleMovesKeepFileAndMemoryEqual (review 3 nit): racing
// explicit moves leave the persisted choice equal to the face's.
func TestConcurrentRoleMovesKeepFileAndMemoryEqual(t *testing.T) {
	td := newTestDaemon(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		role := api.MachineRoleRunner
		if i%2 == 0 {
			role = api.MachineRoleInteractive
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = td.requestDesiredRole(role, false, false) }()
	}
	wg.Wait()
	td.mfaceMu.Lock()
	mem := td.face.desiredRole
	td.mfaceMu.Unlock()
	if file := readChosenRole(td.cfg.DataDir); file != mem {
		t.Fatalf("persisted %q, memory %q", file, mem)
	}
}

// questionEntry answers the feed entry of the work item's question (the
// ask's stamp is its QuestionID); fails when none stands.
func questionEntry(t *testing.T, f *decisionFeed, profile, wi, qid string) protocol.Decision {
	t.Helper()
	for _, e := range f.List().Decisions {
		if e.Kind == protocol.DecisionWorkItem && e.Profile == profile && e.WI == wi && e.Class == "needs_input" && e.QuestionID == qid {
			return e
		}
	}
	t.Fatalf("no needs_input entry of %s/%s for question %s: %+v", profile, wi, qid, f.List())
	return protocol.Decision{}
}

// ownerMessage is the owner_message event the relay emits for one
// asks.jsonl line (driver.OwnerMessageEvent's body).
func ownerMessage(wi, kind, askAt, at string) protocol.EventEntry {
	body := map[string]any{"at": at, "step": "build", "spawn_n": 1, "kind": kind, "by": "owner", "text": "use the second option"}
	if askAt != "" {
		body["ask_at"] = askAt
	}
	raw, _ := json.Marshal(body)
	return protocol.EventEntry{WI: wi, Kind: "owner_message", At: at, Body: raw}
}

// TestOwnerReplyMarksTheQuestionReplied (AC1): the owner's
// reply naming the question's ask marks its entry replied — no longer
// needing a human, still unresolved, persisted — the step's hand-over
// resolves it, and a later question is a new open entry.
func TestOwnerReplyMarksTheQuestionReplied(t *testing.T) {
	const (
		ask   = "2026-11-12T02:10:03.123456789Z"
		reply = "2026-11-12T02:14:55.987654321Z"
		ask2  = "2026-11-12T02:31:40.5Z"
	)
	path := filepath.Join(t.TempDir(), decisionsFile)
	f, err := newDecisionFeed(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Ingest("default", evAt("wi_r", "awaiting_owner", `{"step":"build","question_id":"`+ask+`"}`), nil)
	if e := questionEntry(t, f, "default", "wi_r", ask); !e.Open() || f.List().Attention != 1 {
		t.Fatalf("the question is not open: %+v", f.List())
	}
	f.Ingest("default", ownerMessage("wi_r", "reply", ask, reply), nil)
	check := func(g *decisionFeed, what string) {
		t.Helper()
		e := questionEntry(t, g, "default", "wi_r", ask)
		if !e.Replied || e.RepliedAt != reply || e.Attention || e.Resolved {
			t.Fatalf("%s: the replied question is not replied/unresolved without attention: %+v", what, e)
		}
		if n := g.List().Attention; n != 0 {
			t.Fatalf("%s: attention = %d, want 0 after the reply", what, n)
		}
	}
	check(f, "live feed")
	g, err := newDecisionFeed(path)
	if err != nil {
		t.Fatal(err)
	}
	check(g, "reloaded feed")

	f.Ingest("default", evAt("wi_r", "step_done", `{"step":"build"}`), nil)
	if e := questionEntry(t, f, "default", "wi_r", ask); !e.Resolved || !e.Replied {
		t.Fatalf("the step's hand-over did not resolve the replied question: %+v", e)
	}
	f.Ingest("default", evAt("wi_r", "awaiting_owner", `{"step":"build","question_id":"`+ask2+`"}`), nil)
	if e := questionEntry(t, f, "default", "wi_r", ask2); !e.Open() || e.Replied || f.List().Attention != 1 {
		t.Fatalf("a later question is not a new open entry: %+v", f.List())
	}
}

// TestOnlyTheAnsweredAskIsMarkedReplied (AC2): an
// interjection, a reply naming an earlier ask, and a reply of another
// work item or Cell leave the open question as it is; only the reply
// naming its own ask marks it replied.
func TestOnlyTheAnsweredAskIsMarkedReplied(t *testing.T) {
	const (
		ask1 = "2026-11-12T01:00:00.111111111Z"
		ask2 = "2026-11-12T01:20:00.222222222Z"
	)
	f := testFeed(t)
	f.Ingest("default", evAt("wi_a", "awaiting_owner", `{"step":"plan","question_id":"`+ask1+`"}`), nil)
	f.Ingest("default", evAt("wi_a", "step_done", `{"step":"plan"}`), nil)
	f.Ingest("default", evAt("wi_a", "awaiting_owner", `{"step":"build","question_id":"`+ask2+`"}`), nil)
	first := questionEntry(t, f, "default", "wi_a", ask1)
	if !first.Resolved {
		t.Fatalf("setup: the first question is not resolved: %+v", first)
	}
	stillOpen := func(what string) {
		t.Helper()
		e := questionEntry(t, f, "default", "wi_a", ask2)
		if !e.Attention || e.Replied || e.RepliedAt != "" || e.Resolved || f.List().Attention != 1 {
			t.Fatalf("%s changed the open question: %+v", what, e)
		}
		if got := questionEntry(t, f, "default", "wi_a", ask1); got != first {
			t.Fatalf("%s changed the earlier question: %+v, was %+v", what, got, first)
		}
	}
	f.Ingest("default", ownerMessage("wi_a", "message", "", "2026-11-12T01:25:00.000000001Z"), nil)
	stillOpen("an interjection")
	f.Ingest("default", ownerMessage("wi_a", "reply", ask1, "2026-11-12T01:26:00.000000002Z"), nil)
	stillOpen("a reply naming the earlier ask")
	f.Ingest("default", ownerMessage("wi_b", "reply", ask2, "2026-11-12T01:27:00.000000003Z"), nil)
	stillOpen("a reply of another work item")
	f.Ingest("alpha", ownerMessage("wi_a", "reply", ask2, "2026-11-12T01:28:00.000000004Z"), nil)
	stillOpen("a reply of another Cell")

	const reply = "2026-11-12T01:29:00.000000005Z"
	f.Ingest("default", ownerMessage("wi_a", "reply", ask2, reply), nil)
	if e := questionEntry(t, f, "default", "wi_a", ask2); !e.Replied || e.RepliedAt != reply || e.Attention || e.Resolved {
		t.Fatalf("the reply naming the open ask did not mark it replied: %+v", e)
	}
	if got := questionEntry(t, f, "default", "wi_a", ask1); got != first {
		t.Fatalf("the reply to the second ask changed the first question: %+v", got)
	}
}

// TestFinalEndResolvesCarrierPushFailed: a work item that
// ended for good — wrapped, failed, canceled, through the runtime's release
// or a hub read — will never be resumed, so its carrier_push_failed entry
// ("a resume may lose its commits") resolves; a block or pause keeps it.
func TestFinalEndResolvesCarrierPushFailed(t *testing.T) {
	f := testFeed(t)
	carrier := func(wi string) protocol.Decision {
		t.Helper()
		for _, e := range f.List().Decisions {
			if e.WI == wi && e.Class == "carrier_push_failed" {
				return e
			}
		}
		t.Fatalf("%s: no carrier_push_failed entry", wi)
		return protocol.Decision{}
	}
	// the push failed, then the work item wrapped
	f.Ingest("default", evAt("wi_w", "carrier_push_failed", `{"repo":"app"}`), nil)
	f.Ingest("default", evAt("wi_w", observe.EvJobFinished, `{"reason":"wrapped"}`), nil)
	if e := carrier("wi_w"); !e.Resolved {
		t.Fatalf("wrapped: carrier entry still open: %+v", e)
	}
	if n := f.List().Attention; n != 0 {
		t.Fatalf("wrapped: attention = %d, want 0", n)
	}
	// the driver's terminal event came first, the push failed at the close
	f.Ingest("default", evAt("wi_t", "terminal", `{"status":"completed"}`), nil)
	f.Ingest("default", evAt("wi_t", "carrier_push_failed", `{"repo":"app"}`), nil)
	f.Ingest("default", evAt("wi_t", observe.EvJobFinished, `{"reason":"wrapped"}`), nil)
	if e := carrier("wi_t"); !e.Resolved {
		t.Fatalf("terminal then wrapped: carrier entry still open: %+v", e)
	}
	// a block keeps it (a resume is still possible); a later cancel the hub
	// read observed resolves it
	f.Ingest("default", evAt("wi_b", "carrier_push_failed", `{"repo":"app"}`), nil)
	f.Ingest("default", evAt("wi_b", observe.EvJobFinished, `{"reason":"blocked"}`), nil)
	if e := carrier("wi_b"); e.Resolved {
		t.Fatalf("blocked: carrier entry resolved: %+v", e)
	}
	if err := f.RecordStatus("default", "wi_b", "", api.StatusCanceled); err != nil {
		t.Fatal(err)
	}
	if e := carrier("wi_b"); !e.Resolved {
		t.Fatalf("canceled: carrier entry still open: %+v", e)
	}
	// a failed end
	f.Ingest("default", evAt("wi_f", "carrier_push_failed", `{"repo":"app"}`), nil)
	f.Ingest("default", evAt("wi_f", observe.EvJobFinished, `{"reason":"failed"}`), nil)
	if e := carrier("wi_f"); !e.Resolved {
		t.Fatalf("failed: carrier entry still open: %+v", e)
	}
}
