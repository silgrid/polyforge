package runtime

// workspace_audit_test.go: the capsule.json audit record (polyforge#79)
// — the claim-time snapshot that doubles as the recovery rehydration
// source. A claim whose project has a models entry records the per-step
// post-precedence candidate lists; any other claim records NOTHING
// (omitempty, never fabricated) and its legacy fields are unchanged.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func readIdentity(t *testing.T, capDir string) (*capsule.Identity, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "capsule.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idn capsule.Identity
	if err := json.Unmarshal(raw, &idn); err != nil {
		t.Fatalf("capsule.json does not decode: %v", err)
	}
	return &idn, string(raw)
}

// newAuditRuntime is a workspace runtime over a bundle WITH frontmatter
// (the compose has steps to key on), optionally serving registry repos.
func newAuditRuntime(t *testing.T, repos []api.ProjectRepo) (*Runtime, string) {
	t.Helper()
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	hub := newFakeHub()
	hub.repos = repos
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj", "other"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	return r, ws
}

// A claim whose project has a models entry dispatches under the yaml's
// precedence (the governing source at every dispatch) and records NO
// effective-candidates audit — capsule.json carries no such field.
func TestGovernedClaimWritesAuditRecord(t *testing.T) {
	r, ws := newAuditRuntime(t, nil)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    default: { models: [pi:w79/flow-a, pi:w79/flow-b] }\n")

	if err := r.Start(context.Background(), "wi_w079aud", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, raw := readIdentity(t, testCapsuleDir(r, "wi_w079aud"))
	if strings.Contains(raw, "effective_candidates") {
		t.Fatalf("the deleted audit key must not be written (raw %s)", raw)
	}
	if strings.Contains(raw, "workspace_yaml") || strings.Contains(raw, "repo_sources") {
		t.Fatalf("the retired audit keys must not be written (raw %s)", raw)
	}
	if err := r.Stop(context.Background(), "wi_w079aud", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A yaml that lists only ANOTHER (mapped) project, and a no-yaml claim, record NO
// audit content — the legacy fields (including the Repos name→SHA
// baselines) are unchanged.
func TestUngovernedClaimWritesNoAuditContent(t *testing.T) {
	for _, yaml := range []string{"", "steps:\n  other:\n    default: { models: [pi:w79/x] }\n"} {
		registryURL, remoteHead := w079Remote(t, "registry.txt", "registry marker\n")
		r, ws := newAuditRuntime(t, []api.ProjectRepo{{Name: "w079reg", URL: registryURL}})
		if yaml != "" {
			writeWorkspaceYaml(t, ws, yaml)
		}
		if err := r.Start(context.Background(), "wi_w079noaud", StartOpts{}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		idn, raw := readIdentity(t, testCapsuleDir(r, "wi_w079noaud"))
		if strings.Contains(raw, "effective_candidates") {
			t.Fatalf("yaml %q: an ungoverned claim must record no audit content (raw %s)", yaml, raw)
		}
		if idn.Repos["w079reg"] != remoteHead {
			t.Fatalf("legacy Repos baseline changed: %v", idn.Repos)
		}
		if err := r.Stop(context.Background(), "wi_w079noaud", protocol.FinishWrapped); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
}
