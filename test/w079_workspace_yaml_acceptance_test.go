package test

// w079_workspace_yaml_acceptance_test.go: the acceptance suite for the
// workspace `.polyforge.yaml` — per-project model candidates. It pins the
// contract through the project's real e2e surfaces only: the
// pf/aihub/fakepi binaries, the `pf` CLI, the daemon's UDS face, cli.toml,
// real git and the real filesystem.
//
// The file's closed grammar (everything else is a load error):
//
//	steps:
//	  <project>:
//	    default: { models: [<harness>:<model>, ...] }   # optional: every step of the project
//	    <step-id>: { models: [<harness>:<model>, ...] } # optional: a step id of the installed workflows
//
// Per step: project step-id list > bundle step list > project default >
// bundle defaults (polyforge#164: a step's own list outranks the project
// default). The daemon reads and validates the file BEFORE the
// aihub claim: an invalid file refuses with DAEMON-414 (surfaced to
// `pf submit`), the WI stays queued (never claimed, not even
// transiently), no capsule exists. Repo lists come from the aihub
// registry; `pf file create` infers --project from the cli.toml mapping.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// The autopilot bundle's pinned heads (frontmatter at HEAD): the
// defaults head and the verification step's step-level head (fix has no
// list of its own). Kept-face guards assert these stay in effect
// wherever the yaml layer is silent.
const (
	w079DefaultsHead         = "sub2api-glm/glm-5.3"
	w079VerificationStepHead = "sub2api-glm/glm-5.3-flash"
)

// ---------------------------------------------------------------------------
// Fixtures: yaml files, local checkouts, registry remotes, the pf CLI
// with a working directory, capsule/execution readers.
// ---------------------------------------------------------------------------

// w079Yaml writes one .polyforge.yaml at the workspace root.
func w079Yaml(t *testing.T, ws, content string) string {
	t.Helper()
	p := filepath.Join(ws, ".polyforge.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// w079GitRepo creates a real git repo (one commit carrying markerName
// with markerContent) at dir and answers its HEAD SHA.
func w079GitRepo(t *testing.T, dir, markerName, markerContent string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, markerName), []byte(markerContent), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "w079 init")
	return strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
}

// w079Remote is one local fake registry remote: a real git repo served
// over file://, carrying its own marker commit.
func w079Remote(t *testing.T, markerName, markerContent string) (url, head string) {
	t.Helper()
	dir := t.TempDir()
	head = w079GitRepo(t, dir, markerName, markerContent)
	return "file://" + dir, head
}

// w079Env is the environment one w079 CLI run gets: a scratch HOME and
// XDG_CONFIG_HOME, the connection env, an optional --config global
// flag, and a working directory (the inference scenarios need the CLI
// to run INSIDE a mapped workspace).
type w079Env struct {
	home   string
	config string
	cwd    string
	noConn bool
}

// w079Pf runs the real pf binary once with a working directory and
// returns (combined output, exit code), asserting nothing.
func w079Pf(t *testing.T, inst *v1Install, e w079Env, args ...string) (string, int) {
	t.Helper()
	pfPath := filepath.Join(v1Bins(t), "pf")
	if inst != nil && inst.pfPath != "" {
		pfPath = inst.pfPath
	}
	if e.home == "" {
		e.home = t.TempDir()
	}
	xdg := filepath.Join(e.home, ".config")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"XDG_CONFIG_HOME=" + xdg,
	}
	if !e.noConn && inst != nil {
		env = append(env,
			"PF_SERVER="+inst.url,
			"PF_API_KEY="+inst.adminKey,
			"PF_USER_ID=usr_root",
		)
	}
	argv := make([]string, 0, len(args)+2)
	if e.config != "" {
		argv = append(argv, "--config", e.config)
	}
	argv = append(argv, args...)
	cmd := exec.Command(pfPath, argv...)
	if e.cwd != "" {
		cmd.Dir = e.cwd
	}
	cmd.Env = env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v (cwd %s): %v", argv, e.cwd, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// w079PfWant asserts one w079 CLI run's exit code and answers the
// combined output (wantExit < 0 = must succeed).
func w079PfWant(t *testing.T, inst *v1Install, wantExit int, e w079Env, args ...string) string {
	t.Helper()
	out, code := w079Pf(t, inst, e, args...)
	if wantExit < 0 && code != 0 {
		t.Fatalf("pf %v (cwd %s): exit %d, want success (output %q)", args, e.cwd, code, out)
	}
	if wantExit >= 0 && code != wantExit {
		t.Fatalf("pf %v (cwd %s): exit %d, want %d (output %q)", args, e.cwd, code, wantExit, out)
	}
	return out
}

// w079Capsule is the capsule dir of wi under workspace ws.
func w079Capsule(t *testing.T, inst *v1Install, ws, wi string) string {
	return filepath.Join(ws, "capsules", wiPathName(t, inst, wi))
}

// w079CapsuleRaw answers the raw capsule.json bytes of one capsule.
func w079CapsuleRaw(t *testing.T, capDir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "capsule.json"))
	if err != nil {
		t.Fatalf("capsule.json of %s: %v", capDir, err)
	}
	return string(raw)
}

// w079Identity is the loose read of the capsule.json legacy fields the
// spec pins as unchanged (work_item_id / project / bundle_name / the
// Repos name→SHA baselines).
type w079Identity struct {
	WorkItemID string            `json:"work_item_id"`
	Project    string            `json:"project"`
	BundleName string            `json:"bundle_name"`
	Repos      map[string]string `json:"repos"`
}

// w079IdentityOf decodes one capsule.json's legacy identity.
func w079IdentityOf(t *testing.T, capDir string) *w079Identity {
	t.Helper()
	var id w079Identity
	if err := json.Unmarshal([]byte(w079CapsuleRaw(t, capDir)), &id); err != nil {
		t.Fatalf("capsule.json of %s does not decode: %v", capDir, err)
	}
	return &id
}

