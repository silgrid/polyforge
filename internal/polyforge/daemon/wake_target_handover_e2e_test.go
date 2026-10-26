package daemon

// wake_target_handover_e2e_test.go
// (wi_zgL0mlf5zegK, artifacts/e2e-scenarios.md) acceptance e2e: the
// wake-target handover. Authored against artifacts/spec.md ("##
// Acceptance criteria" + its "#### Scenario" blocks) BEFORE the code,
// on the pre-implementation tree: every scenario below is an
// assertion-level red there (the exact failure is named in each
// assertion) and turns green only on the delivered behavior.
//
// The runnable form follows the repo's hermetic daemon e2e convention
// (chatwatch_e2e_test.go / needs_input_wake_fanout_repro_test.go): a
// real in-process daemon (newTestDaemon) over a two-workspace mapping,
// real chats minted over the real POST /chat/init route, and the real
// resident watcher (watcher.Run — the engine the pf-chat extension
// spawns per session) per chat.
//
// Wire contracts these tests ride — settled by artifacts/plan.md (the
// spec's own resolution layer), never an implementation guess:
//
//   - POST /chat/init gains the body field "workspace": the workspace
//     root the CLI resolved the session cwd to ("" = unmapped cwd).
//     The daemon records the tag on the chat's chat-subs.json chatDoc
//     (chatDoc.Workspace); chats.json — the (harness, session)→id
//     identity map, the pi-web readback contract — keeps its shape.
//   - POST /chats/{id}/adopt {"wi"} is the adopt route. The whole UDS
//     face sits behind the existing refuseStepOrigin guard, so a step
//     process tree is refused DAEMON-403 on it by construction — that
//     half is existing behavior and gets no scenario here. Refusals
//     answer the existing DAEMON-4xx family: an ineligible adopter
//     DAEMON-400 naming the WI's workspace, a live wake target
//     DAEMON-409 naming its chat id.
//
// The raw-HTTP helpers below exist so the tests can drive those wire
// shapes without referencing symbols that do not exist on the
// pre-implementation tree: the requests compile and run today (unknown
// body fields are ignored; an unregistered route answers 404), so the
// baseline red is assertion-level, never build-level.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// gmi14Post issues one raw HTTP request against the daemon's UDS face —
// the same face the CLI dials — so a test can drive a wire shape the
// DaemonClient does not expose on the pre-implementation tree.
func gmi14Post(t *testing.T, td *testDaemon, method, path string, body any) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", td.sock)
	}}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	var rd io.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(blob)
	}
	req, err := http.NewRequest(method, "http://daemon"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// gmi14ChatInit mints (or resumes) one chat over the real /chat/init
// route, tagged with the workspace root the route's new body field
// carries ("" = untagged: an unmapped session cwd).
func gmi14ChatInit(t *testing.T, td *testDaemon, session, workspace string) string {
	t.Helper()
	body := map[string]any{"harness": "pi", "harness_session": session}
	if workspace != "" {
		body["workspace"] = workspace
	}
	code, raw := gmi14Post(t, td, http.MethodPost, "/chat/init", body)
	if code != http.StatusOK {
		t.Fatalf("chat init %q (workspace %q): status %d body %s", session, workspace, code, raw)
	}
	var rec cli.ChatRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil || rec.ChatID == "" {
		t.Fatalf("chat init %q answer: %s (%v)", session, raw, err)
	}
	return rec.ChatID
}

// gmi14Adopt runs the adopt route for one chat and answers the HTTP
// status, the typed refusal the body carries (zero when none) and the
// raw body.
func gmi14Adopt(t *testing.T, td *testDaemon, chat, wi string) (int, api.Error, string) {
	t.Helper()
	code, raw := gmi14Post(t, td, http.MethodPost, "/chats/"+chat+"/adopt", map[string]any{"wi": wi})
	var ae api.Error
	_ = json.Unmarshal([]byte(raw), &ae) // a non-envelope body (404) stays zero
	return code, ae, raw
}

// gmi14RunChat runs one chat's real resident watcher and answers its
// decision stdout. waitDecision mirrors the pf-chat spawns: a chat that
// prints a decision exits after it; a chat expected silent just polls
// for d and answers what leaked.
func gmi14RunChat(t *testing.T, td *testDaemon, chat string, d time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var out bytes.Buffer
	opts := watcher.Options{Owner: "alice", WaitDecision: true, Out: &out, Poll: 20 * time.Millisecond}
	if err := watcher.Run(ctx, clientAPI{d: td.client, chat: chat}, opts); err != nil {
		t.Fatalf("%s watcher: %v", chat, err)
	}
	return out.String()
}

// gmi14Scene is the pi#122 repro scene: two mapped workspaces — wsA
// holds the WI's project "proj" (the fake hub answers it for every WI),
// wsB a foreign project — and the chats minted on them: the wsA origin,
// a second wsA chat (the adopter-to-be), a wsA chat that mutes the WI
// itself, a wsB chat and an untagged chat.
type gmi14Scene struct {
	td             *testDaemon
	wi, wsA, wsB   string
	origin, a2, a3 string
	b1, untagged   string
}

// gmi14Setup builds the scene up to the live, monitored WI: daemon over
// the two-workspace mapping, chats minted and tagged over /chat/init,
// the WI submitted (origin chat when originChat, else chat-less with
// alice as its reporter — the auto-pull claim shape).
func gmi14Setup(t *testing.T, originChat bool) *gmi14Scene {
	t.Helper()
	dir := t.TempDir()
	wsA, wsB := filepath.Join(dir, "wsA"), filepath.Join(dir, "wsB")
	for _, d := range []string{wsA, wsB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := mapping.Build([]mapping.Entry{
		{Workspace: wsA, Projects: []string{"proj"}},
		{Workspace: wsB, Projects: []string{"gmi14other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	td := newTestDaemon(t, func(c *Config) { c.Mapping = p })
	td.hub.mu.Lock()
	td.hub.slugs = map[string]string{"wi_gmi14": "proj#122"}
	td.hub.mu.Unlock()
	s := &gmi14Scene{td: td, wi: "wi_gmi14", wsA: wsA, wsB: wsB}
	s.origin = gmi14ChatInit(t, td, "gmi14-origin", wsA)
	s.a2 = gmi14ChatInit(t, td, "gmi14-a2", wsA)
	s.a3 = gmi14ChatInit(t, td, "gmi14-a3", wsA)
	s.b1 = gmi14ChatInit(t, td, "gmi14-b1", wsB)
	s.untagged = gmi14ChatInit(t, td, "gmi14-untagged", "")
	if originChat {
		td.client.Origin = &cli.SubmitOrigin{ChatID: s.origin, Owner: "alice"}
	} else {
		td.hub.mu.Lock()
		td.hub.reporters = map[string]string{"wi_gmi14": "alice"}
		td.hub.mu.Unlock()
	}
	td.submitClaim(t, s.wi)
	td.waitMonitored(t, s.wi)
	return s
}

// gmi14Own subscribes one chat through owner routing (the second
// pi-web tab pattern: every watcher cycle does this) and marks it live.
func gmi14Own(t *testing.T, td *testDaemon, chat string) {
	t.Helper()
	if _, err := td.client.ChatSubscribeOwned(chat, "alice"); err != nil {
		t.Fatal(err)
	}
}

// gmi14Mute unsubscribes (and thereby mutes) one chat on the WI.
func gmi14Mute(t *testing.T, td *testDaemon, chat, wi string) {
	t.Helper()
	if _, err := td.client.ChatSubscribe(chat, wi, false); err != nil {
		t.Fatal(err)
	}
}

// gmi14SeedQ plays a needs_input park for the WI and waits for the
// job to present it.
func gmi14SeedQ(t *testing.T, td *testDaemon, wi, qid string) {
	t.Helper()
	td.seedPendingQuestion(t, wi, qid, "plan", "?")
	waitNeedsInput(t, td, wi)
}

// gmi14ScopedWakeScene arms the scoped-broadcast condition of the scene
// (the origin chat unsubscribed when there is one) with the full pi#122
// audience subscribed through owner routing — the second wsA chat, the
// wsB chat, the untagged chat, and a wsA chat that muted the WI itself —
// and one undelivered needs_input decision.
func gmi14ScopedWakeScene(t *testing.T, originChat bool) *gmi14Scene {
	t.Helper()
	s := gmi14Setup(t, originChat)
	for _, chat := range []string{s.a2, s.a3, s.b1, s.untagged} {
		gmi14Own(t, s.td, chat)
	}
	gmi14Mute(t, s.td, s.a3, s.wi) // its explicit wish: nothing, ever
	if originChat {
		gmi14Mute(t, s.td, s.origin, s.wi)
	}
	gmi14SeedQ(t, s.td, s.wi, "q-1")
	return s
}

// gmi14StatusOrigin answers the WI's origin chat id as `pf ops status`
// reports it (the field a resume rewrites — the adopt must rewrite it).
func gmi14StatusOrigin(t *testing.T, td *testDaemon, wi string) string {
	t.Helper()
	raw, err := td.client.Status()
	if err != nil {
		t.Fatal(err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for _, j := range st.Jobs {
		if j.WorkItemID == wi {
			if j.Origin == nil {
				return ""
			}
			return j.Origin.ChatID
		}
	}
	t.Fatalf("%s never answered in status", wi)
	return ""
}

// ---------------------------------------------------------------------------
// `pf chat init` tags the chat with the workspace its cwd resolves to
// (spec requirement 1). The resolution is the CLI's, against its own
// cli.toml [workspace] mapping — so these scenarios run the REAL pf CLI
// (in-process, the same entry point a harness session uses, its cwd
// moved with t.Chdir) against the real daemon, and read back the tag
// the daemon recorded.
// ---------------------------------------------------------------------------

// gmi14CLIConfig writes a scratch cli.toml mapping the given workspace
// roots (one project each) and points the CLI at it via
// XDG_CONFIG_HOME.
func gmi14CLIConfig(t *testing.T, roots ...string) {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("server = \"http://gmi14.invalid:1\"\n\n")
	for i, root := range roots {
		fmt.Fprintf(&b, "[workspace.\"%s\"]\nprojects = [\"gmi14p%d\"]\n\n", root, i)
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
}

// gmi14ChatInitCLI runs the real pf CLI's chat init and answers the
// chat id it printed (its stdout contract: the bare id).
func gmi14ChatInitCLI(t *testing.T, session string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"chat", "init", "--harness", "pi", "--harness-session", session},
		bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("pf chat init: code %d stderr %s", code, stderr.String())
	}
	id := strings.TrimSpace(stdout.String())
	if id == "" || strings.Contains(id, "\n") {
		t.Fatalf("pf chat init stdout must be the bare chat id: %q", stdout.String())
	}
	return id
}

// gmi14ChatTag answers the workspace tag the daemon recorded on the
// chat (chat-subs.json, the chat's chatDoc) and whether the chat is
// recorded there at all.
func gmi14ChatTag(t *testing.T, td *testDaemon, chat string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, "chat-subs.json"))
	if err != nil {
		return "", false
	}
	var doc struct {
		Chats map[string]struct {
			Workspace string `json:"workspace"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Chats == nil {
		return "", false
	}
	c, ok := doc.Chats[chat]
	return c.Workspace, ok
}

// TestChatInitWorkspaceTag — three scenarios of the chat workspace
// tag (artifacts/e2e-scenarios.md): the longest-prefix resolution from
// a cwd under two nested mapped roots, the untagged chat an unmapped
// cwd mints (recorded, no tag), and the refresh a re-init takes from the
// current cwd.
func TestChatInitWorkspaceTag(t *testing.T) {
	dir := t.TempDir()
	wsRoot := filepath.Join(dir, "ws")    // a mapped root
	deep := filepath.Join(wsRoot, "deep") // a SECOND mapped root inside it
	for _, d := range []string{
		filepath.Join(deep, "capsules", "gmi14"), // a subdirectory of both roots
		filepath.Join(wsRoot, "repos"),           // a subdirectory of wsRoot only
		filepath.Join(dir, "unmapped"),           // under no mapped root
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := mapping.Build([]mapping.Entry{
		{Workspace: wsRoot, Projects: []string{"proj"}},
		{Workspace: deep, Projects: []string{"gmi14deep"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	td := newTestDaemon(t, func(c *Config) { c.Mapping = p })
	gmi14CLIConfig(t, wsRoot, deep)
	t.Setenv("PF_DAEMON_SOCKET", td.sock)

	t.Run("longest prefix wins", func(t *testing.T) {
		t.Chdir(filepath.Join(deep, "capsules", "gmi14"))
		id := gmi14ChatInitCLI(t, "gmi14-s1")
		tag, ok := gmi14ChatTag(t, td, id)
		if !ok || tag != deep {
			t.Fatalf("a cwd under both %s and %s must tag the chat with the LONGEST prefix %q: tag %q recorded %v", wsRoot, deep, deep, tag, ok)
		}
	})
	t.Run("unmapped cwd mints an untagged chat", func(t *testing.T) {
		t.Chdir(filepath.Join(dir, "unmapped"))
		id := gmi14ChatInitCLI(t, "gmi14-s2")
		tag, ok := gmi14ChatTag(t, td, id)
		if !ok || tag != "" {
			t.Fatalf("an unmapped cwd must mint a RECORDED chat with NO workspace tag (neither adopt-eligible nor in the scoped broadcast): tag %q recorded %v", tag, ok)
		}
	})
	t.Run("re-init refreshes the tag", func(t *testing.T) {
		t.Chdir(filepath.Join(deep, "capsules", "gmi14"))
		id1 := gmi14ChatInitCLI(t, "gmi14-s3")
		t.Chdir(filepath.Join(wsRoot, "repos"))
		id2 := gmi14ChatInitCLI(t, "gmi14-s3") // the same harness session resumes
		if id1 != id2 {
			t.Fatalf("a resumed session keeps its chat: %s then %s", id1, id2)
		}
		if tag, _ := gmi14ChatTag(t, td, id2); tag != wsRoot {
			t.Fatalf("re-init must REFRESH the tag from the current cwd: want %q got %q", wsRoot, tag)
		}
	})
}

// ---------------------------------------------------------------------------
// The scoped decision wake (spec requirement 2) — the pi#122 repro.
// ---------------------------------------------------------------------------

// TestOriginUnsubscribedScopedWake — the origin chat unsubscribed
// (and thereby muted) the WI: the decision goes immediately and only to
// the live, subscribed, same-workspace, non-muted chats of the WI. On
// the pre-implementation tree this is the pi#122 blast radius itself:
// every subscribed chat wakes from its own record, so the cross-workspace
// and the untagged chat print the decision line too.
func TestOriginUnsubscribedScopedWake(t *testing.T) {
	s := gmi14ScopedWakeScene(t, true)
	if out := gmi14RunChat(t, s.td, s.a2, 5*time.Second); !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the WI's workspace's live subscribed chat must be woken: %q", out)
	}
	for _, c := range []struct{ chat, why string }{
		{s.b1, "a cross-workspace chat"},
		{s.untagged, "an untagged chat"},
		{s.a3, "a chat that muted the WI itself"},
	} {
		if out := gmi14RunChat(t, s.td, c.chat, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
			t.Fatalf("REPORTED pi#122 BLAST RADIUS: the decision reached %s: %q", c.why, out)
		}
	}
}

// TestChatlessSubmitScopedWake — the WI has no origin chat (a
// chat-less submit, alice its reporter): the same scoped wake applies,
// identically to the unsubscribed case.
func TestChatlessSubmitScopedWake(t *testing.T) {
	s := gmi14ScopedWakeScene(t, false)
	if out := gmi14RunChat(t, s.td, s.a2, 5*time.Second); !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the WI's workspace's live subscribed chat must be woken: %q", out)
	}
	for _, c := range []struct{ chat, why string }{
		{s.b1, "a cross-workspace chat"},
		{s.untagged, "an untagged chat"},
	} {
		if out := gmi14RunChat(t, s.td, c.chat, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
			t.Fatalf("REPORTED pi#122 BLAST RADIUS: the decision reached %s: %q", c.why, out)
		}
	}
}

// TestScopedDecisionCarriesAdoptHint — the scoped-broadcast
// decision line carries the one human-readable hint line
// 「可用 `pf watch adopt <wi>` 接管」, and nothing adopts on its own:
// after the woken chat's watcher printed, the WI's origin chat field
// still names the unsubscribed origin (no watcher ever adopts).
func TestScopedDecisionCarriesAdoptHint(t *testing.T) {
	s := gmi14ScopedWakeScene(t, true)
	out := gmi14RunChat(t, s.td, s.a2, 5*time.Second)
	if !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the eligible chat must be woken first: %q", out)
	}
	if !strings.Contains(out, "pf watch adopt") || !strings.Contains(out, "接管") {
		t.Fatalf("the scoped decision line must carry the adopt hint 「可用 `pf watch adopt <wi>` 接管」: %q", out)
	}
	if origin := gmi14StatusOrigin(t, s.td, s.wi); origin != s.origin {
		t.Fatalf("no automatic adopt: the origin chat field must still name the unsubscribed origin %s, got %q", s.origin, origin)
	}
}

// ---------------------------------------------------------------------------
// `pf watch adopt <wi>` (spec requirement 3).
// ---------------------------------------------------------------------------

// TestAdoptInheritsRecordAndRewritesOrigin — an eligible
// same-workspace chat adopts: the adopt succeeds, the WI's origin chat
// field is rewritten to the adopter, and the adopter's record is the
// union — the decision the origin chat delivered before it unsubscribed
// does not replay, the undelivered one prints once.
func TestAdoptInheritsRecordAndRewritesOrigin(t *testing.T) {
	s := gmi14Setup(t, true)
	gmi14SeedQ(t, s.td, s.wi, "q-1")
	// the origin chat's watcher delivered q-1 into its record
	if out := gmi14RunChat(t, s.td, s.origin, 5*time.Second); !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the origin chat must be woken first: %q", out)
	}
	gmi14Own(t, s.td, s.b1) // the cross-workspace owner-routed subscriber
	gmi14Mute(t, s.td, s.origin, s.wi)
	gmi14SeedQ(t, s.td, s.wi, "q-2") // a second decision nobody delivered

	code, _, raw := gmi14Adopt(t, s.td, s.a2, s.wi)
	if code != http.StatusOK || !strings.Contains(raw, `"adopted":true`) {
		t.Fatalf("adopt by an eligible same-workspace chat must succeed: status %d body %s", code, raw)
	}
	if origin := gmi14StatusOrigin(t, s.td, s.wi); origin != s.a2 {
		t.Fatalf("the WI's origin chat field must be rewritten to the adopter %s: %q", s.a2, origin)
	}
	out := gmi14RunChat(t, s.td, s.a2, 5*time.Second)
	if !strings.Contains(out, "needs_input q-2") || strings.Contains(out, "q-1") {
		t.Fatalf("the adopter inherits the delivered q-1 (nothing replays) and gets q-2 once: %q", out)
	}
}

// TestAdoptWithoutOriginRecord — the WI has no origin chat (a
// chat-less submit): an eligible same-workspace chat adopts and the
// adopter's own record stands — what it delivered before the adopt does
// not replay, the next decision prints once.
func TestAdoptWithoutOriginRecord(t *testing.T) {
	s := gmi14Setup(t, false)
	gmi14Own(t, s.td, s.a2)
	gmi14SeedQ(t, s.td, s.wi, "q-1")
	if out := gmi14RunChat(t, s.td, s.a2, 5*time.Second); !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the adopter-to-be holds the delivered q-1 in its own record: %q", out)
	}
	code, _, raw := gmi14Adopt(t, s.td, s.a2, s.wi)
	if code != http.StatusOK || !strings.Contains(raw, `"adopted":true`) {
		t.Fatalf("adopt with no origin record to inherit must succeed: status %d body %s", code, raw)
	}
	if origin := gmi14StatusOrigin(t, s.td, s.wi); origin != s.a2 {
		t.Fatalf("the WI's origin chat field must be rewritten to the adopter %s: %q", s.a2, origin)
	}
	gmi14SeedQ(t, s.td, s.wi, "q-2")
	out := gmi14RunChat(t, s.td, s.a2, 5*time.Second)
	if !strings.Contains(out, "needs_input q-2") || strings.Contains(out, "q-1") {
		t.Fatalf("the adopter's own record stands (q-1 never replays) and q-2 prints once: %q", out)
	}
}

// TestAdoptTakesOverTheWakeTargetRole — after an adopt the adopter
// IS the origin: a later decision fires no further scoped broadcast
// (the cross-workspace owner-routed subscriber is Passive relative to
// the live adopter, exactly as under a live origin), and a self re-adopt
// is idempotent — success, no state change, no replay.
func TestAdoptTakesOverTheWakeTargetRole(t *testing.T) {
	s := gmi14Setup(t, true)
	gmi14Own(t, s.td, s.b1)
	gmi14Mute(t, s.td, s.origin, s.wi)
	gmi14SeedQ(t, s.td, s.wi, "q-1") // undelivered: the origin never ran a watcher

	code, _, raw := gmi14Adopt(t, s.td, s.a2, s.wi)
	if code != http.StatusOK || !strings.Contains(raw, `"adopted":true`) {
		t.Fatalf("adopt by an eligible same-workspace chat must succeed: status %d body %s", code, raw)
	}
	if out := gmi14RunChat(t, s.td, s.a2, 5*time.Second); !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the adopter is the wake target now: %q", out)
	}

	// self re-adopt: idempotent — success, no state change, no replay
	code, _, raw = gmi14Adopt(t, s.td, s.a2, s.wi)
	if code != http.StatusOK || !strings.Contains(raw, `"adopted":true`) {
		t.Fatalf("a self re-adopt must succeed idempotently: status %d body %s", code, raw)
	}
	subs, err := s.td.client.ChatSubscriptions(s.a2)
	if err != nil || len(subs.Subscriptions) != 1 {
		t.Fatalf("the adopter's subscription after the re-adopt: %+v (%v)", subs, err)
	}
	if len(subs.Subscriptions[0].Notified) != 1 || !strings.Contains(subs.Subscriptions[0].Notified[0], "q-1") {
		t.Fatalf("the re-adopt must change no state (the delivered q-1 stays delivered): %+v", subs.Subscriptions[0])
	}
	if out := gmi14RunChat(t, s.td, s.a2, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
		t.Fatalf("a self re-adopt replays nothing: %q", out)
	}

	// a later decision: no further scoped broadcast — the cross-workspace
	// owner-routed subscriber is Passive relative to the live adopter
	if err := s.td.obs.Events().Append(s.wi, "step_dead", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if out := gmi14RunChat(t, s.td, s.b1, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
		t.Fatalf("after the adopt the cross-workspace owner-routed chat must be Passive to the adopter: %q", out)
	}
}

// TestAdoptRefusedOutsideWorkspace — a chat tagged with a different
// workspace, and an untagged chat, are refused with DAEMON-400 naming
// the WI's workspace — never a silent subscribe degradation: the
// refused adopter holds no subscription to the WI afterwards.
func TestAdoptRefusedOutsideWorkspace(t *testing.T) {
	s := gmi14Setup(t, true)
	gmi14SeedQ(t, s.td, s.wi, "q-1")
	gmi14Mute(t, s.td, s.origin, s.wi) // the post-broadcast state
	for _, c := range []struct{ chat, why string }{
		{s.b1, "a cross-workspace chat"},
		{s.untagged, "an untagged chat"},
	} {
		code, ae, raw := gmi14Adopt(t, s.td, c.chat, s.wi)
		if code != http.StatusBadRequest || ae.Code != api.ErrDaemonBadInput || !strings.Contains(ae.Detail, s.wsA) {
			t.Fatalf("%s's adopt must be refused with DAEMON-400 naming the WI's workspace %s: status %d body %s", c.why, s.wsA, code, raw)
		}
		subs, err := s.td.client.ChatSubscriptions(c.chat)
		if err != nil {
			t.Fatal(err)
		}
		for _, sub := range subs.Subscriptions {
			if sub.WI == s.wi {
				t.Fatalf("a refused adopt must not degrade into a subscribe: %s holds %v", c.why, sub)
			}
		}
	}
}

// TestAdoptRefusedWhileTargetSubscribed — the WI's current wake
// target is still subscribed: another chat's adopt is refused with
// DAEMON-409 naming the target's chat id, and the target's subscription
// is left alone.
func TestAdoptRefusedWhileTargetSubscribed(t *testing.T) {
	s := gmi14Setup(t, true)
	gmi14SeedQ(t, s.td, s.wi, "q-1")
	code, ae, raw := gmi14Adopt(t, s.td, s.a2, s.wi)
	if code != http.StatusConflict || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, s.origin) {
		t.Fatalf("adopt while the wake target is still subscribed must be refused with DAEMON-409 naming its chat id %s: status %d body %s", s.origin, code, raw)
	}
	subs, err := s.td.client.ChatSubscriptions(s.origin)
	if err != nil || len(subs.Subscriptions) != 1 || subs.Subscriptions[0].WI != s.wi {
		t.Fatalf("the refusal must leave the target's subscription alone: %+v (%v)", subs, err)
	}
}
