package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestOriginPersists(t *testing.T) {
	rec := &jobRecord{WorkItemID: "wi_o", State: string(StateRunning), Origin: &Origin{Kind: OriginChat, ChatID: "chat_1", Owner: "usr_1"}}
	raw, _ := json.Marshal(rec)
	var back jobRecord
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	got := jobFromRecord(&back).record().Origin
	if got == nil || *got != *rec.Origin {
		t.Fatalf("origin roundtrip: %+v", got)
	}
}

func TestStartRecordsItsOrigin(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	base := t.TempDir()
	r.cfg.GitRemoteBase = base
	wi := "wi_origin"
	if out, err := run(t, "git", "init", "--bare", filepath.Join(base, wi+".git")); err != nil {
		t.Fatalf("bare init: %s", out)
	}
	if err := r.Start(context.Background(), wi, StartOpts{Origin: &Origin{Kind: OriginChat, ChatID: "chat_9", Owner: "usr_1"}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	recvRT(t, toOB)
	ji, ok := r.Info(wi)
	if !ok || ji.Origin == nil || ji.Origin.ChatID != "chat_9" || ji.Origin.Owner != "usr_1" {
		t.Fatalf("job origin = %+v", ji.Origin)
	}
	st, err := loadState(r.statePath)
	if err != nil || len(st.Jobs) != 1 || st.Jobs[0].Origin == nil || st.Jobs[0].Origin.Kind != OriginChat {
		t.Fatalf("persisted origin: %v %+v", err, st)
	}
}