// w079Model answers the recorded effective model of one step out of
// execution.json (the driver writes the pinned candidate on every
// step-starting write; "" = no switchable chain).
func w079Model(t *testing.T, capDir, step string) string {
	t.Helper()
	x := readExecution(t, capDir)
	st := x.Steps[step]
	if st == nil {
		t.Fatalf("no step %q in execution.json of %s (steps %v)", step, capDir, x.Steps)
	}
	return st.Model
}

// w079AttemptStats counts one step's archived attempts by cause: the
// provider-class infra failures (the only class that advances the
// candidate pointer) and the gate-fail rewind archives.
func w079AttemptStats(t *testing.T, capDir, step string) (providerInfra, gateRewind int) {
	t.Helper()
	x := readExecution(t, capDir)
	st := x.Steps[step]
	if st == nil {
		t.Fatalf("no step %q in execution.json of %s", step, capDir)
	}
	for _, a := range st.Attempts {
		cause, _ := a["cause"].(string)
		fc, _ := a["failure_cause"].(string)
		switch {
		case cause == "gate_fail_rewind":
			gateRewind++
		case cause == "infra_failure" && isModelClassCause(fc):
			providerInfra++
		}
	}
	return
}

// w079Refused asserts one pre-claim refusal of wi: the `pf submit`
// itself failed (the daemon's synchronous pre-check surfaces the typed
// DAEMON-414 to the submitter) with every needle in its output, and the
// outcome is STABLE — polled for a few seconds, the WI never leaves
// queued at aihub (it is never claimed, not even transiently), no
// capsule exists for it, and the daemon keeps serving.
func w079Refused(t *testing.T, d *w113Daemon, inst *v1Install, ws, wi, submitOut string, submitCode int, needles ...string) {
	t.Helper()
	w079RefusalOutput(t, wi, submitOut, submitCode, needles...)
	w079StayQueued(t, d, inst, []w079RefusedWI{{wi: wi, capDir: w079Capsule(t, inst, ws, wi)}})
	d.pfWant(inst, -1, "ops", "status")
}

// w079RefusalOutput asserts the synchronous half of a pre-claim
// refusal: the `pf submit` of wi failed with DAEMON-414 and every
// needle in its output.
func w079RefusalOutput(t *testing.T, wi, submitOut string, submitCode int, needles ...string) {
	t.Helper()
	if submitCode == 0 {
		t.Fatalf("pf submit of %s succeeded; want a DAEMON-414 refusal (output %q)", wi, submitOut)
	}
	for _, n := range append([]string{"DAEMON-414"}, needles...) {
		if !strings.Contains(submitOut, n) {
			t.Fatalf("submit refusal of %s misses %q (output %q)", wi, n, submitOut)
		}
	}
}

// w079RefusedWI is one refused work item the stability window watches:
// its id, the capsule dir a claim would materialize, and a case name
// for the failure message ("" outside a table).
type w079RefusedWI struct {
	name, wi, capDir string
}

// w079RefusalWindow is how long a refused work item is watched for a
// (wrong) claim after its refusal.
const w079RefusalWindow = 3 * time.Second

