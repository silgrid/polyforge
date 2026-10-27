package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

func TestSubStorePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), chatSubsFile)
	s, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if ch, err := s.Subscribe("chat_a", "wi_1", "", 7); err != nil || !ch {
		t.Fatalf("subscribe: %v %v", ch, err)
	}
	if ch, _ := s.Subscribe("chat_a", "wi_1", "", 99); ch {
		t.Fatal("re-subscribing an active WI changes nothing and must not move the cursor")
	}
	// a re-subscribe that knows the slug fills a subscription recorded without one
	if _, err := s.Subscribe("chat_a", "wi_1", "proj#1", 99); err != nil {
		t.Fatal(err)
	}
	if subs := s.List("chat_a"); len(subs) != 1 || subs[0].Slug != "proj#1" || subs[0].Cursor != 7 {
		t.Fatalf("slug fill: %+v", subs)
	}
	if err := s.Ack("chat_a", protocol.ChatAck{WI: "wi_1", Cursor: 12, Notified: []string{"needs_input:q-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe("chat_b", "wi_2", "", 0); err != nil {
		t.Fatal(err)
	}
	// daemon restart: a brand-new store over the same file
	s2, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.List("chat_a")
	if len(got) != 1 || got[0].WI != "wi_1" || got[0].Cursor != 12 || len(got[0].Notified) != 1 || got[0].Notified[0] != "needs_input:q-1" {
		t.Fatalf("after restart: %+v", got)
	}
	if l := s2.List("chat_b"); len(l) != 1 || l[0].WI != "wi_2" {
		t.Fatalf("chat_b: %+v", l)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", st, err)
	}
	// a finished WI is re-armed by a new submit
	_ = s2.Ack("chat_a", protocol.ChatAck{WI: "wi_1", Cursor: 12, Done: true})
	if ch, _ := s2.Subscribe("chat_a", "wi_1", "", 0); !ch {
		t.Fatal("re-subscribe of a finished WI must re-arm it")
	}
	if l := s2.List("chat_a"); l[0].Done || l[0].Cursor != 12 {
		t.Fatalf("re-armed: %+v", l)
	}
	if ch, _ := s2.Unsubscribe("chat_a", "wi_1"); !ch || len(s2.List("chat_a")) != 0 {
		t.Fatal("unsubscribe")
	}
}

func TestSubStoreRefusesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), chatSubsFile)
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := newSubStore(path); err == nil {
		t.Fatal("a corrupt subscriptions file must refuse, not silently reset")
	}
}

func TestChatRoutesAndSubmitAutoSubscribe(t *testing.T) {
	td := newTestDaemon(t, nil)
	// explicit subscribe/list/unsubscribe through the UDS face
	if _, err := td.client.ChatSubscribe("chat_x", "wi_manual", true); err != nil {
		t.Fatal(err)
	}
	subs, err := td.client.ChatSubscriptions("chat_x")
	if err != nil || len(subs.Subscriptions) != 1 {
		t.Fatalf("subs = %+v err=%v", subs, err)
	}
	if _, err := td.client.ChatSubscriptions("bad/id"); err == nil {
		t.Fatal("an unsafe chat id must be refused")
	}
	// pf submit from a chat subscribes that chat
	td.client.Origin = &cli.SubmitOrigin{ChatID: "chat_x", Owner: "alice"}
	td.submitClaim(t, "wi_sub")
	subs, _ = td.client.ChatSubscriptions("chat_x")
	found := false
	for _, s := range subs.Subscriptions {
		found = found || s.WI == "wi_sub"
	}
	if !found {
		t.Fatalf("submit must auto-subscribe the submitting chat: %+v", subs)
	}
	// a submit without a chat subscribes nobody
	td.client.Origin = &cli.SubmitOrigin{Owner: "alice"}
	td.submitClaim(t, "wi_nochat")
	for _, s := range mustSubs(t, td, "chat_x") {
		if s.WI == "wi_nochat" {
			t.Fatal("a chat-less submit must not subscribe")
		}
	}
	if _, err := td.client.ChatSubscribe("chat_x", "wi_manual", false); err != nil {
		t.Fatal(err)
	}
}

func mustSubs(t *testing.T, td *testDaemon, chat string) []protocol.ChatSub {
	t.Helper()
	s, err := td.client.ChatSubscriptions(chat)
	if err != nil {
		t.Fatal(err)
	}
	return s.Subscriptions
}

// forgetSub drops a subscription the way a chat that never had one looks
// (no unsubscribe mark), so the owner routing adds it again.
func forgetSub(td *testDaemon, chat, wi string) {
	td.subs.mu.Lock()
	defer td.subs.mu.Unlock()
	if c := td.subs.chatLocked(chat, false); c != nil {
		delete(c.Subs, wi)
	}
}

// An explicit unsubscribe from an owned WI sticks across watcher cycles
// (each calls subscribe-owned) and across a daemon restart; an explicit
// subscribe lifts it.
func TestUnsubscribedOwnedWIStaysUnsubscribed(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.client.Origin = &cli.SubmitOrigin{ChatID: "chat_a", Owner: "alice"}
	td.submitClaim(t, "wi_own")
	td.waitMonitored(t, "wi_own")
	td.client.Origin = nil
	if _, err := td.client.ChatSubscribeOwned("chat_b", "alice"); err != nil {
		t.Fatal(err)
	}
	for _, chat := range []string{"chat_a", "chat_b"} {
		if len(mustSubs(t, td, chat)) != 1 {
			t.Fatalf("%s must start subscribed", chat)
		}
		if _, err := td.client.ChatSubscribe(chat, "wi_own", false); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if added, err := td.client.ChatSubscribeOwned(chat, "alice"); err != nil || len(added) != 0 {
				t.Fatalf("%s cycle %d re-added %+v (err %v)", chat, i, added, err)
			}
		}
		if subs := mustSubs(t, td, chat); len(subs) != 0 {
			t.Fatalf("%s after unsubscribe: %+v", chat, subs)
		}
	}
	reloaded, err := newSubStore(td.subs.path)
	if err != nil || reloaded.Settled("chat_b", "wi_own") != true || len(reloaded.List("chat_b")) != 0 {
		t.Fatalf("the unsubscribe must persist: err %v", err)
	}
	if _, err := td.client.ChatSubscribe("chat_b", "wi_own", true); err != nil {
		t.Fatal(err)
	}
	if subs := mustSubs(t, td, "chat_b"); len(subs) != 1 || subs[0].WI != "wi_own" {
		t.Fatalf("an explicit subscribe must lift the unsubscribe: %+v", subs)
	}
	if _, err := td.client.ChatSubscribe("chat_b", "wi_own", false); err != nil {
		t.Fatal(err)
	}
	if _, err := td.client.ChatSubscribe("chat_b", "wi_own", true); err != nil {
		t.Fatal(err)
	}
	if added, _ := td.client.ChatSubscribeOwned("chat_b", "alice"); len(added) != 0 || len(mustSubs(t, td, "chat_b")) != 1 {
		t.Fatalf("re-subscribed WI must be held once: %+v", added)
	}
}

