package runtime

// adopt_origin_test.go — (`pf watch adopt <wi>`): the runtime half
// of the adopt, AdoptOrigin — the persisted job record's origin chat is
// rewritten to the adopter (the same field a resume rewrites), a record
// with no origin gains one, Origin.Owner is untouched, and the rewrite
// lands in state.json.

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAdoptOriginRewritesTheOriginChat(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	base := t.TempDir()
	r.cfg.GitRemoteBase = base
	wi := "wi_adopt"
	if out, err := run(t, "git", "init", "--bare", filepath.Join(base, wi+".git")); err != nil {
		t.Fatalf("bare init: %s", out)
	}
	if err := r.Start(context.Background(), wi, StartOpts{Origin: &Origin{Kind: OriginChat, ChatID: "chat_o", Owner: "usr_1"}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	recvRT(t, toOB)
	if err := r.AdoptOrigin(wi, "chat_a"); err != nil {
		t.Fatalf("AdoptOrigin: %v", err)
	}
	ji, ok := r.Info(wi)
	if !ok || ji.Origin == nil {
		t.Fatalf("job origin after the adopt: %+v", ji.Origin)
	}
	if ji.Origin.ChatID != "chat_a" || ji.Origin.Kind != OriginChat {
		t.Fatalf("the origin chat field must be rewritten to the adopter: %+v", ji.Origin)
	}
	if ji.Origin.Owner != "usr_1" {
		t.Fatalf("Origin.Owner is untouched (owner routing keys on the owner, never the chat): %+v", ji.Origin)
	}
	st, err := loadState(r.statePath)
	if err != nil || len(st.Jobs) != 1 || st.Jobs[0].Origin == nil || st.Jobs[0].Origin.ChatID != "chat_a" || st.Jobs[0].Origin.Owner != "usr_1" {
		t.Fatalf("the rewrite must persist to state.json: %v %+v", err, st.Jobs[0].Origin)
	}
}

func TestAdoptOriginCreatesAnAbsentOrigin(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	base := t.TempDir()
	r.cfg.GitRemoteBase = base
	wi := "wi_adopt2"
	if out, err := run(t, "git", "init", "--bare", filepath.Join(base, wi+".git")); err != nil {
		t.Fatalf("bare init: %s", out)
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil { // no origin recorded
		t.Fatalf("Start: %v", err)
	}
	recvRT(t, toOB)
	if err := r.AdoptOrigin(wi, "chat_a"); err != nil {
		t.Fatalf("AdoptOrigin: %v", err)
	}
	ji, ok := r.Info(wi)
	if !ok || ji.Origin == nil || ji.Origin.Kind != OriginChat || ji.Origin.ChatID != "chat_a" {
		t.Fatalf("a record with no origin gains one (kind chat): %+v", ji.Origin)
	}
	if err := r.AdoptOrigin("wi_nolocaljob", "chat_a"); err == nil {
		t.Fatal("a work item with no local job must refuse, not mint a record")
	}
}
