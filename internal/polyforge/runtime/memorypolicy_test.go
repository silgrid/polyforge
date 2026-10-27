package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// startForPolicy runs one Start of wi on project "proj", with yaml (when
// non-empty) as the workspace's .polyforge.yaml, and answers the
// capsule's memory-policy.json.
func startForPolicy(t *testing.T, wi, yaml string) workspace.MemoryPolicy {
	t.Helper()
	setGitIdentity(t)
	hub := newFakeHub()
	hub.items = map[string]*api.WorkItem{wi: {ID: wi, ProjectName: "proj", Goal: "g", Status: api.StatusRunning}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	if yaml != "" {
		if err := os.MkdirAll(testWorkspace(r), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(workspace.PathFor(testWorkspace(r)), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	raw, err := os.ReadFile(filepath.Join(testCapsuleDir(r, wi), filepath.FromSlash(capsule.MemoryPolicyJSON)))
	if err != nil {
		t.Fatalf("memory-policy.json: %v", err)
	}
	var doc workspace.MemoryPolicy
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("memory-policy.json unparsable: %v", err)
	}
	return doc
}

// TestClaimWritesMemoryPolicy: the claim writes the project's effective
// policy — the built-in default without a section, the merged workspace
// policy with one.
func TestClaimWritesMemoryPolicy(t *testing.T) {
	doc := startForPolicy(t, "wi_mp1", "")
	if !reflect.DeepEqual(doc, workspace.DefaultMemoryPolicy()) {
		t.Fatalf("no yaml: %+v", doc)
	}
	doc = startForPolicy(t, "wi_mp2", "memory_policy:\n  default:\n    record: [a]\n    skip: [b]\n  proj:\n    record: [c]\n")
	if !reflect.DeepEqual(doc.Record, []string{"a", "c"}) || !reflect.DeepEqual(doc.Skip, []string{"b"}) {
		t.Fatalf("workspace policy: %+v", doc)
	}
}