func TestWatchSnapshotRoute(t *testing.T) {
	td := newTestDaemon(t, nil)
	// not registered yet (the fake hub resolves it as running, this daemon
	// holds nothing): a normal, empty local answer (queued/preparing)
	snap, err := td.client.WatchSnapshot("wi_nothing", 0, false)
	if err != nil || snap.Known || snap.Execution != nil || snap.Job != nil {
		t.Fatalf("unknown WI: %+v %v", snap, err)
	}
	td.submitClaim(t, "wi_snap")
	td.waitMonitored(t, "wi_snap")
	td.seedPendingQuestion(t, "wi_snap", "q-1", "plan", "Which database?")
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err = td.client.WatchSnapshot("wi_snap", 0, false)
		if err == nil && snap.Execution != nil && snap.Execution.PendingQuestion != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no execution layer: %+v %v", snap, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.Execution.PendingQuestion.ID != "q-1" || snap.Execution.CurrentStep != "plan" || !snap.Known || snap.Job == nil {
		t.Fatalf("snapshot = %+v", snap)
	}
	if _, err := td.client.WatchSnapshot("wi_snap", -1, false); err == nil {
		t.Fatal("a negative cursor is bad input")
	}
}

func TestWatchSnapshotReadsFinishedWIFromTheCapsule(t *testing.T) {
	td := newTestDaemon(t, nil)
	// a WI the daemon holds no job for, but whose capsule is on disk
	// (finished while the watcher was down): its hub record names the
	// capsule, capsules/<project>-<seq> (polyforge#144)
	td.hub.slugs = map[string]string{"wi_gone": "proj#9"}
	td.seedPendingQuestion(t, "wi_gone", "q-9", "s1", "x")
	snap, err := td.client.WatchSnapshot("wi_gone", 0, false)
	if err != nil || snap.Execution == nil || !snap.Known || snap.Slug != "proj#9" {
		t.Fatalf("capsule fallback: %+v %v", snap, err)
	}
}

// A chat poll of a subscribed work item the daemon holds no job for
// reads aihub at most once per snapshot, by id or by slug: the id
// resolution's record is the snapshot's record, and the live-progress
// lookup reads no hub.
func TestWatchSnapshotOfNonLiveWIReadsTheHubOnce(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	td.hub.slugs = map[string]string{"wi_gone": "proj#9"}
	td.hub.canonical = map[string]string{"proj#9": "wi_gone"}
	td.hub.mu.Unlock()
	td.seedPendingQuestion(t, "wi_gone", "q-9", "s1", "x")
	gets := func() int {
		td.hub.mu.Lock()
		defer td.hub.mu.Unlock()
		return td.hub.gets["wi_gone"]
	}
	for _, spelling := range []string{"wi_gone", "proj#9"} {
		base := gets()
		snap, err := td.client.WatchSnapshot(spelling, 0, false)
		if err != nil || snap.WorkItem != "wi_gone" || snap.Slug != "proj#9" || snap.Execution == nil {
			t.Fatalf("snapshot by %s: %+v %v", spelling, snap, err)
		}
		if n := gets() - base; n != 1 {
			t.Fatalf("snapshot by %s read the hub %d times, want 1", spelling, n)
		}
	}
}

// A watcher's start subscribes its chat to the held WIs the chat
// submitted or its owner owns (origin owner, else the reporter), once; a
// new subscription's cursor sits just before the WI's newest decision
// event, and a finished subscription is not re-armed.
func TestSubscribeOwnedRoutingAndOwnerFallback(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.reporters = map[string]string{"wi_auto": "bob"}
	// wi_chat: submitted from chat_a by alice; wi_auto: no origin owner (reporter bob)
	td.client.Origin = &cli.SubmitOrigin{ChatID: "chat_a", Owner: "alice"}
	td.submitClaim(t, "wi_chat")
	td.client.Origin = nil
	td.submitClaim(t, "wi_auto")
	for _, wi := range []string{"wi_chat", "wi_auto"} {
		td.waitMonitored(t, wi)
	}
	ev := td.obs.Events()
	_ = ev.Append("wi_auto", "step_dead", map[string]any{})
	_ = ev.Append("wi_auto", "t_noise", nil)
	dead, _ := ev.Newest("wi_auto", func(k string) bool { return k == "step_dead" })
	deadSeq := dead.Seq

	owned := func(chat, owner string) map[string]int64 {
		t.Helper()
		added, err := td.client.ChatSubscribeOwned(chat, owner)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]int64{}
		for _, s := range added {
			m[s.WI] = s.Cursor
		}
		return m
	}
	if got := owned("chat_z", ""); len(got) != 0 {
		t.Fatalf("a stranger chat with no owner: %v", got)
	}
	forgetSub(td, "chat_a", "wi_chat") // the submit's own subscription
	before := ev.LastSeq("wi_chat")
	if got := owned("chat_a", ""); len(got) != 1 || got["wi_chat"] < before || got["wi_chat"] > ev.LastSeq("wi_chat") {
		t.Fatalf("origin chat: %v, want wi_chat from its current seq (%d)", got, before)
	}
	if got := owned("chat_y", "alice"); len(got) != 1 {
		t.Fatalf("owner alice: %v", got)
	}
	if got := owned("chat_z", "bob"); len(got) != 1 || got["wi_auto"] != deadSeq-1 {
		t.Fatalf("reporter fallback for bob: %v, want wi_auto just before its step_dead (seq %d)", got, deadSeq)
	}
	if got := owned("chat_z", "bob"); len(got) != 0 {
		t.Fatalf("a second start adds nothing: %v", got)
	}
	if err := td.subs.Ack("chat_y", protocol.ChatAck{WI: "wi_chat", Cursor: 1, Done: true}); err != nil {
		t.Fatal(err)
	}
	if got := owned("chat_y", "alice"); len(got) != 0 {
		t.Fatalf("a finished subscription must not be re-armed: %v", got)
	}
	if subs := mustSubs(t, td, "chat_z"); len(subs) != 1 || subs[0].WI != "wi_auto" || subs[0].Cursor != deadSeq-1 {
		t.Fatalf("chat_z subscriptions: %+v", subs)
	}
}

