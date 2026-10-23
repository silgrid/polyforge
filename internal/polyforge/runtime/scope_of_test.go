package runtime

// scope_of_test.go — ScopeOf, the wake-target scope read the
// daemon's subStore resolvers use. Both facts — the origin chat id and
// the project — must survive the drop a terminal close performs
// (stopOp moves the job to DONE and dropJob drops it): the terminal
// decision is exactly the one that fires the scoped wake, so a scope
// that dies with the job would fall back to the owner-wide broadcast
// (the pi#122 blast radius).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestScopeOfSurvivesTheTerminalDrop(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	base := t.TempDir()
	r.cfg.GitRemoteBase = base
	wi := "wi_scope"
	if out, err := run(t, "git", "init", "--bare", filepath.Join(base, wi+".git")); err != nil {
		t.Fatalf("bare init: %s", out)
	}
	if err := r.Start(context.Background(), wi, StartOpts{
		Origin: &Origin{Kind: OriginChat, ChatID: "chat_o", Owner: "usr_1"},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	recvRT(t, toOB)
	liveOrigin, project, ok := r.ScopeOf(wi)
	ji, held := r.Info(wi)
	if !ok || !held || ji.State != StateRunning {
		t.Fatalf("a held job answers its scope from the live record: %+v", ji)
	}
	if liveOrigin != "chat_o" || project != ji.Project || project == "" {
		t.Fatalf("the live record's scope: origin %q project %q", liveOrigin, project)
	}
	// the terminal close: stopOp seals, moves the job to DONE and drops
	// it — the scope facts must survive the drop.
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, held := r.Info(wi); held {
		t.Fatal("the wrapped close must drop the job")
	}
	origin, project2, ok := r.ScopeOf(wi)
	if !ok || origin != "chat_o" || project2 != project {
		t.Fatalf("the scope facts must survive the drop (origin %q project %q, want %q %q)",
			origin, project2, "chat_o", project)
	}
}

func TestScopeOfNeverHeldAnswersFalse(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	if _, _, ok := r.ScopeOf("wi_neverheld"); ok {
		t.Fatal("a work item this runtime never held has no scope to answer")
	}
}