// w079StayQueued asserts the stable half of a pre-claim refusal over ONE
// window of w079RefusalWindow starting now (every item was refused
// before the call, so each is watched at least that long after its own
// refusal): no item ever leaves queued at aihub and no capsule appears.
// The last sweep starts after the window closes and also reads aihub's
// durable event history, where any claim — however briefly held — left a
// session_claimed event.
func w079StayQueued(t *testing.T, d *w113Daemon, inst *v1Install, refused []w079RefusedWI) {
	t.Helper()
	deadline := time.Now().Add(w079RefusalWindow)
	for {
		closed := time.Now().After(deadline)
		for _, r := range refused {
			label := r.wi
			if r.name != "" {
				label = fmt.Sprintf("%s (case %q)", r.wi, r.name)
			}
			if status := inst.wiStatus(t, r.wi); status != "queued" {
				t.Fatalf("refused work item %s left the queued state: status %q — a pre-claim refusal must never claim it (log tail %s)", label, status, d.logTail())
			}
			if _, err := os.Stat(r.capDir); err == nil {
				t.Fatalf("refused claim of %s materialized a capsule at %s", label, r.capDir)
			}
			if closed && strings.Contains(wiRawWithEvents(t, inst, r.wi), `"session_claimed"`) {
				t.Fatalf("refused work item %s was claimed at some point: aihub recorded session_claimed", label)
			}
		}
		if closed {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// w079Run submits wi and waits for the completed, wrapped run; answers
// the capsule dir.
func w079Run(t *testing.T, d *w113Daemon, inst *v1Install, ws, wi string) string {
	t.Helper()
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
	return capDir
}

// w079Setup starts one daemon over the given workspace→projects mapping.
func w079Setup(t *testing.T, inst *v1Install, wss ...w113WS) *w113Daemon {
	t.Helper()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"), inst.url, inst.adminKey, "", wss)
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
	d.start()
	t.Cleanup(d.stop)
	return d
}

// ---------------------------------------------------------------------------
// Read/failure semantics.
// ---------------------------------------------------------------------------

// TestW079AbsentAndEmptyYamlRunExactlyAsHead pins the kept faces: a
// workspace with NO .polyforge.yaml, one whose file is comments-only,
// and one whose models section does not list the claimed project all
// behave as bundle-only — the repo inventory comes from the aihub
// registry, models from bundle frontmatter alone, the legacy capsule.json
// fields are unchanged and no yaml audit content is fabricated.
func TestW079AbsentAndEmptyYamlRunExactlyAsHead(t *testing.T) {
	remoteURL, remoteHead := w079Remote(t, "w079reg.txt", "registry marker\n")
	inst := newV1InstallRepos(t, []api.ProjectRepo{{Name: "w079reg", URL: remoteURL}})
	w113EnsureProject(t, inst, "w079other")
	ws := t.TempDir()
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e", "w079other"}})

	wiA := w113NewWI(t, inst, "e2e", "w079 absent-yaml run exactly as head", envelopeAutopilot())
	capA := w079Run(t, d, inst, ws, wiA)

	w079Yaml(t, ws, "# w079: comments only — no sections at all.\n# a second comment line.\n")
	wiB := w113NewWI(t, inst, "e2e", "w079 comments-only yaml is a no-op", envelopeAutopilot())
	capB := w079Run(t, d, inst, ws, wiB)

	w079Yaml(t, ws, "steps:\n  w079other:\n    default: { models: [pi:w079-other-model] }\n")
	wiC := w113NewWI(t, inst, "e2e", "w079 unlisted project keeps bundle models", envelopeAutopilot())
	capC := w079Run(t, d, inst, ws, wiC)

	for name, capDir := range map[string]string{"absent yaml": capA, "comments-only yaml": capB, "project not listed": capC} {
		raw := w079CapsuleRaw(t, capDir)
		if strings.Contains(raw, "yaml") || strings.Contains(raw, "effective_candidates") || strings.Contains(raw, "w079-other-model") {
			t.Fatalf("%s: capsule.json fabricated yaml audit content (raw %q)", name, raw)
		}
		id := w079IdentityOf(t, capDir)
		if len(id.Repos) != 1 || id.Repos["w079reg"] != remoteHead {
			t.Fatalf("%s: Repos baselines changed: %v (want w079reg=%s)", name, id.Repos, remoteHead)
		}
		marker, err := os.ReadFile(filepath.Join(capDir, "repos", "w079reg", "w079reg.txt"))
		if err != nil || string(marker) != "registry marker\n" {
			t.Fatalf("%s: the registry clone did not land: %q, %v", name, marker, err)
		}
		if got := w079Model(t, capDir, "verification"); got != w079VerificationStepHead {
			t.Fatalf("%s: verification model %q, want the bundle step-level head %q", name, got, w079VerificationStepHead)
		}
		if got := w079Model(t, capDir, "fix"); got != w079DefaultsHead {
			t.Fatalf("%s: fix model %q, want the bundle defaults head %q", name, got, w079DefaultsHead)
		}
		if got := w079Model(t, capDir, "grill"); got != w079DefaultsHead {
			t.Fatalf("%s: grill model %q, want the bundle defaults head %q", name, got, w079DefaultsHead)
		}
	}
}

// TestW079PerProjectModelPrecedence pins the chain inside ONE workspace
// serving three projects: project step-id list > bundle step list > project
// default > bundle defaults. e2e declares default + fix (the fix step
// gets the fix list, verification keeps its own step list, the rest get
// the default), demo-ops declares only a default (it governs every step
// without a list of its own, never a step's own list — polyforge#164),
// and a third project the yaml does not list stays bundle-only — no
// cross-project leak.
func TestW079PerProjectModelPrecedence(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "demo-ops")
	w113EnsureProject(t, inst, "w079third")
	ws := t.TempDir()
	w079Yaml(t, ws, `steps:
  e2e:
    default: { models: [pi:w079-e2e-def] }
    fix: { models: [pi:w079-e2e-code-a, pi:w079-e2e-code-b] }
  demo-ops:
    default: { models: [pi:w079-demo-ops-def] }
`)
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e", "demo-ops", "w079third"}})

	wiE := w113NewWI(t, inst, "e2e", "w079 e2e models", envelopeAutopilot())
	wiI := w113NewWI(t, inst, "demo-ops", "w079 demo-ops models", envelopeAutopilot())
	wiT := w113NewWI(t, inst, "w079third", "w079 unlisted project models", envelopeAutopilot())
	capE := w079Run(t, d, inst, ws, wiE)
	capI := w079Run(t, d, inst, ws, wiI)
	capT := w079Run(t, d, inst, ws, wiT)

	for step, want := range map[string]string{
		"grill": "w079-e2e-def", "verification": w079VerificationStepHead,
		"fix": "w079-e2e-code-a",
	} {
		if got := w079Model(t, capE, step); got != want {
			t.Fatalf("e2e %s model %q, want %q (project step id > bundle step > project default)", step, got, want)
		}
	}
	for _, step := range []string{"grill", "fix"} {
		if got := w079Model(t, capI, step); got != "w079-demo-ops-def" {
			t.Fatalf("demo-ops %s model %q, want its project default w079-demo-ops-def", step, got)
		}
	}
	if got := w079Model(t, capI, "verification"); got != w079VerificationStepHead {
		t.Fatalf("demo-ops verification model %q, want its own step-level head %q (a step list outranks the project default)", got, w079VerificationStepHead)
	}
	if got := w079Model(t, capT, "verification"); got != w079VerificationStepHead {
		t.Fatalf("unlisted project verification model %q, want the bundle step-level head %q", got, w079VerificationStepHead)
	}
	if got := w079Model(t, capT, "grill"); got != w079DefaultsHead {
		t.Fatalf("unlisted project grill model %q, want the bundle defaults head %q", got, w079DefaultsHead)
	}
	if raw := w079CapsuleRaw(t, capT); strings.Contains(raw, "w079-e2e") || strings.Contains(raw, "w079-demo-ops") {
		t.Fatalf("the unlisted project's capsule leaks another project's models: %s", raw)
	}
	// e2e's capsule records no effective-candidates audit (the field was
	// deleted with the audit's dead-code sweep — the yaml governs
	// dispatches directly; the model pins above are the behavior) and
	// leaks nothing of the other project's.
	if raw := w079CapsuleRaw(t, capE); strings.Contains(raw, "effective_candidates") || strings.Contains(raw, "w079-demo-ops") {
		t.Fatalf("e2e's capsule must carry no effective-candidates audit and leak nothing: %s", raw)
	}
}

// TestW079ProjectStepOnlyFallsThroughToBundle pins the scoping of a
// step-id-only entry: a `grill` list hits the grill step only, while the
// other steps keep the bundle's own resolution.
func TestW079ProjectStepOnlyFallsThroughToBundle(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    grill: { models: [pi:w079-spec-x] }\n")
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e"}})
	capDir := w079Run(t, d, inst, ws, w113NewWI(t, inst, "e2e", "w079 step-only list", envelopeAutopilot()))
	if got := w079Model(t, capDir, "grill"); got != "w079-spec-x" {
		t.Fatalf("grill model %q, want w079-spec-x", got)
	}
	if got := w079Model(t, capDir, "fix"); got != w079DefaultsHead {
		t.Fatalf("fix model %q, want the bundle defaults head %q", got, w079DefaultsHead)
	}
	if got := w079Model(t, capDir, "verification"); got != w079VerificationStepHead {
		t.Fatalf("verification model %q, want its bundle step-level head %q", got, w079VerificationStepHead)
	}
}

// TestW079OneDaemonTwoWorkspacesTwoYamls: one daemon, two workspaces,
// two different yamls, the SAME bundle — each claim applies only its own
// workspace's yaml.
func TestW079OneDaemonTwoWorkspacesTwoYamls(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "demo-ops")
	wsA := filepath.Join(t.TempDir(), "wsA")
	wsB := filepath.Join(t.TempDir(), "wsB")
	for _, p := range []string{wsA, wsB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w079Yaml(t, wsA, "steps:\n  e2e:\n    default: { models: [pi:w079a-flow] }\n")
	w079Yaml(t, wsB, "steps:\n  demo-ops:\n    fix: { models: [pi:w079b-code] }\n")
	d := w079Setup(t, inst, w113WS{key: wsA, projects: []string{"e2e"}}, w113WS{key: wsB, projects: []string{"demo-ops"}})

	wiA := w113NewWI(t, inst, "e2e", "w079 two-workspace claim A", envelopeAutopilot())
	wiB := w113NewWI(t, inst, "demo-ops", "w079 two-workspace claim B", envelopeAutopilot())
	capA := w079Run(t, d, inst, wsA, wiA)
	capB := w079Run(t, d, inst, wsB, wiB)

	if got := w079Model(t, capA, "grill"); got != "w079a-flow" {
		t.Fatalf("A grill model %q, want w079a-flow", got)
	}
	if got := w079Model(t, capB, "fix"); got != "w079b-code" {
		t.Fatalf("B fix model %q, want w079b-code", got)
	}
	if got := w079Model(t, capB, "grill"); got != w079DefaultsHead {
		t.Fatalf("B grill model %q, want the bundle defaults head %q", got, w079DefaultsHead)
	}
	// Neither capsule carries a candidate audit (the effective-candidates
	// field was deleted with the audit's dead-code sweep — the yaml
	// governs dispatches directly; the model pins above are the
	// behavior), and neither leaks the other project's models.
	rawA, rawB := w079CapsuleRaw(t, capA), w079CapsuleRaw(t, capB)
	if strings.Contains(rawA, "effective_candidates") || strings.Contains(rawA, "w079b-code") {
		t.Fatalf("A's capsule must carry no candidate audit and leak nothing (raw %s)", rawA)
	}
	if strings.Contains(rawB, "effective_candidates") || strings.Contains(rawB, "w079a-flow") {
		t.Fatalf("B's capsule must carry no candidate audit and leak nothing (raw %s)", rawB)
	}
}

// TestW079YamlEditGovernsNextClaimNoRestart: an edit takes effect on the
// NEXT claim with no daemon restart (same pid throughout).
func TestW079YamlEditGovernsNextClaimNoRestart(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    default: { models: [pi:w079-edit-v1] }\n")
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e"}})
	pid := d.cmd.Process.Pid

	capA := w079Run(t, d, inst, ws, w113NewWI(t, inst, "e2e", "w079 edit first claim", envelopeAutopilot()))
	if got := w079Model(t, capA, "grill"); got != "w079-edit-v1" {
		t.Fatalf("claim 1 grill model %q, want w079-edit-v1", got)
	}
	w079Yaml(t, ws, "steps:\n  e2e:\n    default: { models: [pi:w079-edit-v2] }\n")
	capB := w079Run(t, d, inst, ws, w113NewWI(t, inst, "e2e", "w079 edit second claim", envelopeAutopilot()))
	if got := w079Model(t, capB, "grill"); got != "w079-edit-v2" {
		t.Fatalf("claim 2 grill model %q, want w079-edit-v2 — the edit must govern the next claim", got)
	}
	if raw := w079CapsuleRaw(t, capB); strings.Contains(raw, "w079-edit-v1") {
		t.Fatalf("claim 2's audit carries the pre-edit token (raw %s)", raw)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the daemon process %d did not survive the yaml edit: %v", pid, err)
	}
}

// TestW079InvalidYamlRefusesWithoutClaim pins the strict schema: every
// invalid shape (the retired `projects:` / `workflow_defaults:` shapes
// included) refuses the work item BEFORE the aihub claim — `pf submit`
// surfaces the typed DAEMON-414 naming .polyforge.yaml, the WI stays
// queued (never even transiently running), no capsule exists, and the
// daemon keeps serving.
func TestW079InvalidYamlRefusesWithoutClaim(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e"}})

	cases := []struct {
		name    string
		yaml    string
		needles []string
	}{
		{"torn yaml", "steps:\n  e2e:\n    default: [\n", nil},
		{"old shape projects", "projects:\n  e2e:\n    repos: []\n", []string{"projects"}},
		{"old shape workflow_defaults", "workflow_defaults:\n  model_candidates: [pi:a]\n", []string{"workflow_defaults"}},
		{"unknown top-level key", "model:\n  e2e:\n    default: { models: [pi:a] }\n", []string{"model"}},
		{"unknown step id", "steps:\n  e2e:\n    w079bogusstep: { models: [pi:a] }\n", []string{"steps.e2e.w079bogusstep"}},
		{"retired kind key", "steps:\n  e2e:\n    gate: { models: [pi:a] }\n", []string{"steps.e2e.gate"}},
		{"legacy top-level models", "models:\n  e2e:\n    default: [a]\n", []string{`unknown top-level key "models"`}},
		{"retired harness key", "steps:\n  e2e:\n    fix: { models: [pi:a], harness: claude }\n", []string{"steps.e2e.fix.harness: the `harness` key was removed (polyforge#175)"}},
		{"unprefixed candidate", "steps:\n  e2e:\n    fix: { models: [pi:a, sub2api-glm/glm-5.3] }\n", []string{`steps.e2e.fix.models[1]: "sub2api-glm/glm-5.3" needs a harness prefix (pi:… or claude:…)`}},
		{"unknown harness prefix", "steps:\n  e2e:\n    fix: { models: [codex:a] }\n", []string{`steps.e2e.fix.models[0]: "codex:a" has an unknown harness prefix "codex"`}},
		{"empty project entry", "steps:\n  e2e:\n", nil},
		{"null list", "steps:\n  e2e:\n    fix:\n", nil},
		{"entry a scalar", "steps:\n  e2e:\n    default: abc\n", []string{"steps.e2e.default: must be `{ models: [...] }`"}},
		{"empty model id", "steps:\n  e2e:\n    default: { models: [\"\"] }\n", nil},
		{"legacy models as a scalar", "models: 42\n", []string{`unknown top-level key "models"`}},
		{"project not mapped to this workspace", "steps:\n  w079ghost:\n    default: { models: [pi:a] }\n", []string{"w079ghost", "not mapped to this workspace"}},
		{"duplicate project key", "steps:\n  e2e:\n    default: { models: [pi:a] }\n  w079dup:\n    default: { models: [pi:a] }\n  w079dup:\n    default: { models: [pi:b] }\n", []string{"w079dup"}},
		{"duplicate list key", "steps:\n  e2e:\n    default: { models: [pi:a] }\n    default: { models: [pi:b] }\n", []string{"default"}},
	}
	// Each case's submit is refused synchronously (checked per case);
	// the stability half — every refused WI stays queued with no capsule
	// — then runs over ONE shared window that opens after the last
	// refusal, so every case is watched at least w079RefusalWindow. The
	// daemon runs without --auto and a refused submit is never enqueued,
	// so nothing but a new submit could claim a refused WI later.
	var refused []w079RefusedWI
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w079Yaml(t, ws, c.yaml)
			wi := w113NewWI(t, inst, "e2e", "w079 invalid yaml: "+c.name, envelopeAutopilot())
			out, code := d.pf(inst, "submit", wi)
			w079RefusalOutput(t, wi, out, code, append([]string{".polyforge.yaml"}, c.needles...)...)
			refused = append(refused, w079RefusedWI{name: c.name, wi: wi, capDir: w079Capsule(t, inst, ws, wi)})
			d.pfWant(inst, -1, "ops", "status")
		})
	}
	w079StayQueued(t, d, inst, refused)
	d.pfWant(inst, -1, "ops", "status")
}

