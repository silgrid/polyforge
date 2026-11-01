package runtime

// attrs_test.go: — the dispatch-time attr composition reads
// the workspace yaml and the bundle fresh at every call (nothing is
// snapshotted at claim), so an attr edit between two dispatches governs the
// next one.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const attrsFrontmatter = `---
name: demo-bundle
steps:
  - id: grill
    attrs:
      question_batch_size: { type: int, default: 0 }
  - { id: fix }
---

# demo
`

func TestAttrsComposeFreshPerDispatch(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, attrsFrontmatter)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	r := New(cfg, hub, sp, supervise.NewQueue[protocol.RTMsg](), supervise.NewQueue[protocol.OBMsg]())
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    grill:\n      attrs: { question_batch_size: 2 }\n")
	if err := r.Start(context.Background(), "wi_attrs216", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_attrs216")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	j.mu.Lock()
	compose := r.composeAttrsFor(j.project, j.bundleName)
	j.mu.Unlock()
	first, err := compose()
	if err != nil || !reflect.DeepEqual(first, map[string]map[string]string{"grill": {"question_batch_size": "2"}}) {
		t.Fatalf("first dispatch attrs = %v, %v; want grill.question_batch_size=2", first, err)
	}
	// the owner edits the yaml between two dispatches: the next one reads it
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    grill:\n      attrs: { question_batch_size: 5 }\n")
	second, err := compose()
	if err != nil || second["grill"]["question_batch_size"] != "5" {
		t.Fatalf("the next dispatch must read the edit: %v, %v", second, err)
	}
	// back to the default: no override at all (the all-at-once prompt)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    grill:\n      attrs: { question_batch_size: 0 }\n")
	if third, err := compose(); err != nil || third != nil {
		t.Fatalf("a value equal to the default composes nothing: %v, %v", third, err)
	}
	// an invalid yaml blocks the dispatch like a compose failure (DAEMON-414)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    grill:\n      attrs: { mystery_knob: 1 }\n")
	if _, err := compose(); err == nil {
		t.Fatal("an undeclared attr must fail the dispatch-time composition")
	}
	if err := r.Stop(context.Background(), "wi_attrs216", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