// Every watcher cycle calls subscribe-owned, so a repeated call costs no
// hub read: a WI's reporter is fetched once, and a WI the chat already
// holds a subscription for is skipped before any owner lookup.
func TestSubscribeOwnedRepeatedCallReadsTheHubOnce(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	td.hub.reporters = map[string]string{"wi_auto": "bob"}
	td.hub.mu.Unlock()
	td.submitClaim(t, "wi_auto") // no origin: the owner is the reporter
	td.waitMonitored(t, "wi_auto")
	gets := func() int {
		td.hub.mu.Lock()
		defer td.hub.mu.Unlock()
		return td.hub.gets["wi_auto"]
	}
	for _, owner := range []string{"carol", "carol", "bob"} {
		if _, err := td.client.ChatSubscribeOwned("chat_c", owner); err != nil {
			t.Fatal(err)
		}
	}
	base := gets()
	for i := 0; i < 3; i++ {
		for _, chat := range []string{"chat_c", "chat_d"} {
			if _, err := td.client.ChatSubscribeOwned(chat, "carol"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := gets(); n != base {
		t.Fatalf("repeated subscribe-owned read the hub %d more times, want 0", n-base)
	}
	if subs := mustSubs(t, td, "chat_c"); len(subs) != 1 || subs[0].WI != "wi_auto" {
		t.Fatalf("chat_c subscriptions: %+v", subs)
	}
}

func waitNeedsInput(t *testing.T, td *testDaemon, wis ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		jobs, _ := td.statusJobs()
		n := 0
		for _, j := range jobs {
			for _, wi := range wis {
				if j.WorkItemID == wi && j.NeedsInput {
					n++
				}
			}
		}
		if n == len(wis) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs never presented needs_input: %+v", jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWatchProgressProjection(t *testing.T) {
	if p := watchProgress(observe.LiveProgress{InDispatch: true, Step: "s"}); p != nil {
		t.Fatalf("an empty dispatch has nothing to show: %+v", p)
	}
	p := watchProgress(observe.LiveProgress{InDispatch: true, Command: "go test ./...", CommandAgeSec: 42,
		RecentFiles: []string{"a.go", "b.go"}, ToolCalls: 7})
	if p == nil || p.Command != "go test ./..." || p.CommandAgeSec == nil || *p.CommandAgeSec != 42 ||
		len(p.RecentFiles) != 2 || p.ToolCalls == nil || *p.ToolCalls != 7 {
		t.Fatalf("projection: %+v", p)
	}
	p = watchProgress(observe.LiveProgress{InDispatch: true, RecentFiles: []string{"a.go"}})
	if p == nil || p.Command != "" || p.CommandAgeSec != nil || p.ToolCalls != nil {
		t.Fatalf("files-only projection: %+v", p)
	}
}

// TestLastSeqAndOwnedCursorPastHistoryCap (repo review 2026-10-16 B-5):
// a WI with more than HistoryCap events still reports its true last seq,
// and an owned subscription still finds its newest decision event.
func TestLastSeqAndOwnedCursorPastHistoryCap(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.client.Origin = &cli.SubmitOrigin{ChatID: "chat_long", Owner: "alice"}
	wi := "wi_" + "longlog"
	td.submitClaim(t, wi)
	td.waitMonitored(t, wi)
	forgetSub(td, "chat_long", wi)
	ev := td.obs.Events()
	for i := 0; i < observe.HistoryCap+10; i++ {
		if err := ev.Append(wi, "t_event", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := ev.Append(wi, "step_dead", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	n := ev.LastSeq(wi)
	if got := td.lastSeq(wi); got != n || n <= observe.HistoryCap {
		t.Fatalf("lastSeq = %d, want %d (> HistoryCap)", got, n)
	}
	added, err := td.client.ChatSubscribeOwned("chat_long", "")
	if err != nil || len(added) != 1 || added[0].Cursor != n-1 {
		t.Fatalf("owned subscription %+v err=%v, want the cursor just before step_dead (%d)", added, err, n-1)
	}
}

// polyforge#199: an owner-routed subscription shares the origin chat's
// delivery record (answered with it, acked into it) and is passive while
// the origin chat is live; when the origin chat's watcher is gone, only
// the lowest-id live chat sharing the record is the fallback; a daemon
// start does not hand a WI to a fallback before the origin chat had a
// chatLiveFor to reconnect; an explicit subscribe makes the chat a wake
// target with a record of its own; without an origin subscription (or
// origin chat) every chat wakes from its own record.
func TestSubStoreWakesTheOriginChatElseOneFallback(t *testing.T) {
	s, err := newSubStore(filepath.Join(t.TempDir(), chatSubsFile))
	if err != nil {
		t.Fatal(err)
	}
	now := s.born
	s.now = func() time.Time { return now }
	get := func(chat, wi string) protocol.ChatSub {
		t.Helper()
		for _, sub := range s.List(chat) {
			if sub.WI == wi {
				return sub
			}
		}
		t.Fatalf("%s holds no subscription to %s: %+v", chat, wi, s.List(chat))
		return protocol.ChatSub{}
	}
	passive := func(chat string) bool { t.Helper(); return get(chat, "wi_1").Passive }
	if _, err := s.Subscribe("chat_o", "wi_1", "", 5); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"chat_b", "chat_c"} {
		if _, err := s.SubscribeNew(c, "wi_1", "", "chat_o", 0); err != nil {
			t.Fatal(err)
		}
	}
	// just after a daemon start nobody was seen yet: the origin chat
	// still counts as live
	if passive("chat_o") || !passive("chat_b") || !passive("chat_c") {
		t.Fatal("after a start: only the origin chat wakes")
	}
	// the origin chat delivers: the record is shared
	if err := s.Ack("chat_o", protocol.ChatAck{WI: "wi_1", Cursor: 7, Notified: []string{"needs_input:q-1"}}); err != nil {
		t.Fatal(err)
	}
	if b := get("chat_b", "wi_1"); b.Cursor != 7 || len(b.Notified) != 1 {
		t.Fatalf("an owner-routed subscription reads the origin record: %+v", b)
	}
	s.Seen("chat_o")
	s.Seen("chat_b")
	s.Seen("chat_c")
	now = now.Add(chatLiveFor / 2)
	s.Seen("chat_b")
	s.Seen("chat_c")
	if passive("chat_o") || !passive("chat_b") || !passive("chat_c") {
		t.Fatal("origin live: only the origin chat wakes")
	}
	// the origin chat's watcher stopped polling a chatLiveFor ago
	now = now.Add(chatLiveFor / 2)
	s.Seen("chat_b")
	s.Seen("chat_c")
	if passive("chat_b") || !passive("chat_c") {
		t.Fatal("origin gone: the lowest-id live chat is the one fallback")
	}
	// the fallback delivers into the shared record: the origin chat
	// sees it delivered too
	if err := s.Ack("chat_b", protocol.ChatAck{WI: "wi_1", Cursor: 9, Notified: []string{"needs_input:q-1"}}); err != nil {
		t.Fatal(err)
	}
	if o := get("chat_o", "wi_1"); o.Cursor != 9 {
		t.Fatalf("a fallback ack lands in the origin record: %+v", o)
	}
	// the fallback goes too: the next live chat takes over
	now = now.Add(chatLiveFor)
	s.Seen("chat_c")
	if passive("chat_c") {
		t.Fatal("the only live chat must be the fallback")
	}
	// the origin chat is back: the others are passive again
	s.Seen("chat_o")
	if !passive("chat_c") {
		t.Fatal("a returning origin chat is the wake target again")
	}
	// an explicit subscribe makes chat_c a wake target of its own, with
	// a record of its own starting like a new subscription
	if ch, err := s.Subscribe("chat_c", "wi_1", "", 11); err != nil || !ch {
		t.Fatalf("explicit subscribe: %v %v", ch, err)
	}
	if c := get("chat_c", "wi_1"); c.Passive || c.Cursor != 11 || len(c.Notified) != 0 {
		t.Fatalf("an explicitly subscribed chat is not passive and owns its record: %+v", c)
	}
	if o := get("chat_o", "wi_1"); o.Cursor != 9 {
		t.Fatalf("the origin record is untouched: %+v", o)
	}
	// the origin chat unsubscribed (muted) the WI: the record is gone,
	// chat_b wakes from its own
	if _, err := s.Unsubscribe("chat_o", "wi_1"); err != nil {
		t.Fatal(err)
	}
	if b := get("chat_b", "wi_1"); b.Passive || b.Cursor != 0 {
		t.Fatalf("without an origin subscription the chat uses its own record and wakes: %+v", b)
	}
	// a WI without an origin chat wakes every subscribed chat
	if _, err := s.SubscribeNew("chat_b", "wi_2", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if get("chat_b", "wi_2").Passive {
		t.Fatal("an origin-less WI must not be passive")
	}
}

// The watch snapshot's execution carries the declared step structure of
// the capsule's pinned workflow (polyforge#213): frontmatter order (not
// the alphabetical order of the steps map), on_fail the gate's rewind
// target or an explicit null, no other keys. A missing or unparseable
// workflow.md omits the field and leaves the rest of the snapshot as is.
func TestWatchSnapshotDeclaredSteps(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	td.hub.slugs = map[string]string{"wi_gone": "proj#9"}
	td.hub.mu.Unlock()
	td.seedPendingQuestion(t, "wi_gone", "q-9", "zeta", "x")
	capDir := filepath.Join(td.cfg.Mapping.Workspaces()[0], "capsules", api.PathName("proj#9", "wi_gone"))
	bundleDir := filepath.Join(capDir, capsule.DirWorkflows, "flow")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	idn, _ := json.Marshal(capsule.Identity{WorkItemID: "wi_gone", BundleName: "flow"})
	if err := os.WriteFile(filepath.Join(capDir, capsule.CapsuleJSON), idn, 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(bundleDir, "workflow.md")
	snapshotJSON := func() map[string]any {
		t.Helper()
		raw, err := json.Marshal(td.watchSnapshot("wi_gone", nil, false, 0))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		ex, ok := m["execution"].(map[string]any)
		if !ok {
			t.Fatalf("no execution: %s", raw)
		}
		return ex
	}

	for name, body := range map[string]string{
		"missing":     "",
		"unparseable": "---\nsteps: [\n---\n",
	} {
		if body == "" {
			_ = os.Remove(wfPath)
		} else if err := os.WriteFile(wfPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ex := snapshotJSON()
		if _, has := ex["declared_steps"]; has {
			t.Fatalf("%s workflow.md: declared_steps must be omitted: %v", name, ex)
		}
		if ex["current_step"] != "zeta" || ex["pending_question"] == nil {
			t.Fatalf("%s workflow.md: rest of the snapshot changed: %v", name, ex)
		}
	}

	wf := "---\nname: flow\nsteps:\n" +
		"  - { id: zeta }\n" +
		"  - { id: alpha }\n" +
		"  - { id: mid, context_policy: fresh, gate: true, on_fail: { target: alpha } }\n" +
		"---\n\n# flow\n"
	if err := os.WriteFile(wfPath, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := snapshotJSON()
	got, err := json.Marshal(ex["declared_steps"])
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"zeta","on_fail":null},{"id":"alpha","on_fail":null},{"id":"mid","on_fail":"alpha"}]`
	if string(got) != want {
		t.Fatalf("declared_steps = %s, want %s", got, want)
	}
	for _, e := range ex["declared_steps"].([]any) {
		if len(e.(map[string]any)) != 2 {
			t.Fatalf("an entry carries keys beyond id and on_fail: %v", e)
		}
	}
	if ex["current_step"] != "zeta" || ex["pending_question"] == nil {
		t.Fatalf("rest of the snapshot changed: %v", ex)
	}
}

// ---------------------------------------------------------------------------
// polyforge#224 — the demand-gated summarize lane on the watch route.

// TestW224DemandHelperProcess is not a test: re-executed as a child, it
// fetches one demand-marked watch snapshot over the socket named in
// its environment and prints the HTTP status and body. The child
// carries whatever step markers the test hands it, so the kernel's
// SO_PEERCRED names exactly the peer the demand gate judges — the #122
// seam.
func TestW224DemandHelperProcess(t *testing.T) {
	sock := os.Getenv("W224_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	wi := os.Getenv("W224_WI")
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}, Timeout: 30 * time.Second}
	resp, err := c.Get("http://pf/jobs/" + url.PathEscape(wi) + "/watch?after=0&demand=1")
	if err != nil {
		fmt.Printf("W224-ERR %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("W224-STATUS %d %s\n", resp.StatusCode, body)
}

// w224DecodeSnap decodes one child answer body.
func w224DecodeSnap(t *testing.T, body string) protocol.WatchSnapshot {
	t.Helper()
	var s protocol.WatchSnapshot
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("the snapshot JSON is undecodable: %v (%s)", err, body)
	}
	return s
}

// An eligible human-side marked request starts exactly ONE background
// summarize call: the plain (unmarked) fetches before it start nothing,
// the marked request arms one call whose plain line lands in the next
// snapshot with its age (no error, no capped, the snapshot whole), and
// a marked request within the 60 s interval starts no second call.
// (The kept-summary-alongside-failure and the success-clears-error arms
// need a second call on the same WI past the interval — pinned in the
// lane's own tests; the e2e suite exercises the walk end to end.)
func TestW224MarkedRequestArmsExactlyOneCall(t *testing.T) {
	r := newW224Rig(t, []string{"pi:w224/m"})
	// unmarked fetches never arm the lane
	for i := 0; i < 3; i++ {
		if s := w224Snap(t, r.td, r.wi); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
			t.Fatalf("an unmarked fetch armed the summarize lane: %+v", s)
		}
	}
	if r.disp.n() != 0 {
		t.Fatalf("unmarked fetches started summarize calls: %v", r.disp.calls)
	}
	// the human-side marked request (a real child, no step markers)
	// arms exactly one background call
	began := time.Now()
	code, body := w224DemandChild(t, r.td, r.wi)
	if el := time.Since(began); el > 5*time.Second {
		t.Fatalf("the marked request waited %v on the call (the route must answer immediately)", el)
	}
	if code != http.StatusOK {
		t.Fatalf("the marked snapshot request must answer as a read: %d %s", code, body)
	}
	if r.disp.n() != 1 {
		t.Fatalf("the marked request must start exactly one call: %v", r.disp.calls)
	}
	// the line lands with its age, no error, no capped; the snapshot stays whole
	s := w224AwaitSummary(t, r.td, r.wi, 5*time.Second)
	if s.Summary == nil || *s.Summary != "w224: the agent is working" {
		t.Fatalf("the summarize line did not land: %+v", s)
	}
	if s.SummaryAgeSec == nil || *s.SummaryAgeSec < 0 {
		t.Fatalf("the held summary must carry its age: %+v", s)
	}
	if s.SummaryError != nil || s.SummaryCapped != nil {
		t.Fatalf("a successful call holds no error and no capped: %+v", s)
	}
	if !s.Known || s.Job == nil {
		t.Fatalf("the summarize lane broke the snapshot: %+v", s)
	}
	// a marked request within the 60 s interval starts no second call
	// (the transcript has grown meanwhile — the interval blocks alone)
	r.grow(t, "more work")
	if code, body := w224DemandChild(t, r.td, r.wi); code != http.StatusOK {
		t.Fatalf("the second marked request must answer as a read: %d %s", code, body)
	}
	if r.disp.n() != 1 {
		t.Fatalf("a second call fired within 60 s of the first: %v", r.disp.calls)
	}
}

// A parked WI makes zero calls (assumption A11): the demand gates run
// only on a LIVE RUNNING STEP DISPATCH — the current step in_progress
// with a live step process, not parked on a question. A needs_input WI
// (parked on a question), a between-steps WI (the current step pending)
// and a finished one (done) all still carry a resolvable spawn-journal
// entry and a grown transcript, and the lane is FRESH (the restart
// shape: the zero position makes any size a growth) — yet the marked
// request starts nothing: the gate alone blocks it. (The paused shape is
// the gate's other half — a paused job is not StateRunning and holds no
// agent — the same posture pendingQuestions reads.)
func TestW224ParkedWIMakesZeroCalls(t *testing.T) {
	for name, mut := range map[string]func(t *testing.T, r *w224Rig){
		"needs_input (parked on a question)": func(t *testing.T, r *w224Rig) {
			w224WriteExecution(t, r.capDir, capsule.StepInProgress, "w224-q")
		},
		"between steps (current step pending)": func(t *testing.T, r *w224Rig) {
			w224WriteExecution(t, r.capDir, capsule.StepPending, "")
		},
		"finished (current step done)": func(t *testing.T, r *w224Rig) {
			w224WriteExecution(t, r.capDir, capsule.StepDone, "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newW224Rig(t, []string{"pi:w224/m"})
			mut(t, r)
			// the transcript has grown past everything a fresh lane ever
			// recorded (the restart shape: position 0) — the gate alone
			// must block the call
			r.grow(t, "parked growth")
			code, body := w224DemandChild(t, r.td, r.wi)
			if code != http.StatusOK {
				t.Fatalf("the marked request on a parked WI must answer as a read: %d %s", code, body)
			}
			if s := w224DecodeSnap(t, body); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
				t.Fatalf("a parked WI armed the summarize lane: %+v", s)
			}
			time.Sleep(200 * time.Millisecond)
			if r.disp.n() != 0 {
				t.Fatalf("a parked WI made summarize calls: %v", r.disp.calls)
			}
			if s := w224Snap(t, r.td, r.wi); s.Summary != nil || s.SummaryError != nil {
				t.Fatalf("a parked WI landed a summarize fact: %+v", s)
			}
		})
	}
}

// A step agent's demand flag authorizes nothing: a child carrying the
// step markers (PF_WI_ID + PF_DAEMON_ID of THIS daemon — a step process
// tree of it) fetches the snapshot with the marker set; the marker is
// ignored (no demand recorded, no call started, no error returned — the
// route stays a read any caller may fetch).
func TestW224StepPeerDemandIgnored(t *testing.T) {
	r := newW224Rig(t, []string{"pi:w224/m"})
	code, body := w224DemandChild(t, r.td, r.wi,
		"PF_WI_ID="+r.wi, "PF_DAEMON_ID="+r.td.cfg.MachineID)
	if code != http.StatusOK {
		t.Fatalf("a step-peer's marked snapshot request must answer as a read: %d %s", code, body)
	}
	if s := w224DecodeSnap(t, body); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
		t.Fatalf("a step-peer's demand=1 started a summarize call: %+v", s)
	}
	if r.disp.n() != 0 {
		t.Fatalf("a step-peer's demand=1 recorded demand (a call started): %v", r.disp.calls)
	}
	time.Sleep(200 * time.Millisecond)
	if s := w224Snap(t, r.td, r.wi); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("a step-peer's demand=1 armed the lane: %+v", s)
	}
}

// A call in flight does not stall the snapshot: with the model call
// blocked in the fake, the marked request itself and every plain fetch
// answer immediately with the pre-call state; the call lands once
// released (the landing is the flight's proof).
func TestW224InFlightCallDoesNotStallTheSnapshot(t *testing.T) {
	r := newW224Rig(t, []string{"pi:w224/m"})
	r.disp.block = make(chan struct{})
	began := time.Now()
	code, body := w224DemandChild(t, r.td, r.wi)
	if el := time.Since(began); el > 5*time.Second {
		t.Fatalf("the marked request waited %v on the in-flight call", el)
	}
	if code != http.StatusOK {
		t.Fatalf("the marked request must answer as a read: %d %s", code, body)
	}
	if s := w224DecodeSnap(t, body); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("the marked request answered the in-flight call's state, not the pre-call state: %+v", s)
	}
	// plain fetches inside the flight: each answers immediately, each
	// carries the pre-call state
	for i := 0; i < 2; i++ {
		began = time.Now()
		s := w224Snap(t, r.td, r.wi)
		if el := time.Since(began); el > 5*time.Second {
			t.Fatalf("a snapshot fetch waited %v on the in-flight call", el)
		}
		if s.Summary != nil || s.SummaryError != nil {
			t.Fatalf("a fetch inside the flight carried the in-flight call's state: %+v", s)
		}
	}
	if r.disp.n() != 1 {
		t.Fatalf("the flight's call is missing: %v", r.disp.calls)
	}
	close(r.disp.block)
	s := w224AwaitSummary(t, r.td, r.wi, 5*time.Second)
	if s.Summary == nil || *s.Summary != "w224: the agent is working" {
		t.Fatalf("the in-flight call never landed: %+v", s)
	}
}

// A failed call never breaks the snapshot: the raw error text of the
// latest failed call rides summary_error verbatim (never faked), no
// summary is held (none finished), and the snapshot stays whole.
func TestW224FailedCallLandsRawError(t *testing.T) {
	r := newW224Rig(t, []string{"pi:w224/bad"})
	r.disp.answer = func(candidate string) (string, error) {
		return "", errors.New("harness exploded: model refused (exit 1)")
	}
	if code, body := w224DemandChild(t, r.td, r.wi); code != http.StatusOK {
		t.Fatalf("the marked request must answer as a read: %d %s", code, body)
	}
	s := w224AwaitSummary(t, r.td, r.wi, 5*time.Second)
	if s.Summary != nil {
		t.Fatalf("a summary is held for a failed call: %q", *s.Summary)
	}
	if s.SummaryError == nil || !strings.Contains(*s.SummaryError, "harness exploded: model refused (exit 1)") {
		t.Fatalf("summary_error must carry the RAW error text: %+v", s)
	}
	if !s.Known || s.Job == nil {
		t.Fatalf("the failed call broke the snapshot: %+v", s)
	}
}

// A project with no summarize key has the lane silently off: a marked
// request makes zero calls, no error appears in any face, and the four
// snapshot fields simply stay absent.
func TestW224UnconfiguredLaneIsSilentlyOff(t *testing.T) {
	r := newW224Rig(t, nil)
	code, body := w224DemandChild(t, r.td, r.wi)
	if code != http.StatusOK {
		t.Fatalf("the marked request under a keyless yaml must answer as a read: %d %s", code, body)
	}
	if s := w224DecodeSnap(t, body); s.Summary != nil || s.SummaryError != nil || s.SummaryCapped != nil {
		t.Fatalf("the unconfigured lane is not silently off: %+v", s)
	}
	time.Sleep(200 * time.Millisecond)
	if r.disp.n() != 0 {
		t.Fatalf("the unconfigured lane made a call: %v", r.disp.calls)
	}
	if s := w224Snap(t, r.td, r.wi); s.Summary != nil || s.SummaryError != nil {
		t.Fatalf("the unconfigured lane made an error: %+v", s)
	}
}

// The lane's whole state is memory-only: no watchers directory, no
// state file — nothing appears under the workspace (the old lane's
// <workspace>/.polyforge/watchers/<wi>.state shape does not return).
func TestW224NoStateFileOrWatchersDir(t *testing.T) {
	r := newW224Rig(t, []string{"pi:w224/m"})
	code, body := w224DemandChild(t, r.td, r.wi)
	if code != http.StatusOK {
		t.Fatalf("the marked request must answer as a read: %d %s", code, body)
	}
	w224AwaitSummary(t, r.td, r.wi, 5*time.Second)
	ws, err := r.td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".polyforge")); !os.IsNotExist(err) {
		t.Fatalf("a watchers state directory exists under the workspace: %v", err)
	}
	var hit string
	_ = filepath.WalkDir(ws, func(path string, e os.DirEntry, err error) error {
		if hit != "" || err != nil || e.IsDir() {
			return nil
		}
		if strings.HasSuffix(e.Name(), ".state") {
			hit = path
		}
		return nil
	})
	if hit != "" {
		t.Fatalf("a summarize state file exists under the workspace: %s", hit)
	}
}

// TestSubStoreTagWorkspace — the chat's workspace tag is
// recorded on the chatDoc (even untagged — an untagged chat is a
// recorded chat), refreshed by a later tag, and survives a restart.
func TestSubStoreTagWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), chatSubsFile)
	s, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TagWorkspace("chat_t", "/ws/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.TagWorkspace("chat_t", "/ws/b"); err != nil { // a re-init refreshes
		t.Fatal(err)
	}
	if err := s.TagWorkspace("chat_u", ""); err != nil { // untagged, still recorded
		t.Fatal(err)
	}
	// daemon restart: a brand-new store over the same file
	s2, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if tag := s2.WorkspaceOf("chat_t"); tag != "/ws/b" {
		t.Fatalf("refreshed tag must survive a restart: %q", tag)
	}
	if tag := s2.WorkspaceOf("chat_u"); tag != "" {
		t.Fatalf("untagged tag = %q, want \"\"", tag)
	}
	s2.mu.Lock()
	_, recorded := s2.doc.Chats["chat_u"]
	s2.mu.Unlock()
	if !recorded {
		t.Fatal("an untagged chat must be recorded in the tag store")
	}
	if tag := s2.WorkspaceOf("chat_never"); tag != "" {
		t.Fatalf("an unknown chat answers no tag: %q", tag)
	}
}

// TestSubStoreUnsubscribeRetainsRecord — the record a dropped
// subscription carried (cursor, delivered keys, done) is retained on
// the chat for a later adopter to inherit; a fresh subscribe deletes
// it (a fresh subscription starts fresh).
func TestSubStoreUnsubscribeRetainsRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), chatSubsFile)
	s, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe("chat_o", "wi_1", "proj#1", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack("chat_o", protocol.ChatAck{WI: "wi_1", Cursor: 7, Notified: []string{"needs_input:q-1", "needs_input:q-2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unsubscribe("chat_o", "wi_1"); err != nil {
		t.Fatal(err)
	}
	retained := func(store *subStore) *protocol.ChatSub {
		store.mu.Lock()
		defer store.mu.Unlock()
		c := store.doc.Chats["chat_o"]
		if c == nil {
			return nil
		}
		return c.Records["wi_1"]
	}
	rec := retained(s)
	if rec == nil || rec.Cursor != 7 || len(rec.Notified) != 2 || rec.Notified[0] != "needs_input:q-1" {
		t.Fatalf("the dropped record must be retained: %+v", rec)
	}
	// the retained record survives a daemon restart
	s2, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec = retained(s2); rec == nil || rec.Cursor != 7 || len(rec.Notified) != 2 {
		t.Fatalf("the retained record must survive a restart: %+v", rec)
	}
	// a fresh subscribe starts fresh: the retained record is deleted,
	// never resumed
	if _, err := s2.Subscribe("chat_o", "wi_1", "proj#1", 0); err != nil {
		t.Fatal(err)
	}
	if rec = retained(s2); rec != nil {
		t.Fatalf("a fresh subscription must drop the retained record: %+v", rec)
	}
	if subs := s2.List("chat_o"); len(subs) != 1 || subs[0].Cursor != 0 || len(subs[0].Notified) != 0 {
		t.Fatalf("fresh record: %+v", subs)
	}
}

// TestSubStoreAdoptSurgery — the adopt surgery unions the
// target's retained record into the adopter's subscription (nothing
// either side delivered replays), re-points every other owner-routed
// subscription at the adopter, leaves explicit subscribers untouched,
// consumes the retained record, and is idempotent — all persisted.
func TestSubStoreAdoptSurgery(t *testing.T) {
	path := filepath.Join(t.TempDir(), chatSubsFile)
	s, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// the target (origin) chat: delivered q-1 and q-2, then unsubscribed
	if _, err := s.Subscribe("chat_o", "wi_1", "proj#1", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack("chat_o", protocol.ChatAck{WI: "wi_1", Cursor: 7, Notified: []string{"needs_input:q-1", "needs_input:q-2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unsubscribe("chat_o", "wi_1"); err != nil {
		t.Fatal(err)
	}
	// the adopter: its own subscription, cursor 5, delivered q-2
	if _, err := s.Subscribe("chat_a", "wi_1", "proj#1", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack("chat_a", protocol.ChatAck{WI: "wi_1", Cursor: 5, Notified: []string{"needs_input:q-2"}}); err != nil {
		t.Fatal(err)
	}
	// one owner-routed subscription (cross-workspace or not) and one
	// explicit subscriber
	if _, err := s.SubscribeNew("chat_b", "wi_1", "proj#1", "chat_o", 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscribeNew("chat_x", "wi_1", "proj#1", "chat_o", 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe("chat_e", "wi_1", "proj#1", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Adopt("chat_a", "wi_1", "proj#1", "chat_o"); err != nil {
		t.Fatal(err)
	}
	sub := func(store *subStore, chat string) *protocol.ChatSub {
		store.mu.Lock()
		defer store.mu.Unlock()
		c := store.doc.Chats[chat]
		if c == nil {
			return nil
		}
		return c.Subs["wi_1"]
	}
	a := sub(s, "chat_a")
	if a == nil || a.OriginChat != "" || a.OwnerRouted {
		t.Fatalf("the adopter's subscription is a wake target of its own record: %+v", a)
	}
	if a.Cursor != 7 {
		t.Fatalf("cursor = the max of the two records (events either side consumed never replay): %d", a.Cursor)
	}
	if len(a.Notified) != 2 || a.Notified[0] != "needs_input:q-1" || a.Notified[1] != "needs_input:q-2" {
		t.Fatalf("notified = the union, deduped (nothing replays): %+v", a.Notified)
	}
	for _, chat := range []string{"chat_b", "chat_x"} {
		if o := sub(s, chat); o == nil || o.OriginChat != "chat_a" || !o.OwnerRouted {
			t.Fatalf("%s's owner-routed subscription must re-point at the adopter: %+v", chat, o)
		}
	}
	if e := sub(s, "chat_e"); e == nil || e.OriginChat != "" || e.OwnerRouted || e.Cursor != 2 || len(e.Notified) != 0 {
		t.Fatalf("an explicit subscriber stays a wake target of its own record, untouched: %+v", e)
	}
	s.mu.Lock()
	consumed := s.doc.Chats["chat_o"] == nil || s.doc.Chats["chat_o"].Records["wi_1"] == nil
	s.mu.Unlock()
	if !consumed {
		t.Fatal("the target's retained record must be consumed by the adopt")
	}
	// idempotent: re-running the surgery over the adopted state changes nothing
	if err := s.Adopt("chat_a", "wi_1", "proj#1", "chat_o"); err != nil {
		t.Fatal(err)
	}
	if a2 := sub(s, "chat_a"); a2.Cursor != 7 || len(a2.Notified) != 2 || a2.Notified[0] != "needs_input:q-1" {
		t.Fatalf("a re-run must change no state: %+v", a2)
	}
	// persisted: a brand-new store over the same file reads the same facts
	s2, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if a3 := sub(s2, "chat_a"); a3.Cursor != 7 || len(a3.Notified) != 2 || a3.OriginChat != "" {
		t.Fatalf("the adopt must persist: %+v", a3)
	}
	if o := sub(s2, "chat_b"); o == nil || o.OriginChat != "chat_a" {
		t.Fatalf("the re-route must persist: %+v", o)
	}
}

// TestSubStoreAdoptWithoutRecordAndMute — an adopter that holds
// no subscription gets one created (nothing to inherit, nothing of its
// own delivered), and its mute mark on the WI is cleared — a stronger
// act than the explicit subscribe it mirrors.
func TestSubStoreAdoptWithoutRecordAndMute(t *testing.T) {
	s, err := newSubStore(filepath.Join(t.TempDir(), chatSubsFile))
	if err != nil {
		t.Fatal(err)
	}
	// the adopter muted the WI (unsubscribed from an owner routing it
	// never held a subscription through)
	if _, err := s.Unsubscribe("chat_a", "wi_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Adopt("chat_a", "wi_1", "proj#1", ""); err != nil { // no origin record to inherit
		t.Fatal(err)
	}
	s.mu.Lock()
	c := s.doc.Chats["chat_a"]
	muted, sub := c.Muted["wi_1"], c.Subs["wi_1"]
	s.mu.Unlock()
	if muted {
		t.Fatal("adopt must clear the adopter's mute mark on the WI")
	}
	if sub == nil || sub.Cursor != 0 || len(sub.Notified) != 0 || sub.OriginChat != "" || sub.OwnerRouted || sub.Slug != "proj#1" {
		t.Fatalf("an adopter without a subscription gets one created, fresh: %+v", sub)
	}
}

// TestSubStoreScopedWakeAudience — with the WI's origin chat
// gone (unsubscribed or none) and its workspace holding a live
// subscribed chat, the decision wake is scoped: the eligible chat is a
// wake target of its own record with the Adoptable flag (the adopt
// hint), every other owner-routed subscriber is Passive, an explicit
// subscriber stays a wake target of its own record untouched, and a
// muted chat (no subscription) is excluded by construction. With the
// origin holding a subscription, or an empty eligible set, or after an
// adopt (the adopter IS the origin), today's semantics apply: no flag,
// and the scoped demotion never fires.
func TestSubStoreScopedWakeAudience(t *testing.T) {
	const wi = "wi_g14"
	s, err := newSubStore(filepath.Join(t.TempDir(), chatSubsFile))
	if err != nil {
		t.Fatal(err)
	}
	origin := "chat_o" // the job record's origin chat (the scope's fact)
	s.scope = wakeScope{
		originChat: func(string) string { return origin },
		workspace:  func(string) string { return "/ws/a" },
	}
	// the audience: chat_a (tagged /ws/a, explicit), chat_x (tagged
	// /ws/b, owner-routed to the origin), chat_u (untagged,
	// owner-routed), chat_m (tagged /ws/a, mutes the WI itself)
	if err := s.TagWorkspace("chat_a", "/ws/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.TagWorkspace("chat_x", "/ws/b"); err != nil {
		t.Fatal(err)
	}
	if err := s.TagWorkspace("chat_m", "/ws/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe("chat_a", wi, "proj#14", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscribeNew("chat_x", wi, "proj#14", origin, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscribeNew("chat_u", wi, "proj#14", origin, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe("chat_m", wi, "proj#14", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unsubscribe("chat_m", wi); err != nil { // its explicit wish: nothing, ever
		t.Fatal(err)
	}
	subOf := func(chat string) protocol.ChatSub {
		t.Helper()
		for _, sub := range s.List(chat) {
			if sub.WI == wi {
				return sub
			}
		}
		t.Fatalf("%s holds no subscription to %s", chat, wi)
		return protocol.ChatSub{}
	}

	// the origin unsubscribed: the scoped broadcast fires
	if _, err := s.Subscribe("chat_o", wi, "proj#14", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unsubscribe("chat_o", wi); err != nil {
		t.Fatal(err)
	}
	if a := subOf("chat_a"); !a.Adoptable || a.Passive {
		t.Fatalf("the eligible chat is a flagged wake target of its own record: %+v", a)
	}
	for _, chat := range []string{"chat_x", "chat_u"} {
		if sub := subOf(chat); !sub.Passive || sub.Adoptable {
			t.Fatalf("%s (owner-routed, outside the eligible set) must be Passive, unflagged: %+v", chat, sub)
		}
	}

	// an explicit subscriber outside the set (a cross-workspace one)
	// stays a wake target of its own record, untouched
	if _, err := s.Subscribe("chat_e", wi, "proj#14", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.TagWorkspace("chat_e", "/ws/b"); err != nil {
		t.Fatal(err)
	}
	if e := subOf("chat_e"); e.Passive || e.Adoptable {
		t.Fatalf("a cross-workspace explicit subscriber is untouched: %+v", e)
	}

	// the origin holds its subscription again: not gone — no flag, and
	// the owner-routed go back to record sharing (Passive under the
	// live origin)
	if _, err := s.Subscribe("chat_o", wi, "proj#14", 0); err != nil {
		t.Fatal(err)
	}
	if a := subOf("chat_a"); a.Adoptable {
		t.Fatalf("a live origin scopes nothing: %+v", a)
	}
	if x := subOf("chat_x"); !x.Passive || x.Adoptable {
		t.Fatalf("an owner-routed sub shares the live origin's record (Passive): %+v", x)
	}

	// an empty eligible set: today's owner-wide semantics — every
	// subscribed chat wakes its own record, no Passive, no flag
	if _, err := s.Unsubscribe("chat_o", wi); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.seen["chat_a"] = s.now().Add(-2 * chatLiveFor) // the eligible chat's watcher went silent
	s.mu.Unlock()
	for _, chat := range []string{"chat_a", "chat_x", "chat_u", "chat_e"} {
		if sub := subOf(chat); sub.Passive || sub.Adoptable {
			t.Fatalf("an empty eligible set is today's owner-wide fallback (no new output): %+v", sub)
		}
	}
	delete(s.seen, "chat_a") // silent chats read as live again (born-based)

	// after an adopt the condition is false by construction: the
	// adopter IS the origin and holds the subscription — no further
	// scoped broadcast, the re-pointed subscribers share its record
	if err := s.Adopt("chat_a", wi, "proj#14", origin); err != nil {
		t.Fatal(err)
	}
	adopter := "chat_a"
	s.scope = wakeScope{
		originChat: func(string) string { return adopter },
		workspace:  func(string) string { return "/ws/a" },
	}
	if a := subOf("chat_a"); a.Adoptable || a.Passive {
		t.Fatalf("the adopter is the origin now: no flag, no demotion: %+v", a)
	}
	if x := subOf("chat_x"); !x.Passive || x.Adoptable {
		t.Fatalf("a re-pointed subscriber is Passive relative to the live adopter: %+v", x)
	}
	if u := subOf("chat_u"); !u.Passive || u.Adoptable {
		t.Fatalf("a re-pointed cross-workspace subscriber is Passive to the adopter: %+v", u)
	}
}