// TestW079FixYamlAndResubmitIsClean: a refusal leaves the WI queued with
// no capsule; after the operator fixes the file, resubmitting runs it
// cleanly under the fixed yaml — exactly one capsule.
func TestW079FixYamlAndResubmitIsClean(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    default: { models: [pi:w079-ok] }\n   BAD KEY\n")
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e"}})

	wi := w113NewWI(t, inst, "e2e", "w079 fix-and-resubmit cycle", envelopeAutopilot())
	out, code := d.pf(inst, "submit", wi)
	w079Refused(t, d, inst, ws, wi, out, code, ".polyforge.yaml")
	if ents, err := os.ReadDir(filepath.Join(ws, "capsules")); err == nil && len(ents) != 0 {
		t.Fatalf("the refused submit left capsule husks: %v", ents)
	}

	w079Yaml(t, ws, "steps:\n  e2e:\n    default: { models: [pi:w079-reclaimed] }\n")
	capDir := w079Run(t, d, inst, ws, wi)
	if got := w113TopLevel(t, filepath.Join(ws, "capsules")); len(got) != 1 || got[0] != wiPathName(t, inst, wi) {
		t.Fatalf("capsules dir holds %v, want exactly [%s]", got, wi)
	}
	if got := w079Model(t, capDir, "grill"); got != "w079-reclaimed" {
		t.Fatalf("post-fix grill model %q, want w079-reclaimed", got)
	}
}

