package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// watchDaemon is a fake daemon for the watch lane: one WI whose state the
// test scripts, the decision feed, and the recorded dismiss bodies. query
// records the last watch route's raw query string (the --demand flag's
// wire shape); paths records every request with its profile header.
type watchDaemon struct {
	mu       sync.Mutex
	snap     protocol.WatchSnapshot
	feed     protocol.DecisionFeed
	dismiss  []protocol.DecisionDismiss
	sock     string
	query    string
	requests []string
}

// queryOf answers the last watch route's raw query string.
func (w *watchDaemon) queryOf() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.query
}

func (w *watchDaemon) seen() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requests...)
}

func newWatchDaemon(t *testing.T, snap protocol.WatchSnapshot) *watchDaemon {
	t.Helper()
	w := &watchDaemon{snap: snap}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jobs/{wi}/watch", func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.query = r.URL.RawQuery
		_ = json.NewEncoder(rw).Encode(w.snap)
	})
	mux.HandleFunc("GET /decisions", func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		_ = json.NewEncoder(rw).Encode(w.feed)
	})
	mux.HandleFunc("POST /decisions/dismiss", func(rw http.ResponseWriter, r *http.Request) {
		var b protocol.DecisionDismiss
		_ = json.NewDecoder(r.Body).Decode(&b)
		w.mu.Lock()
		w.dismiss = append(w.dismiss, b)
		w.mu.Unlock()
		n := len(b.IDs)
		if b.All {
			n = 2
		}
		_ = json.NewEncoder(rw).Encode(protocol.DecisionDismissAnswer{Dismissed: n})
	})
	w.sock = newFakeDaemon(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.requests = append(w.requests, r.Method+" "+r.URL.Path+" profile="+r.Header.Get(protocol.ProfileHeader))
		w.mu.Unlock()
		mux.ServeHTTP(rw, r)
	}))
	return w
}

func watchEnv(w *watchDaemon, t *testing.T, extra map[string]string) map[string]string {
	env := map[string]string{"PF_DAEMON_SOCKET": w.sock, "XDG_RUNTIME_DIR": t.TempDir()}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestHelpGroupsAreExplicitMetadata(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)
	help := out.String()
	for _, name := range []string{"watch ", "submit", "doctor", "daemon", "users"} {
		if !strings.Contains(help, "  "+name) {
			t.Errorf("help lacks %q", name)
		}
	}
	if strings.Contains(help, "watch-probe") {
		t.Error("watch-probe is gone")
	}
	for _, gone := range []string{"migrate-memories", "migrate-work-items"} {
		if strings.Contains(help, gone) {
			t.Errorf("%s is deleted and must not be listed", gone)
		}
		if lookupCommand(gone) != nil {
			t.Errorf("%s is deleted and must not dispatch", gone)
		}
	}
	if strings.Contains(help, "DEPRECATED") {
		t.Error("no verb is a deprecated alias in v2")
	}
	for _, gone := range []string{"observe", "takeover", "verify-session", "unblock", "batch-confirm", "chat"} {
		if lookupCommand(gone) != nil {
			t.Errorf("%s is removed and must not dispatch", gone)
		}
		if code, _, stderr := runPf(t, nil, gone, "wi_1"); code != 2 || !strings.Contains(stderr, "unknown command") {
			t.Errorf("%s must be unknown: %d %q", gone, code, stderr)
		}
	}
	// the watch verb sits in the execution-plane group, between its header and the next
	exec := help[strings.Index(help, "Execution-plane"):strings.Index(help, "Management face")]
	if !strings.Contains(exec, "  watch ") {
		t.Errorf("watch must be listed under the execution plane:\n%s", exec)
	}
	for _, c := range commands {
		if c.Group == "" {
			t.Errorf("%s has no help group", c.Name)
		}
	}
	if code, _, stderr := runPf(t, nil, "watch-probe", "wi_1"); code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Errorf("watch-probe must be unknown: %d %q", code, stderr)
	}
}

