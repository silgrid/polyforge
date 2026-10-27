package daemon

// wake_scope_persistence_repro_test.go — the repro of the
// structural failure the review left open (its blocking finding 1,
// capsules/the internal project-14/artifacts/review.md): the scoped audience of a
// TERMINAL decision does not last. The gmi14 terminal test
// (TestTerminalDecisionStaysScopedAfterTheJobDrop) checks only the
// transient window right after the job drop, with the eligible chat live
// and no restart. These two tests hold the same scene and then walk the
// two ordinary events that collapse the audience later — the invariant the
// spec ("to no other chat") and docs/upgrade.md claim:
//
//   - a daemon restart (every upgrade / deploy / crash): the scope facts
//     the scoped wake reads live only in the runtime's in-memory `dropped`
//     map, so a fresh process resolves no workspace, the eligible set is
//     empty and the wake falls back to the owner-wide broadcast — every
//     subscribed chat of the owner wakes from its own record, and the
//     owner-routed subscribers outside the eligible set never acked (they
//     are Passive while the scope holds), so the old completed decision
//     prints again in each of them: the pi#122 blast radius, back later.
//   - the eligible chats going quiet: the audience is recomputed per poll,
//     so once every eligible same-workspace chat is past chatLiveFor
//     without a poll, the eligible set is empty with no restart at all —
//     the same owner-wide fallback fires even though the eligible chat
//     already delivered AND acked the decision.
//
// Both are authored red on the branch (the tree these tests target):
// each fails exactly the way the report describes — the terminal decision
// line reaching a cross-workspace chat, an untagged chat and a chat that
// muted the WI itself — and turns green only on the delivered behavior.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// gmi15RestartDaemon shuts the scene's daemon down and starts a fresh one
// over the SAME socket, data dir, mapping, hub and spawner — the
// in-process form of an operator restart (every upgrade / deploy) or a
// crash recovery: the subscriptions, delivery records, workspace tags and
// mute marks persist in the data dir, while everything the old process
// held only in memory (the runtime's dropped-scope facts, the watcher
// liveness) starts empty. Run returning releases the data-dir flock (the
// single-instance guard), so the new daemon binds only after the old one
// fully exited.
func gmi15RestartDaemon(t *testing.T, td *testDaemon) *testDaemon {
	t.Helper()
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown the old daemon: %v", err)
	}
	select {
	case <-td.runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the old daemon never exited (its data-dir flock never released)")
	}
	cfg := Config{
		MachineID:     td.cfg.MachineID,
		SocketPath:    td.cfg.SocketPath,
		Mapping:       td.cfg.Mapping,
		DataDir:       td.cfg.DataDir,
		GitRemoteBase: td.cfg.GitRemoteBase,
		MaxConcurrent: 4,
		Hub:           td.hub,
		Spawner:       td.spawner,
		AgentStarter:  fakeEngineStarter(),
		ObservePoll:   10 * time.Millisecond,
		DrainInterval: 10 * time.Millisecond,
		DispatchTick:  10 * time.Millisecond,
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(context.Background()) }()
	ntd := &testDaemon{Daemon: d, hub: td.hub, spawner: td.spawner, sock: cfg.SocketPath, runDone: runDone}
	ntd.client = cli.NewDaemonClient(ntd.sock)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := ntd.client.Status(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the restarted daemon never served")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = d.Shutdown(context.Background(), DirectShutdownCause())
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
		}
	})
	return ntd
}