// TestW079InvalidYamlOfOneWorkspaceDoesNotBlockAnother: a bad file in one
// workspace refuses only that workspace's projects; the daemon keeps
// serving the other.
func TestW079InvalidYamlOfOneWorkspaceDoesNotBlockAnother(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "demo-ops")
	wsA, wsB := filepath.Join(t.TempDir(), "wsA"), filepath.Join(t.TempDir(), "wsB")
	for _, p := range []string{wsA, wsB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w079Yaml(t, wsA, "projects: {}\n")
	d := w079Setup(t, inst, w113WS{key: wsA, projects: []string{"e2e"}}, w113WS{key: wsB, projects: []string{"demo-ops"}})
	wiA := w113NewWI(t, inst, "e2e", "w079 bad workspace", envelopeAutopilot())
	out, code := d.pf(inst, "submit", wiA)
	w079Refused(t, d, inst, wsA, wiA, out, code, ".polyforge.yaml")
	w079Run(t, d, inst, wsB, w113NewWI(t, inst, "demo-ops", "w079 good workspace", envelopeAutopilot()))
}

// w079ProviderErr is the stderr text a crashing fake harness leaves behind:
// a MODEL-class reason, so the driver's exit classification advances the
// candidate chain (v2-converge 2.3 — a bare crash is an unclassified exit
// and retries the same model). Since polyforge#147 D7 the throttle texts
// (429 / overloaded / quota / ...) are their own, NON-model class that
// backs off on the same model, so this fixture names an unresolvable
// model instead.
const w079ProviderErr = `upstream provider error: model "fake/w079-missing" not found (fake harness injected)`

