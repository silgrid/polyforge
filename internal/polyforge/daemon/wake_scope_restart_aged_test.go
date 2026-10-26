package daemon

// wake_scope_restart_aged_test.go — the restart-then-age variant
// of TestTerminalScopeSurvivesDaemonRestart (root-cause experiment
// E2). The restart test runs inside the fresh daemon's born grace, where
// every chat not seen yet counts as live; this one ages that grace and the
// eligible chat's liveness past chatLiveFor after the restart, so a fix
// that only restored the in-memory scope facts (and still relied on the
// eligible chat being live) cannot pass it: what the scoped audience
// delivered must be durable in chat-subs.json for the other owner-routed
// subscribers themselves.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestTerminalScopeSurvivesRestartThenQuiet(t *testing.T) {
	for _, originChat := range []bool{true, false} {
		s := gmi15TerminalScene(t, originChat)
		s.td = gmi15RestartDaemon(t, s.td)
		// the restarted daemon's born grace has run out and the eligible
		// chat never polled again (its session closed).
		s.td.subs.mu.Lock()
		s.td.subs.born = s.td.subs.now().Add(-2 * chatLiveFor)
		delete(s.td.subs.seen, s.a2)
		s.td.subs.mu.Unlock()
		gmi15AssertNoLeak(t, s)
	}
}

// TestSubStoreScopedAckLandsForPassiveOutsiders pins the store half: an
// eligible chat's ack under a scoped wake lands durably in the records of
// the owner-routed subscribers outside the eligible set (as in a shared
// record: cursor max, delivered keys set to the acker's — a key of the
// outsider's own record the acker does not hold is gone — done or), and
// only there — another eligible chat and
// an explicit subscriber keep their own records; an ack outside a scoped
// wake (the scope resolvers answer no workspace) lands in the acker's
// record alone.
func TestSubStoreScopedAckLandsForPassiveOutsiders(t *testing.T) {
	const wi = "wi_g15"
	path := filepath.Join(t.TempDir(), chatSubsFile)
	s, err := newSubStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ws := "/ws/a"
	s.scope = wakeScope{
		originChat: func(string) string { return "" }, // chat-less: the origin is gone
		workspace:  func(string) string { return ws },
	}
	must := func(_ bool, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for chat, tag := range map[string]string{"chat_a": "/ws/a", "chat_a2": "/ws/a", "chat_x": "/ws/b", "chat_e": "/ws/b"} {
		if err := s.TagWorkspace(chat, tag); err != nil {
			t.Fatal(err)
		}
	}
	must(s.SubscribeNew("chat_a", wi, "p#15", "", 0))
	must(s.SubscribeNew("chat_a2", wi, "p#15", "", 0))
	must(s.SubscribeNew("chat_x", wi, "p#15", "", 0))
	must(s.SubscribeNew("chat_u", wi, "p#15", "", 0)) // untagged
	must(s.Subscribe("chat_e", wi, "p#15", 0))        // explicit
	if err := s.Ack("chat_x", protocol.ChatAck{WI: wi, Cursor: 3, Notified: []string{"k_old"}}); err != nil {
		t.Fatal(err) // seeds a key of the outsider's own record (its watcher acked before the scope fired)
	}
	if err := s.Ack("chat_a", protocol.ChatAck{WI: wi, Cursor: 9, Notified: []string{"completed"}, Done: true}); err != nil {
		t.Fatal(err)
	}
	re, err := newSubStore(path) // what is durable, not what is in memory
	if err != nil {
		t.Fatal(err)
	}
	rec := func(chat string) *protocol.ChatSub { return re.doc.Chats[chat].Subs[wi] }
	if r := rec("chat_x"); r.Cursor != 9 || strings.Join(r.Notified, ",") != "completed" || !r.Done {
		t.Fatalf("the cross-workspace outsider must mirror the acker's delivered keys: %+v", r)
	}
	if r := rec("chat_u"); r.Cursor != 9 || strings.Join(r.Notified, ",") != "completed" || !r.Done {
		t.Fatalf("the untagged outsider must hold the delivery: %+v", r)
	}
	if r := rec("chat_a2"); r.Cursor != 0 || len(r.Notified) != 0 || r.Done {
		t.Fatalf("another eligible chat keeps its own record: %+v", r)
	}
	if r := rec("chat_e"); r.Cursor != 0 || len(r.Notified) != 0 || r.Done {
		t.Fatalf("an explicit subscriber keeps its own record: %+v", r)
	}

	// no scoped wake (no workspace resolves): the ack is the acker's alone
	ws = ""
	if err := s.Ack("chat_a2", protocol.ChatAck{WI: wi, Cursor: 12, Notified: []string{"later"}}); err != nil {
		t.Fatal(err)
	}
	if r := s.doc.Chats["chat_u"].Subs[wi]; r.Cursor != 9 || strings.Join(r.Notified, ",") != "completed" {
		t.Fatalf("an ack outside a scoped wake must not land elsewhere: %+v", r)
	}
}