// gmi15TerminalScene arms the terminal-decision scene of the gmi14
// terminal test: the full pi#122 audience subscribed through owner routing
// (the eligible second wsA chat, a wsA chat that mutes the WI itself, the
// wsB chat and the untagged chat), the origin unsubscribed (or absent),
// the job wrapped — moved to DONE and dropped — and the eligible chat's
// watcher delivered AND acked the completed decision.
func gmi15TerminalScene(t *testing.T, originChat bool) *gmi14Scene {
	t.Helper()
	s := gmi14Setup(t, originChat)
	for _, chat := range []string{s.a2, s.a3, s.b1, s.untagged} {
		gmi14Own(t, s.td, chat)
	}
	gmi14Mute(t, s.td, s.a3, s.wi) // its explicit wish: nothing, ever
	if originChat {
		gmi14Mute(t, s.td, s.origin, s.wi)
	}
	// the terminal close: the job is sealed, moved to DONE and dropped —
	// the decision it carries is completed (wrapped).
	if _, err := s.td.client.Wrap(s.wi, "wrap"); err != nil {
		t.Fatalf("wrap %s: %v", s.wi, err)
	}
	waitGone(t, s.td, s.wi)
	if _, held := s.td.rt.Info(s.wi); held {
		t.Fatalf("the wrapped close must drop the job (%s)", s.wi)
	}
	// the eligible same-workspace chat delivered the decision — its
	// watcher printed it and acked the record (notified + done).
	if out := gmi14RunChat(t, s.td, s.a2, 5*time.Second); !strings.Contains(out, "completed") {
		t.Fatalf("the WI's workspace's live subscribed chat must deliver the terminal decision: %q", out)
	}
	return s
}

// gmi15AssertNoLeak asserts the invariant both tests exist for: the
// terminal decision the eligible chat already delivered and acked reaches
// NO other chat — not a cross-workspace one, not an untagged one, not one
// that muted the WI itself.
func gmi15AssertNoLeak(t *testing.T, s *gmi14Scene) {
	t.Helper()
	for _, c := range []struct{ chat, why string }{
		{s.b1, "a cross-workspace chat"},
		{s.untagged, "an untagged chat"},
		{s.a3, "a chat that muted the WI itself"},
	} {
		if out := gmi14RunChat(t, s.td, c.chat, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
			t.Fatalf("REPORTED pi#122 BLAST RADIUS: the terminal decision reached %s after the eligible chat delivered and acked it: %q", c.why, out)
		}
	}
}

// TestTerminalScopeSurvivesDaemonRestart — path (a) of the report:
// after a daemon restart (an upgrade, a deploy, a crash — the
// crash-equals-shutdown rule) the terminal decision the eligible chat
// already delivered and acked must NOT go owner-wide: the cross-workspace,
// untagged and self-muted chats print nothing. On the tree the
// restart wipes the scope facts (in-memory only), so the scoped wake
// collapses to the owner-wide broadcast and every one of them prints the
// old completed decision.
func TestTerminalScopeSurvivesDaemonRestart(t *testing.T) {
	for _, originChat := range []bool{true, false} {
		s := gmi15TerminalScene(t, originChat)
		// the restart itself: a fresh process over the same data dir —
		// the persisted subscriptions and records survive it, the
		// in-memory scope facts do not.
		s.td = gmi15RestartDaemon(t, s.td)
		gmi15AssertNoLeak(t, s)
	}
}

// TestTerminalScopeSurvivesEligibleQuiet — path (b) of the report:
// once every eligible same-workspace chat has gone longer than chatLiveFor
// without a poll (its session closed), with NO restart at all, the
// terminal decision it already delivered and acked must NOT go owner-wide.
// On the tree the audience is recomputed per poll, so the quiet
// eligible chat empties the eligible set and the owner-wide broadcast
// fires.
func TestTerminalScopeSurvivesEligibleQuiet(t *testing.T) {
	for _, originChat := range []bool{true, false} {
		s := gmi15TerminalScene(t, originChat)
		// the eligible chat's watcher went silent: its last poll ages
		// past chatLiveFor (its session closed — no poll comes again).
		s.td.subs.mu.Lock()
		s.td.subs.seen[s.a2] = s.td.subs.now().Add(-2 * chatLiveFor)
		s.td.subs.mu.Unlock()
		gmi15AssertNoLeak(t, s)
	}
}