// The --demand flag maps to the demand=1 query parameter of the watch
// route (polyforge#224): the human-side --once --demand poll carries the
// marker; an unflagged --once fetch (the resident watcher's polls use
// the same client) carries none.
func TestWatchOnceDemandFlagMapsToQueryParam(t *testing.T) {
	snap := protocol.WatchSnapshot{WorkItem: "wi_1", Known: true, Events: []protocol.EventEntry{}}
	w := newWatchDaemon(t, snap)
	if code, _, stderr := runPf(t, watchEnv(w, t, nil), "watch", "wi_1", "--once", "--demand", "--json"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if q := w.queryOf(); !strings.Contains(q, "after=0") || !strings.Contains(q, "demand=1") {
		t.Fatalf("the --demand flag did not map to the demand=1 query param: %q", q)
	}
	if code, _, stderr := runPf(t, watchEnv(w, t, nil), "watch", "wi_1", "--once", "--json"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if q := w.queryOf(); strings.Contains(q, "demand") {
		t.Fatalf("an unflagged --once fetch carried the demand param: %q", q)
	}
}

// --demand without --once is a usage error: an authorizing flag is
// never silently dropped.
func TestWatchDemandWithoutOnceIsUsageError(t *testing.T) {
	w := newWatchDaemon(t, protocol.WatchSnapshot{WorkItem: "wi_1", Events: []protocol.EventEntry{}})
	code, _, stderr := runPf(t, watchEnv(w, t, nil), "watch", "wi_1", "--demand")
	if code != 1 || !strings.Contains(stderr, "--demand") || !strings.Contains(stderr, "--once") {
		t.Fatalf("code=%d stderr=%q — --demand without --once must be a usage error", code, stderr)
	}
	code, _, stderr = runPf(t, watchEnv(w, t, nil), "watch", "--demand")
	if code != 1 || !strings.Contains(stderr, "--once") {
		t.Fatalf("code=%d stderr=%q — --demand with no path at all must be the same usage error", code, stderr)
	}
}

// TestRetiredWatchFacesAreGone ( AC2): the resident chat
// watcher, its subscription verbs and its statusline face are no longer
// there — a usage error, never a silent no-op.
func TestRetiredWatchFacesAreGone(t *testing.T) {
	w := newWatchDaemon(t, protocol.WatchSnapshot{WorkItem: "wi_1"})
	env := watchEnv(w, t, map[string]string{"PF_CHAT_ID": "chat_x"})
	for _, args := range [][]string{
		{"watch", "--chat", "chat_x"},
		{"watch", "--wait-decision"},
		{"watch", "--statusline", "--harness-session", "-"},
		{"watch"},
		{"watch", "adopt", "wi_1"},
		{"watch", "subscribe", "wi_1"},
		{"watch", "unsubscribe", "wi_1"},
		{"watch", "list"},
		{"watch", "ack"},
		{"chat", "init", "--harness", "claude", "--harness-session", "s"},
	} {
		if code, out, _ := runPf(t, env, args...); code == 0 {
			t.Errorf("%v still answers: %q", args, out)
		}
	}
	for _, r := range w.seen() {
		if strings.Contains(r, "/chat") || strings.Contains(r, "/jobs/") {
			t.Fatalf("a retired face reached the daemon: %s", r)
		}
	}
	// the retired subcommand words are named, not read as a work item
	for _, word := range []string{"list", "subscribe", "unsubscribe", "adopt", "ack"} {
		code, _, stderr := runPf(t, env, "watch", word, "--once")
		if code == 0 || !strings.Contains(stderr, "retired") || !strings.Contains(stderr, "pf watch decisions") {
			t.Fatalf("watch %s: %d %q", word, code, stderr)
		}
	}
}

// TestWatchDecisionsAndDismiss: `pf watch decisions` prints the feed
// (Host scope: no profile header) and `--json` re-emits it verbatim;
// `pf watch dismiss` sends the ids or --all and refuses a bad id before
// any request.
func TestWatchDecisionsAndDismiss(t *testing.T) {
	w := newWatchDaemon(t, protocol.WatchSnapshot{})
	w.feed = protocol.DecisionFeed{Attention: 1, Decisions: []protocol.Decision{
		{ID: 3, At: "2026-11-09T10:02:00Z", Kind: protocol.DecisionTask, Profile: "default", TaskID: "t_1",
			Class: "completed", Source: "task", Phrase: "the background task completed", Text: "task t_1 completed: done — see `pf task show t_1`"},
		{ID: 2, At: "2026-11-09T10:01:00Z", Kind: protocol.DecisionWorkItem, Profile: "alpha", WI: "wi_2", Slug: "p#2",
			Class: "failed", Source: "failed_gate", Phrase: "the workflow failed", Attention: true},
		{ID: 1, At: "2026-11-09T10:00:00Z", Kind: protocol.DecisionWorkItem, Profile: "default", WI: "wi_1",
			Class: "needs_input", Source: "needs_input", Phrase: "a step is waiting for a human answer", QuestionID: "q-1", Attention: true, Resolved: true},
	}}
	env := watchEnv(w, t, map[string]string{"PF_PROFILE": "alpha"})
	code, out, stderr := runPf(t, env, "watch", "decisions")
	if code != 0 {
		t.Fatalf("decisions: %d %s", code, stderr)
	}
	for _, want := range []string{"1 open decision(s), 3 retained", "#3", "task t_1 completed", "pf task show t_1",
		"OPEN", "alpha:p#2 failed (failed_gate) — the workflow failed", "resolved", "wi_1 needs_input q-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	code, out, _ = runPf(t, env, "--json", "watch", "decisions")
	var got protocol.DecisionFeed
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Decisions) != 3 || got.Attention != 1 {
		t.Fatalf("decisions --json: %d %q", code, out)
	}
	for _, r := range w.seen() {
		if strings.HasPrefix(r, "GET /decisions") && !strings.HasSuffix(r, "profile=") {
			t.Fatalf("the feed is Host-scoped, yet a profile rode: %s", r)
		}
	}
	if code, out, stderr = runPf(t, env, "watch", "dismiss", "2", "#3"); code != 0 || !strings.Contains(out, "dismissed 2") {
		t.Fatalf("dismiss: %d %q %q", code, out, stderr)
	}
	if code, out, _ = runPf(t, env, "watch", "dismiss", "--all"); code != 0 || !strings.Contains(out, "dismissed 2") {
		t.Fatalf("dismiss --all: %d %q", code, out)
	}
	for _, bad := range [][]string{{"watch", "dismiss"}, {"watch", "dismiss", "x"}, {"watch", "dismiss", "0"}, {"watch", "dismiss", "1", "--all"}} {
		if code, _, _ := runPf(t, env, bad...); code == 0 {
			t.Fatalf("%v must be refused", bad)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.dismiss) != 2 || len(w.dismiss[0].IDs) != 2 || w.dismiss[0].IDs[1] != 3 || !w.dismiss[1].All {
		t.Fatalf("dismiss bodies = %+v", w.dismiss)
	}
}

// TestFollowUpResolvesLikeAnyCellCommand ( AC5): a follow-up
// on a work item (`pf answer`, `pf watch <wi> --once`) without --profile
// resolves its Cell by the cwd / PF_PROFILE rules; a leftover PF_CHAT_ID
// changes nothing and no chat route is read.
func TestFollowUpResolvesLikeAnyCellCommand(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{} // request path -> X-PF-Profile
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method+" "+r.URL.Path] = r.Header.Get(protocol.ProfileHeader)
		mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/chats/"):
			t.Errorf("a retired chat route was read: %s", r.URL.Path)
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "/watch"):
			_ = json.NewEncoder(w).Encode(protocol.WatchSnapshot{WorkItem: "x", Known: true})
		case strings.HasSuffix(r.URL.Path, "/answer"):
			_, _ = w.Write([]byte(`{"question_id":"q-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	env := map[string]string{"PF_DAEMON_SOCKET": sock, "PF_CHAT_ID": "chat_f", "PF_PROFILE": "beta", "PF_USER_ID": "u"}
	header := func(key string) string {
		mu.Lock()
		defer mu.Unlock()
		return seen[key]
	}
	if code, _, stderr := runPf(t, env, "watch", "wi_def", "--once"); code != 0 {
		t.Fatalf("watch: %d %s", code, stderr)
	}
	if got := header("GET /jobs/wi_def/watch"); got != "beta" {
		t.Fatalf("watch --once must follow PF_PROFILE: %q", got)
	}
	if code, _, stderr := runPf(t, env, "answer", "p#2", "--text", "yes"); code != 0 {
		t.Fatalf("answer: %d %s", code, stderr)
	}
	if got := header("POST /jobs/p#2/answer"); got != "beta" {
		t.Fatalf("answer must follow PF_PROFILE: %q", got)
	}
	if code, _, _ := runPf(t, env, "--profile", "gamma", "watch", "wi_def", "--once"); code != 0 {
		t.Fatal("an explicit --profile follow-up")
	}
	if got := header("GET /jobs/wi_def/watch"); got != "gamma" {
		t.Fatalf("an explicit --profile wins: %q", got)
	}
}

// TestWatchDismissJSONIsTheDaemonAnswer pins the wire shape the Pi Web
// panel's dismiss route reads (pi-web lib/polyforge-decisions.ts):
// `pf --json watch dismiss` prints the daemon's DecisionDismissAnswer
// verbatim — exactly {"dismissed":N} — and a refusal is a non-zero exit
// with nothing on stdout.
func TestWatchDismissJSONIsTheDaemonAnswer(t *testing.T) {
	w := newWatchDaemon(t, protocol.WatchSnapshot{})
	env := watchEnv(w, t, nil)
	code, out, stderr := runPf(t, env, "--json", "watch", "dismiss", "4", "5")
	if code != 0 || strings.TrimSpace(out) != `{"dismissed":2}` {
		t.Fatalf("dismiss --json: %d %q %q", code, out, stderr)
	}
	refusing := newFakeDaemon(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusForbidden)
		_, _ = rw.Write([]byte(`{"code":"DAEMON-403","detail":"a step process tree may not dismiss"}`))
	}))
	code, out, stderr = runPf(t, map[string]string{"PF_DAEMON_SOCKET": refusing}, "--json", "watch", "dismiss", "4")
	if code == 0 || strings.TrimSpace(out) != "" || !strings.Contains(stderr, "DAEMON-403") {
		t.Fatalf("a refused dismiss must exit non-zero with stdout empty: %d %q %q", code, out, stderr)
	}
}
