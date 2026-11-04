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
	"github.com/silgrid/polyforge/internal/polyforge/liveconfig"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// policyRuntime builds a test runtime whose live snapshot carries the
// given memory_policy entries for the project's workspace path (nil = no
// block).
func policyRuntime(t *testing.T, wi string, entries map[string]workspace.PolicyEntry) *Runtime {
	t.Helper()
	setGitIdentity(t)
	hub := newFakeHub()
	hub.items = map[string]*api.WorkItem{wi: {ID: wi, ProjectName: "proj", Goal: "g", Status: api.StatusRunning}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.Live = liveconfig.New(r.cfg.Mapping, "", 0, 0)
	if entries != nil {
		r.cfg.Live.SetMemoryPolicy(map[string]map[string]workspace.PolicyEntry{testWorkspace(r): entries})
	}
	return r
}

// startForPolicy runs one Start of wi on project "proj" and answers the
// capsule's memory-policy.json.
func startForPolicy(t *testing.T, r *Runtime, wi string) workspace.MemoryPolicy {
	t.Helper()
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	return readPolicy(t, r, wi)
}

func readPolicy(t *testing.T, r *Runtime, wi string) workspace.MemoryPolicy {
	t.Helper()
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
// policy from the daemon's cli.toml snapshot — the built-in default
// without a block for the path, the merged path policy with one — and a
// yaml on disk is no source.
func TestClaimWritesMemoryPolicy(t *testing.T) {
	doc := startForPolicy(t, policyRuntime(t, "wi_mp1", nil), "wi_mp1")
	if !reflect.DeepEqual(doc, workspace.DefaultMemoryPolicy()) {
		t.Fatalf("no block: %+v", doc)
	}
	r := policyRuntime(t, "wi_mp2", map[string]workspace.PolicyEntry{
		"default": {Record: []string{"a"}, Skip: []string{"b"}},
		"proj":    {Record: []string{"c"}},
	})
	doc = startForPolicy(t, r, "wi_mp2")
	if !reflect.DeepEqual(doc.Record, []string{"a", "c"}) || !reflect.DeepEqual(doc.Skip, []string{"b"}) {
		t.Fatalf("path policy: %+v", doc)
	}
	// a swap after the claim (a reload) leaves the running job's capsule alone
	r.cfg.Live.SetMemoryPolicy(nil)
	if got := readPolicy(t, r, "wi_mp2"); !reflect.DeepEqual(got, doc) {
		t.Fatalf("a running job's capsule changed after a swap: %+v", got)
	}
}
