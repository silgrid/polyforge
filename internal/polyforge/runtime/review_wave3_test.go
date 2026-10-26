package runtime

// review_wave3_test.go — runtime regressions for the 2026-10-16 repo
// review.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// B-7: the terminal close of a completed workflow must seal-push the
// capsule like every other terminal close. The driver does no git; the
// terminal execution.json (and the transcript tail) reach the hub only
// through the observe flush — which may have failed or timed out — so
// skipping the seal push let SessionEnd(wrapped) land on a chain missing
// the final state.
func TestTerminalWrapSealPushesTheFinalState(t *testing.T) {
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_w3_sealpush"
	j := seedJob(r, wi)
	bare := initGitCapsule(t, j)
	hub.hold(wi, j.sessionID, api.StatusRunning)
	execDir := filepath.Join(j.capsuleDir, "runtime-state")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]string{"workflow_status": capsule.WorkflowStatusCompleted})
	if err := os.WriteFile(filepath.Join(execDir, "execution.json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	j.agent = &recordingHandle{}
	j.agentPID = 0
	j.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTFinished || msg.Reason != api.StatusWrapped {
		t.Fatalf("want RTFinished(wrapped), got %+v", msg)
	}
	out, err := exec.Command("git", "--git-dir", bare, "show", "refs/heads/main:runtime-state/execution.json").CombinedOutput()
	if err != nil || !strings.Contains(string(out), capsule.WorkflowStatusCompleted) {
		t.Fatalf("hub chain lacks the terminal execution.json: %v: %s", err, out)
	}
}

// B-8 (S part): the task-repo clone runs on the claim lane;
// it must never wait on an interactive credential prompt.
func TestTaskRepoCloneIsNonInteractive(t *testing.T) {
	bin := t.TempDir()
	envOut := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\nenv > " + envOut + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	os.Unsetenv("GIT_TERMINAL_PROMPT")
	_ = gitClone(context.Background(), "https://example.invalid/r.git", filepath.Join(t.TempDir(), "r"))
	raw, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_HTTP_LOW_SPEED_TIME=60"} {
		if !strings.Contains(string(raw), want+"\n") {
			t.Fatalf("clone env lacks %s", want)
		}
	}
}

// B-6 end to end: after the restart drops the record of a WI the hub
// holds blocked under another session, the owner's resume (hub back to
// queued) re-claims it through the ordinary Start — not refused as
// "already active".
func TestBlockedRecordDroppedAtRestartThenResumeReclaims(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	const wi = "wi_w3_blkres"
	hub.items = map[string]*api.WorkItem{wi: {ID: wi, Slug: "proj#7", Seq: 7, ProjectName: "proj"}}
	hub.status[wi] = api.StatusBlocked
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	rec := matrixRecord(r, wi, 0, "")
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStateFile(t, r, []*jobRecord{rec})
	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	hub.status[wi] = api.StatusQueued // pf resume
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("resume re-claim refused: %v", err)
	}
	defer terminateJobAgent(r, wi)
	if m := recvRT(t, toOB); m.Kind != protocol.RTStarted {
		t.Fatalf("expected RTStarted, got %+v", m)
	}
}