// TestW079RotationAdvancesAndCapsOnEffectiveList pins the rotation
// composition on the post-precedence list: two provider-class infra
// failures advance the pointer along the yaml per-project step-id list (two
// crashes are inside the recovery budget; a third would trip the storm
// guard), and the pin is capped at the list end — never beyond, never
// wrapping. RED pre-implementation (the effective list is the bundle's).
func TestW079RotationAdvancesAndCapsOnEffectiveList(t *testing.T) {
	cases := []struct {
		name      string
		yamlEntry string
		want      string
	}{
		{"two failures pin the third entry", "fix: { models: [pi:w079r-a, pi:w079r-b, pi:w079r-c] }", "w079r-c"},
		{"two failures on a two-entry list cap at the end", "fix: { models: [pi:w079r-a, pi:w079r-b] }", "w079r-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inst := newV1Install(t)
			ws := t.TempDir()
			w079Yaml(t, ws, "steps:\n  e2e:\n    "+c.yamlEntry+"\n")
			cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
				inst.url, inst.adminKey, "",
				[]w113WS{{key: ws, projects: []string{"e2e"}}})
			d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
			d.start()
			defer d.stop()

			wi := w113NewWI(t, inst, "e2e", "w079 rotation on the effective list: "+c.name,
				[]map[string]any{
					{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
					{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "before", "crash_stderr": w079ProviderErr},
					{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "before", "crash_stderr": w079ProviderErr},
					{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
					{"do": "envelope", "id": "verification", "outcome": "completed"},
				})
			d.pfWant(inst, -1, "submit", wi)
			capDir := w079Capsule(t, inst, ws, wi)
			w113WaitCapsule(t, capDir, 90*time.Second)
			w113WaitCompleted(t, capDir, 300*time.Second)
			inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

			providerInfra, _ := w079AttemptStats(t, capDir, "fix")
			if providerInfra != 2 {
				t.Fatalf("fix archived %d provider-class infra failures, want 2 (the two crash-before dispatches)", providerInfra)
			}
			if got := w079Model(t, capDir, "fix"); got != c.want {
				t.Fatalf("fix model %q, want %q — rotation must advance along the yaml-derived effective list and cap at its end", got, c.want)
			}
		})
	}
}

// TestW079GateFailRewindResetsEffectiveHead pins INV-1g's composition
// with the yaml-derived list: one provider failure advances the pin to
// the list's second entry, then a gate FAIL on verification rewinds to
// fix and RESETS the pin to the effective list's head. RED
// pre-implementation.
func TestW079GateFailRewindResetsEffectiveHead(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:w079g-a, pi:w079g-b] }\n")
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
	d.start()
	defer d.stop()

	verdict := map[string]any{
		"error":   "w079 injected gate verdict (fake harness)",
		"verdict": "FAIL",
	}
	wi := w113NewWI(t, inst, "e2e", "w079 gate-fail rewind resets the effective head",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "before", "crash_stderr": w079ProviderErr},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		})
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	providerInfra, gateRewind := w079AttemptStats(t, capDir, "fix")
	if providerInfra != 1 || gateRewind != 1 {
		t.Fatalf("fix attempts: %d provider infra, %d gate-fail rewinds; want 1/1 — the archived history the recomputation indexes", providerInfra, gateRewind)
	}
	if got := w079Model(t, capDir, "fix"); got != "w079g-a" {
		t.Fatalf("fix model after the rewind %q, want the effective list head w079g-a (a gate FAIL rewind resets the pointer)", got)
	}
}

// TestW079RotationUntouchedWithoutYaml pins the kept face of the
// rotation criterion: with NO yaml, the exact same crash + gate-fail
// rewind ladder behaves exactly as at HEAD — the pointer advances on
// the provider failure, the rewind resets to the bundle step-level
// list's head. Green before AND after the change.
func TestW079RotationUntouchedWithoutYaml(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
	d.start()
	defer d.stop()

	verdict := map[string]any{
		"error":   "w079 injected gate verdict (fake harness)",
		"verdict": "FAIL",
	}
	wi := w113NewWI(t, inst, "e2e", "w079 rotation untouched without a yaml",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "before", "crash_stderr": w079ProviderErr},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		})
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	providerInfra, gateRewind := w079AttemptStats(t, capDir, "fix")
	if providerInfra != 1 || gateRewind != 1 {
		t.Fatalf("fix attempts: %d provider infra, %d gate-fail rewinds; want 1/1", providerInfra, gateRewind)
	}
	if got := w079Model(t, capDir, "fix"); got != w079DefaultsHead {
		t.Fatalf("fix model after the rewind %q, want the bundle list head %q — rotation semantics must stay exactly as today", got, w079DefaultsHead)
	}
}

