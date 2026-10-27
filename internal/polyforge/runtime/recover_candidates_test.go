package runtime

// recover_candidates_test.go: the recovery lane's candidate composition
// (polyforge#33). A relaunch carries NO snapshot: every derivation —
// live job or restart-reconstructed — gets the dispatch-time composition
// closure, which reads the workspace yaml and bundle FRESH at every call,
// so an edit governs the next dispatch (the polyforge#79 "next claim only"
// freeze is overruled). The interrupted-dispatch archive signal rides the
// relaunch as the raw recovery cause.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// recordingStarter wraps the scripted engine starter and records every
// AgentStartInput a relaunch carries — the recovery lane's rehydration
// is observable at exactly this seam.
type recordingStarter struct {
	mu     sync.Mutex
	inputs []AgentStartInput
	base   AgentStartFunc
}

func (rs *recordingStarter) start(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
	rs.mu.Lock()
	rs.inputs = append(rs.inputs, in)
	rs.mu.Unlock()
	return rs.base(ctx, in)
}

func (rs *recordingStarter) last() AgentStartInput {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.inputs) == 0 {
		return AgentStartInput{}
	}
	return rs.inputs[len(rs.inputs)-1]
}

// A relaunch carries the live composition: the yaml edited mid-flight is
// what the re-dispatch composes, and the interrupted-dispatch archive
// signal rides along as the raw recovery cause.
func TestRecoveryRelaunchComposesLive(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	r := New(Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		MaxRecoveries: 2,
	}, hub, sp, toOB, supervise.NewQueue[protocol.OBMsg]())
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	rec := &recordingStarter{base: scriptedEngineStarter(sp)}
	r.cfg.AgentStarter = rec.start

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/code-a, pi:w79/code-b] }\n")
	if err := r.Start(context.Background(), "wi_w079rec", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_w079rec")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	if got := recvRT(t, toOB); got.Kind != protocol.RTStarted {
		t.Fatalf("expected the claim's RTStarted, got %+v", got)
	}
	first, err := rec.last().Compose()
	if err != nil || len(first["fix"]) != 2 || first["fix"][0] != "pi:w79/code-a" {
		t.Fatalf("the claim's composition %v (err %v), want [w79/code-a w79/code-b]", first["fix"], err)
	}

	// The mid-flight edit, then the death → recovery.
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/edited-a] }\n")
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("expected RTRecovered, got %+v", msg)
	}
	relaunch := rec.last()
	got, err := relaunch.Compose()
	if err != nil || len(got["fix"]) != 1 || got["fix"][0] != "pi:w79/edited-a" {
		t.Fatalf("relaunch composition for fix %v (err %v), want the edited [w79/edited-a] — composition is live", got["fix"], err)
	}
	if relaunch.InterruptedCause != protocol.RecoverDeath {
		t.Fatalf("relaunch InterruptedCause %q, want the raw recovery cause %q (the archive signal)", relaunch.InterruptedCause, protocol.RecoverDeath)
	}

	// An invalid yaml surfaces as the typed DAEMON-414 error (the driver
	// blocks on it); the agent-writable capsule.json is never a source.
	blob, _ := json.Marshal(map[string]any{"effective_candidates": map[string][]string{"fix": {"pi:evil/x"}}})
	if err := os.WriteFile(filepath.Join(j.capsuleDir, capsule.CapsuleJSON), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: [\n")
	if _, err := relaunch.Compose(); err == nil || !strings.Contains(err.Error(), "DAEMON-414") {
		t.Fatalf("composition over an invalid yaml: %v, want the DAEMON-414 error", err)
	}
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/code-a] }\n")
	if got, err := relaunch.Compose(); err != nil || strings.Contains(strings.Join(got["fix"], ","), "evil") {
		t.Fatalf("composition %v (err %v) used the forged capsule.json", got["fix"], err)
	}
}
