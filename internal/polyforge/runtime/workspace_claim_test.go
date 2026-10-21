package runtime

// workspace_claim_test.go: the polyforge#79 pre-claim yaml lane — the
// fresh per-claim read, the typed DAEMON-414 refusal BEFORE the aihub
// claim (no claim call, no capsule, the WI stays queued), and the
// no-restart freshness observable (an edit governs the next claim on the
// SAME runtime instance).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// newWorkspaceTestRuntime builds a runtime whose workspace mapping names
// ONE workspace carrying the given project, over a pristine demo bundle
// (materialize can succeed).
func newWorkspaceTestRuntime(t *testing.T, hub Aihub, project string) (*Runtime, string, *scriptedSpawner) {
	t.Helper()
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID: "m-test",
		Mapping:   mappingFor(ws, project),
		DataDir:   filepath.Join(t.TempDir(), "data"),
		// polyforge#106: the driver is the only engine — the rig scripts
		// it through the single engine seam.
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
		// The storm guard's window is collapsed: test episodes are
		// separated by handleOB calls, not real time.
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub.(*fakeHub).capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = r.cfg.DataDir + "/capsule-remotes"
	r.cfg.WorkflowsDir = wfDir
	return r, ws, sp
}

func writeWorkspaceYaml(t *testing.T, ws, content string) string {
	t.Helper()
	p := filepath.Join(ws, ".polyforge.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const w079ValidYaml = "steps:\n  proj:\n    default: { models: [pi:a/one] }\n"

// An invalid .polyforge.yaml refuses with the typed DAEMON-414 BEFORE the
// aihub claim: no SessionClaim call, no session end, no capsule, no
// journal entry, no live job — and after the operator fixes the file, the
// NEXT claim on the SAME runtime runs clean.
func TestWorkspaceYamlRefusesBeforeClaim(t *testing.T) {
	hub := newFakeHub()
	r, ws, sp := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: [\n") // torn
	err := r.Start(context.Background(), "wi_w079ref", StartOpts{})
	if err == nil {
		t.Fatal("an invalid workspace yaml must refuse the claim")
	}
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonWorkspaceYaml {
		t.Fatalf("expected DAEMON-414, got %v (%T)", err, err)
	}
	if !strings.Contains(ae.Detail, ".polyforge.yaml") {
		t.Fatalf("the refusal must name the file, got %q", ae.Detail)
	}
	if n := hub.claimCount(); n != 0 {
		t.Fatalf("the refusal must happen BEFORE the aihub claim, got %d claim calls", n)
	}
	if n := hub.endCount(); n != 0 {
		t.Fatalf("nothing was claimed, so nothing may be ended, got %d ends", n)
	}
	if _, serr := os.Stat(testCapsuleDir(r, "wi_w079ref")); !os.IsNotExist(serr) {
		t.Fatalf("the refused claim must materialize nothing (stat: %v)", serr)
	}
	if r.jobOf("wi_w079ref") != nil {
		t.Fatal("a refused claim must leave no live job")
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("a refusal before the claim must journal nothing, got %+v", items)
	}
	if n := sp.callCount(); n != 0 {
		t.Fatalf("the refusal must derive no agent, got %d spawns", n)
	}

	// The operator fixes the yaml; the SAME runtime serves the next claim.
	writeWorkspaceYaml(t, ws, w079ValidYaml)
	if err := r.Start(context.Background(), "wi_w079fix", StartOpts{}); err != nil {
		t.Fatalf("the fixed yaml must let the next claim run: %v", err)
	}
	if r.jobOf("wi_w079fix") == nil {
		t.Fatal("the fixed-yaml claim must run to a live agent")
	}
	if err := r.Stop(context.Background(), "wi_w079fix", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// Every strict-schema violation family refuses through the same lane.
func TestWorkspaceYamlRefusalFamilies(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"torn", "steps:\n  proj:\n    fix: [\n"},
		{"unknown top-level key", "model: x\nsteps:\n  proj:\n    default: { models: [pi:a] }\n"},
		{"retired projects section", "projects:\n  proj:\n    repos: { models: [] }\n"},
		{"retired workflow_defaults section", "workflow_defaults:\n  model_candidates: [pi:a]\n"},
		{"key that is no installed step id", "steps:\n  proj:\n    build: { models: [pi:a] }\n"},
		{"retired kind key", "steps:\n  proj:\n    gate: { models: [pi:a] }\n"},
		{"legacy top-level models (pre-#169 shape)", "models:\n  proj:\n    default: [a]\n"},
		{"unknown harness", "steps:\n  proj:\n    default: { models: [pi:a], harness: codex }\n"},
		{"project not mapped to this workspace", "steps:\n  proj:\n    default: { models: [pi:a] }\n  ghost:\n    default: { models: [pi:b] }\n"},
		{"empty project entry", "steps:\n  proj:\n"},
		{"empty model id", "steps:\n  proj:\n    default: { models: [\"\"] }\n"},
		{"scalar instead of list", "steps:\n  proj:\n    default: a\n"},
		{"duplicate key", "steps:\n  proj:\n    default: { models: [pi:a] }\n    default: { models: [pi:b] }\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hub := newFakeHub()
			r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
			writeWorkspaceYaml(t, ws, c.yaml)
			err := r.Start(context.Background(), "wi_w079fam", StartOpts{})
			ae, ok := err.(*api.Error)
			if !ok || ae.Code != api.ErrDaemonWorkspaceYaml {
				t.Fatalf("expected DAEMON-414 for %s, got %v", c.name, err)
			}
			if n := hub.claimCount(); n != 0 {
				t.Fatalf("%s: refused AFTER a claim (%d claim calls)", c.name, n)
			}
			if _, serr := os.Stat(testCapsuleDir(r, "wi_w079fam")); !os.IsNotExist(serr) {
				t.Fatalf("%s: the refused claim materialized a capsule", c.name)
			}
		})
	}
}

// A failed work-item probe claims nothing: the project (hence the
// workspace) is unknown, so Start returns the error for the caller to
// retry.
func TestProbeFailureClaimsNothing(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	hub.lookupErr["wi_w079probe"] = errors.New("hub unreachable")
	err := r.Start(context.Background(), "wi_w079probe", StartOpts{})
	if err == nil || !strings.Contains(err.Error(), "hub unreachable") {
		t.Fatalf("expected the probe error, got %v", err)
	}
	if n := hub.claimCount(); n != 0 {
		t.Fatalf("a failed probe must not claim, got %d claim calls", n)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("a failed probe must journal nothing, got %+v", items)
	}
}

// A workspace WITHOUT a yaml runs exactly as before: the claim
// proceeds.
func TestAbsentWorkspaceYamlEqualsToday(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	if _, err := os.Stat(filepath.Join(ws, ".polyforge.yaml")); !os.IsNotExist(err) {
		t.Fatalf("fixture defect: %v", err)
	}
	if err := r.Start(context.Background(), "wi_w079abs", StartOpts{}); err != nil {
		t.Fatalf("an absent yaml must not refuse the claim: %v", err)
	}
	j := r.jobOf("wi_w079abs")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	if err := r.Stop(context.Background(), "wi_w079abs", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A comments-only yaml is absent-equivalent: the claim proceeds.
func TestCommentsOnlyWorkspaceYamlIsAbsentEquivalent(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	writeWorkspaceYaml(t, ws, "# just comments\n# nothing more\n")
	if err := r.Start(context.Background(), "wi_w079cmt", StartOpts{}); err != nil {
		t.Fatalf("a comments-only yaml must not refuse the claim: %v", err)
	}
	j := r.jobOf("wi_w079cmt")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	if err := r.Stop(context.Background(), "wi_w079cmt", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A RE-CLAIM over a retained paused record reads the yaml on its own
// pre-claim path: a yaml broken for the re-claim refuses it before any
// claim call (the paused record stays), a fixed one proceeds.
func TestWorkspaceYamlReadOnFreshClaimAndReclaim(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, w079ValidYaml)
	if err := r.Start(context.Background(), "wi_w079lane", StartOpts{}); err != nil {
		t.Fatalf("fresh claim: %v", err)
	}
	if err := r.Stop(context.Background(), "wi_w079lane", protocol.FinishPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	hub.forceEnd("wi_w079lane")
	claims := hub.claimCount()
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    bogus: { models: [pi:a] }\n")
	terr := r.Start(context.Background(), "wi_w079lane", StartOpts{})
	if ae, ok := terr.(*api.Error); !ok || ae.Code != api.ErrDaemonWorkspaceYaml {
		t.Fatalf("expected DAEMON-414 on the re-claim, got %v", terr)
	}
	if n := hub.claimCount(); n != claims {
		t.Fatalf("the refused re-claim must not call the hub (claims %d -> %d)", claims, n)
	}
	writeWorkspaceYaml(t, ws, w079ValidYaml)
	if err := r.Start(context.Background(), "wi_w079lane", StartOpts{}); err != nil {
		t.Fatalf("re-claim after the fix: %v", err)
	}
	if err := r.Stop(context.Background(), "wi_w079lane", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// The journal replay is the one place the yaml is read after a claim (the
// claim is already open server-side): an invalid yaml compensates with the
// blocked session end and settles the journal.
func TestReplayWithInvalidYamlCompensatesBlocked(t *testing.T) {
	hub := newFakeHub()
	r, ws, sp := newWorkspaceTestRuntime(t, hub, "proj")
	session := fakeSession("wi_w079rep")
	if err := r.noteClaiming("wi_w079rep", session, nil); err != nil {
		t.Fatal(err)
	}
	hub.hold("wi_w079rep", session, api.StatusRunning)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: [\n")

	replayClaims(t, r)

	if end := hub.lastEnd(); end.Status != api.StatusBlocked || end.Note == "" {
		t.Fatalf("an invalid yaml at replay must end the session blocked with a note, got %+v", end)
	}
	if r.jobOf("wi_w079rep") != nil || sp.callCount() != 0 {
		t.Fatal("the refused replay must start nothing")
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("the journal must settle, got %+v", items)
	}
}

// A transport-class failure of the pre-claim probe is the typed transient
// DAEMON-503 (nothing claimed, the caller retries); a hub refusal stays a
// plain final error.
func TestStartProbeFailureIsTypedTransient(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	hub.lookupErr = map[string]error{"wi_probe": errors.New("connection refused")}
	err := r.Start(context.Background(), "wi_probe", StartOpts{})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonHubUnavailable {
		t.Fatalf("transport-class probe failure = %v, want DAEMON-503", err)
	}
	hub.lookupErr["wi_probe"] = &api.Error{Code: api.ErrCoreNotFound, Detail: "no such work item"}
	err = r.Start(context.Background(), "wi_probe", StartOpts{})
	if err == nil || (errors.As(err, &ae) && ae.Code == api.ErrDaemonHubUnavailable) {
		t.Fatalf("a hub refusal must stay final, got %v", err)
	}
	if n := hub.claimCount(); n != 0 {
		t.Fatalf("a failed probe must not claim, got %d", n)
	}
}