// TestW079RestartKeepsClaimTimeSnapshot: recovery rehydrates the
// claim-time effective-candidates snapshot from capsule.json and never
// re-reads the yaml — a mid-flight edit plus a daemon restart does not
// change the in-flight work item's models.
func TestW079RestartKeepsClaimTimeSnapshot(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:w079-m1, pi:w079-m2] }\n")
	sock := filepath.Join(shortTmp(t, "w079sock"), "d.sock")
	dataDir := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})

	wi := w113NewWI(t, inst, "e2e", "w079 restart keeps the snapshot",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 8000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d.start()
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 90*time.Second)
	time.Sleep(2 * time.Second)

	d.crash()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:w079-n1, pi:w079-n2] }\n")
	d2 := w113NewDaemon(t, inst, w113DOptConfig(cfg), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d2.start()
	defer d2.stop()
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	if got := w079Model(t, capDir, "fix"); got != "w079-m1" {
		t.Fatalf("fix model after the restart %q, want the claim-time head w079-m1 (recovery rehydrates the snapshot, never the edited yaml)", got)
	}
}

// ---------------------------------------------------------------------------
// pf file create — project inference from the cli.toml mapping.
// ---------------------------------------------------------------------------

// TestW079SingleProjectWorkspaceInfersDefault: a cwd inside a mapped
// workspace that lists exactly ONE project gets that project as the
// create's default.
func TestW079SingleProjectWorkspaceInfersDefault(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})

	goal := "w079 inferred default project goal"
	out := w079PfWant(t, inst, -1, w079Env{config: cfg, cwd: ws}, "file", "create", "--goal", goal)
	if !strings.Contains(out, goal) {
		t.Fatalf("pf file create (inferred): output does not carry the created item (output %q)", out)
	}
	items, err := listWorkItems(inst, "e2e", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Goal == goal {
			return
		}
	}
	t.Fatalf("the inferred create produced no work item in e2e (items %+v)", items)
}

// TestW079MultiProjectWorkspaceAsksForProject: a workspace mapping two
// projects never silently picks one.
func TestW079MultiProjectWorkspaceAsksForProject(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "w079second")
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e", "w079second"}}})
	out, code := w079Pf(t, inst, w079Env{config: cfg, cwd: ws}, "file", "create", "--goal", "w079 multi-project no pick")
	if code != 1 || !strings.Contains(out, "--project") {
		t.Fatalf("pf file create (two projects, no --project): exit %d, output %q; want exit 1 asking for --project", code, out)
	}
	for _, project := range []string{"e2e", "w079second"} {
		items, err := listWorkItems(inst, project, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("the errored create still produced work items in %s: %+v", project, items)
		}
	}
}

// TestW079UnmatchedCwdRequiredProjectErrorStands: a cwd matching no
// mapped workspace keeps the #113 required-project error.
func TestW079UnmatchedCwdRequiredProjectErrorStands(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	out, code := w079Pf(t, inst, w079Env{config: cfg, cwd: t.TempDir()}, "file", "create", "--goal", "w079 unmatched cwd no default")
	if code != 1 {
		t.Fatalf("pf file create (unmatched cwd): exit %d, want 1 (output %q)", code, out)
	}
	for _, tok := range []string{"--project", "required", "[workspace", "doctor"} {
		if !strings.Contains(out, tok) {
			t.Fatalf("the #113 error lost the %q guidance token (output %q)", tok, out)
		}
	}
}

// TestW079ExplicitProjectBypassesInference: an explicit --project wins,
// and a broken .polyforge.yaml never affects file creation.
func TestW079ExplicitProjectBypassesInference(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "w079second")
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e", "w079second"}}})
	w079Yaml(t, ws, "models: [\n")
	goal := "w079 explicit project over invalid yaml"
	w079PfWant(t, inst, -1, w079Env{config: cfg, cwd: ws}, "file", "create", "--project", "e2e", "--goal", goal)
	items, err := listWorkItems(inst, "e2e", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Goal == goal && it.ProjectName == "e2e" {
			return
		}
	}
	t.Fatalf("the explicit --project e2e create did not land in e2e (items %+v)", items)
}

// ---------------------------------------------------------------------------
// Skill documentation — the greppable floors.
// ---------------------------------------------------------------------------

func w079Doc(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{v1Root(t)}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestW079InitFragmentPointerAndWorkspaceInit: verbs/init.md carries the
// machine-level POINTER and the workspace-level init content (scan,
// selective sync, the [workspace] mapping, the models yaml).
func TestW079InitFragmentPointerAndWorkspaceInit(t *testing.T) {
	text := w079Doc(t, "skills", "polyforge", "verbs", "init.md")
	for _, tok := range []string{"README", "install.sh", ".polyforge.yaml", "[workspace", "scan", "sync", "models:"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(tok)) {
			t.Fatalf("verbs/init.md does not carry %q", tok)
		}
	}
}

// TestW079SkillTeachesModelsYaml: SKILL.md's session rule teaches the
// per-project models file and the pre-claim refusal, and none of the
// retired sections.
func TestW079SkillTeachesModelsYaml(t *testing.T) {
	text := w079Doc(t, "skills", "polyforge", "SKILL.md")
	for _, tok := range []string{".polyforge.yaml", "`models`", "DAEMON-414"} {
		if !strings.Contains(text, tok) {
			t.Fatalf("SKILL.md does not teach %q", tok)
		}
	}
	for _, retired := range []string{"workflow_defaults", "`projects` section", "drift"} {
		if strings.Contains(text, retired) {
			t.Fatalf("SKILL.md still teaches the retired %q", retired)
		}
	}
}

