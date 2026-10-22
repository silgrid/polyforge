package daemon

// placement_test.go: the submit answer's placement + missing-clone
// signalling — round trips through the CLI's own
// DaemonClient over the daemon's real UDS face, the repo set scripted
// through fakeHub's work-item-view/project reads, the clone presence
// real on disk (workspace.IsRepoClone's own rule).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// placementClone makes <ws>/.repo/<name> look like a git clone (a .git
// directory — the presence rule's own face).
func placementClone(t *testing.T, ws, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ws, ".repo", name, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// placementSubmit submits wi and answers the decoded claim-family answer.
func placementSubmit(t *testing.T, td *testDaemon, wi string) cli.SubmitResponse {
	t.Helper()
	mkBare(t, td.cfg.GitRemoteBase, wi)
	raw, err := td.client.ClaimWithModels(wi, "normal", nil)
	if err != nil {
		t.Fatalf("submit %s: %v", wi, err)
	}
	var sr cli.SubmitResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		t.Fatalf("submit answer undecodable: %v (%s)", err, raw)
	}
	return sr
}

// TestSubmitAnswerStatesPlacement: an accepted submit of a mapped project
// answers the placement — the local daemon's machine id and the capsule
// landing path the claim itself resolves — with no missing_repos when
// every clone the claim will clone is present.
func TestSubmitAnswerStatesPlacement(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.repoInventory = map[string][]string{} // no declared inventory: the project's repos are the check set
	td.hub.projectRepos = map[string][]string{"proj": {"ra", "rb"}}
	ws := td.cfg.Mapping.Workspaces()[0]
	for _, name := range []string{"ra", "rb"} {
		placementClone(t, ws, name)
	}

	sr := placementSubmit(t, td, "wi_p")
	if !sr.Accepted || sr.WorkItemID != "wi_p" {
		t.Fatalf("submit answer = %+v, want accepted for wi_p", sr)
	}
	if sr.MachineID != "m-test" {
		t.Fatalf("machine_id = %q, want the local daemon's m-test", sr.MachineID)
	}
	if want := filepath.Join(ws, "capsules", "wi_p"); sr.CapsulePath != want {
		t.Fatalf("capsule_path = %q, want the claim's own landing path %q", sr.CapsulePath, want)
	}
	if len(sr.MissingRepos) != 0 {
		t.Fatalf("all clones present: missing_repos = %v, want none", sr.MissingRepos)
	}
}

// TestSubmitAnswerWarnsMissingClonesStillAccepted: the mapped workspace
// lacks the clone of one checked repo — the answer names it (and not the
// present one) and the submit stays accepted: the claim clones fresh, no
// refusal, no override flag.
func TestSubmitAnswerWarnsMissingClonesStillAccepted(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.repoInventory = map[string][]string{} // no declared inventory: the project's repos are the check set
	td.hub.projectRepos = map[string][]string{"proj": {"ra", "rb"}}
	ws := td.cfg.Mapping.Workspaces()[0]
	placementClone(t, ws, "ra") // rb missing

	sr := placementSubmit(t, td, "wi_m")
	if !sr.Accepted {
		t.Fatalf("the submit must stay accepted alongside the warning: %+v", sr)
	}
	if len(sr.MissingRepos) != 1 || sr.MissingRepos[0] != "rb" {
		t.Fatalf("missing_repos = %v, want exactly the missing repo [rb]", sr.MissingRepos)
	}
}

// TestSubmitAnswerCloneSetInventoryElseProjectRepos: the checked set is
// the work item's frozen repo_inventory when it declares one — exactly
// that list, an undeclared project repo is not checked — else every repo
// of the project.
func TestSubmitAnswerCloneSetInventoryElseProjectRepos(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.projectRepos = map[string][]string{"proj": {"ra", "rb"}}
	td.hub.repoInventory = map[string][]string{"wi_inv": {"ra"}}
	// nothing cloned at all

	srInv := placementSubmit(t, td, "wi_inv")
	if !srInv.Accepted {
		t.Fatalf("the submit must stay accepted: %+v", srInv)
	}
	if len(srInv.MissingRepos) != 1 || srInv.MissingRepos[0] != "ra" {
		t.Fatalf("declared inventory: missing_repos = %v, want exactly [ra] — the undeclared rb is not checked", srInv.MissingRepos)
	}

	srAll := placementSubmit(t, td, "wi_all")
	if !srAll.Accepted {
		t.Fatalf("the submit must stay accepted: %+v", srAll)
	}
	if len(srAll.MissingRepos) != 2 || srAll.MissingRepos[0] != "ra" || srAll.MissingRepos[1] != "rb" {
		t.Fatalf("no inventory: missing_repos = %v, want every project repo [ra rb] (sorted)", srAll.MissingRepos)
	}
}

// TestSubmitAnswerProbeUnresolvedNoPlacement: a hub probe that cannot
// resolve the work item answers an accepted submit with NO placement
// fields — nothing invented, nothing blocked (the submit stays
// durable-queued exactly as today).
func TestSubmitAnswerProbeUnresolvedNoPlacement(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	td.hub.probeFails = 2 // resolveWI's read + the route's own probe read
	td.hub.mu.Unlock()

	sr := placementSubmit(t, td, "wi_u")
	if !sr.Accepted || sr.WorkItemID != "wi_u" {
		t.Fatalf("submit answer = %+v, want accepted for wi_u", sr)
	}
	if sr.MachineID != "" || sr.CapsulePath != "" || len(sr.MissingRepos) != 0 {
		t.Fatalf("probe unresolved: the answer must carry no placement fields, got %+v", sr)
	}
}

// TestSubmitUnmappedRefusalUnchanged: an unmapped project is refused
// DAEMON-412 byte-for-byte exactly as today — the refusal sits ahead of
// the placement code, which never runs for it.
func TestSubmitUnmappedRefusalUnchanged(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		ws := filepath.Join(t.TempDir(), "ws")
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		c.Mapping = mappingFor(ws, "other") // "proj" is unmapped
	})
	mkBare(t, td.cfg.GitRemoteBase, "wi_x")

	_, err := td.client.ClaimWithModels("wi_x", "normal", nil)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonUnmappedProject {
		t.Fatalf("submit error = %v, want the typed DAEMON-412 refusal", err)
	}
	// the runtime's own refusal message, byte-for-byte (unmappedProjectError)
	want := "project \"proj\" is not in the [workspace] mapping of this machine — the claim is refused before any capsule is created; " +
		"add the project to a [workspace.\"<path>\"] section in the config (`pf doctor` checks the mapping)"
	if ae.Detail != want {
		t.Fatalf("DAEMON-412 detail = %q, want the unchanged byte-for-byte refusal %q", ae.Detail, want)
	}
}