// TestW079RepoInventoryComesFromTheRegistry: SKILL.md teaches that the
// repo inventory (and clone URLs) come from the aihub project registry and
// that the cli.toml [workspace] mapping is the only placement source —
// never the yaml. Any verb fragment that names repos/<name> worktrees must
// say the same. (The ship fragment used to be the one that did; pf ship is
// gone — polyforge#166 — so the SKILL.md sentence is the pinned subject.)
func TestW079RepoInventoryComesFromTheRegistry(t *testing.T) {
	skill := strings.Join(strings.Fields(w079Doc(t, "skills", "polyforge", "SKILL.md")), " ")
	const rule = "The repo inventory and clone URLs always come from the aihub project registry; the cli.toml `[workspace]` mapping is the only placement source."
	if !strings.Contains(skill, rule) {
		t.Fatalf("SKILL.md no longer teaches the registry rule: %q", rule)
	}
	verbsDir := filepath.Join(v1Root(t), "skills", "polyforge", "verbs")
	ents, err := os.ReadDir(verbsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(verbsDir, e.Name()))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !strings.Contains(string(raw), "repos/") {
			continue
		}
		if !strings.Contains(string(raw), "registry") {
			t.Fatalf("%s routes repo paths but does not name the aihub registry as their source", e.Name())
		}
		if strings.Contains(string(raw), "`projects` section") {
			t.Fatalf("%s still teaches the retired yaml projects section", e.Name())
		}
	}
}

// TestW079InstallScriptStaysWorkspaceBlind pins the installer half of
// the init split: tools/install.sh never learns about workspaces — no
// .polyforge.yaml mention anywhere in the machine-install path. Green
// before AND after.
func TestW079InstallScriptStaysWorkspaceBlind(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "tools", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "polyforge.yaml") {
		t.Fatal("tools/install.sh learned about workspaces — machine install must stay workspace-blind")
	}
}

// TestW079NoRestartTeachingNearYamlMentions pins the deletion half of
// the fresh-read criterion: no doc that mentions the yaml teaches that
// a daemon restart is needed for an edit to take effect (a line naming
// both the yaml and a restart must carry a negation). Green before AND
// after (nothing mentions the yaml at HEAD).
func TestW079NoRestartTeachingNearYamlMentions(t *testing.T) {
	root := v1Root(t)
	roots := []string{
		filepath.Join(root, "skills"),
		filepath.Join(root, "workflows"),
		filepath.Join(root, "docs", "design", "polyforge-vnext"),
		filepath.Join(root, "README.md"),
	}
	negations := []string{" no ", "not ", "never ", "without", "无需", "不用", "不需要", " without "}
	restarts := []string{"restart", "重启"}
	var hits []string
	for _, r := range roots {
		info, err := os.Stat(r)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			raw, rerr := os.ReadFile(r)
			if rerr != nil {
				t.Fatal(rerr)
			}
			w079ScanRestartLines(r, string(raw), restarts, negations, &hits)
			continue
		}
		err = filepath.WalkDir(r, func(path string, de os.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if de.IsDir() || (!strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".sh")) {
				return nil
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			w079ScanRestartLines(path, string(raw), restarts, negations, &hits)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("docs teach a restart after yaml edits — the note must be deleted, not honored (%d hits):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}

// w079ScanRestartLines flags every line of one file that mentions the
// yaml AND a restart without carrying a negation.
func w079ScanRestartLines(path, text string, restarts, negations []string, hits *[]string) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "polyforge.yaml") {
			continue
		}
		hasRestart := false
		for _, r := range restarts {
			if strings.Contains(strings.ToLower(line), strings.ToLower(r)) {
				hasRestart = true
				break
			}
		}
		if !hasRestart {
			continue
		}
		negated := false
		for _, n := range negations {
			if strings.Contains(strings.ToLower(line), strings.ToLower(n)) {
				negated = true
				break
			}
		}
		if !negated {
			*hits = append(*hits, path+": "+strings.TrimSpace(line))
		}
	}
}

// ---------------------------------------------------------------------------
// Placement invariants.
// ---------------------------------------------------------------------------

// TestW079PlacementFollowsCliTomlOnly pins the placement
// invariant: a workspace whose yaml names a project the
// cli.toml mapping routes ELSEWHERE has no effect — the claim lands in
// the mapped workspace, the yaml is not a placement source. Kept face:
// green before AND after (placement reads cli.toml only).
func TestW079PlacementFollowsCliTomlOnly(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "demo-ops")
	wsX := filepath.Join(t.TempDir(), "wsX") // its yaml claims e2e
	wsY := t.TempDir()                       // the mapping routes e2e here
	if err := os.MkdirAll(wsX, 0o755); err != nil {
		t.Fatal(err)
	}
	w079Yaml(t, wsX, "steps:\n  e2e:\n    default: { models: [pi:w079-drift-model] }\n")
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{
			{key: wsX, projects: []string{"demo-ops"}},
			{key: wsY, projects: []string{"e2e"}},
		})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
	d.start()
	defer d.stop()

	wi := w113NewWI(t, inst, "e2e", "w079 placement follows cli.toml only", envelopeAutopilot())
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, wsY, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	// Placement went through the mapping; wsX's yaml never moved it.
	if _, err := os.Stat(w079Capsule(t, inst, wsX, wi)); err == nil {
		t.Fatalf("the claim landed in %s — the yaml moved capsule placement", w079Capsule(t, inst, wsX, wi))
	}
	// The read yaml is the MAPPED workspace's (wsY's, absent): the
	// models stay bundle-only, wsX's flow default governs nothing.
	if got := w079Model(t, capDir, "grill"); got != w079DefaultsHead {
		t.Fatalf("grill model %q, want the bundle defaults head %q (wsX's yaml must not govern wsY's claim)", got, w079DefaultsHead)
	}
}
