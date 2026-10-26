package test

// s12_submit_models_test.go: `pf submit <wi> --models a,b,c` — the manual
// per-work-item candidate list, the top of the five-level precedence
// (manual > yaml step id > bundle step > yaml default > bundle defaults).
// Observed through the real pf/aihub/fakepi binaries: the model pinned on
// each step in execution.json and the capsule.json audit.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestS12ManualModelsBeatYamlAndAdvanceOnModelFailure: a WI submitted with
// --models dispatches every step with the manual head (over the yaml and
// the bundle), and a provider-class failure advances along the manual list.
func TestS12ManualModelsBeatYamlAndAdvanceOnModelFailure(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    default: { models: [pi:s12-yaml-def] }\n    fix: { models: [pi:s12-yaml-code] }\n")
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e"}})

	wi := w113NewWI(t, inst, "e2e", "s12 manual models",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "before", "crash_stderr": w079ProviderErr},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		})
	d.pfWant(inst, -1, "submit", wi, "--models", "pi:s12-m1, pi:s12-m2,pi:s12-m3")
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	for step, want := range map[string]string{"grill": "s12-m1", "verification": "s12-m1", "fix": "s12-m2"} {
		if got := w079Model(t, capDir, step); got != want {
			t.Fatalf("step %s model %q, want %q (manual list; fix advanced once on the provider failure)", step, got, want)
		}
	}
	// capsule.json records NO candidate audit at all (the effective-candidates
	// field was deleted with the audit's dead-code sweep; the manual list is
	// the daemon-private Start note, the yaml governs dispatches directly).
	// The models the steps dispatched under (above) are the behavior pin.
	if raw := w079CapsuleRaw(t, capDir); strings.Contains(raw, "manual_candidates") || strings.Contains(raw, "s12-yaml") || strings.Contains(raw, "effective_candidates") {
		t.Fatalf("capsule.json must not carry a candidate audit (manual, yaml, or effective): %s", raw)
	}

	// an ordinary submit of another WI is unaffected: the yaml chain governs.
	wi2 := w113NewWI(t, inst, "e2e", "s12 plain submit keeps yaml", envelopeAutopilot())
	cap2 := w079Run(t, d, inst, ws, wi2)
	if got := w079Model(t, cap2, "grill"); got != "s12-yaml-def" {
		t.Fatalf("plain submit grill model %q, want the yaml default", got)
	}
}

// TestS12ManualModelsBadInputRefused: the CLI refuses a malformed list
// before the daemon is involved.
func TestS12ManualModelsBadInputRefused(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	d := w079Setup(t, inst, w113WS{key: ws, projects: []string{"e2e"}})
	wi := w113NewWI(t, inst, "e2e", "s12 bad models", envelopeAutopilot())
	for _, bad := range []string{"pi:a,,pi:b", "pi:a,pi:a", ",", "codex:a", "claude:"} {
		out := d.pfWant(inst, 1, "submit", wi, "--models", bad)
		if !strings.Contains(out, "--models") {
			t.Fatalf("--models %q: refusal output %q does not name the flag", bad, out)
		}
	}
	// polyforge#175: an unprefixed candidate is refused up front, naming
	// it and the two prefixes
	out := d.pfWant(inst, 1, "submit", wi, "--models", "pi:a/b,example-gateway/model-a")
	if !strings.Contains(out, `submit: --models: "example-gateway/model-a" needs a harness prefix (pi:… or claude:…)`) {
		t.Fatalf("unprefixed --models refusal: %q", out)
	}
	// polyforge#176: an effort the candidate's harness does not take is
	// refused up front too
	out = d.pfWant(inst, 1, "submit", wi, "--models", "pi:a/b:minimal,claude:claude-opus-5-5:minimal")
	if !strings.Contains(out, `submit: --models: "claude:claude-opus-5-5:minimal": effort "minimal" is not accepted by claude (it accepts low|medium|high|xhigh|max)`) {
		t.Fatalf("claude effort --models refusal: %q", out)
	}
	if st := inst.wiStatus(t, wi); st != "queued" {
		t.Fatalf("a refused submit moved the WI to %q", st)
	}
}

// TestS12ManualModelsSurviveDaemonRestart: a daemon crash mid-run keeps
// the manual list (the capsule snapshot), even with the yaml edited.
func TestS12ManualModelsSurviveDaemonRestart(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:s12-y1, pi:s12-y2] }\n")
	sock := filepath.Join(shortTmp(t, "s12sock"), "d.sock")
	dataDir := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	wi := w113NewWI(t, inst, "e2e", "s12 restart keeps manual models",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 8000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d.start()
	d.pfWant(inst, -1, "submit", wi, "--models", "pi:s12-m1,pi:s12-m2")
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 90*time.Second)
	time.Sleep(2 * time.Second)

	d.crash()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:s12-n1] }\n")
	d2 := w113NewDaemon(t, inst, w113DOptConfig(cfg), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d2.start()
	defer d2.stop()
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	for _, step := range []string{"grill", "fix", "verification"} {
		if got := w079Model(t, capDir, step); got != "s12-m1" {
			t.Fatalf("step %s model %q after the restart, want the manual head s12-m1", step, got)
		}
	}
}

// TestS12ManualModelsSurviveReclaim: a blocked WI (capsule lost while the
// daemon was down) is re-claimed by `pf resume` without any --models; the
// list comes back from the daemon's own record (manual-models.json in its
// data dir), never from the capsule chain.
func TestS12ManualModelsSurviveReclaim(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s12 reclaim keeps manual models",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed", "ms": 120000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi, "--models", "pi:s12-r1,pi:s12-r2")
	waitStepStatus(t, d, wi, "verification", "in_progress", 90*time.Second)
	capA := d.capsuleDir(wi)
	// The verification step_start (in_progress with its dispatch ordinal)
	// reaches the hub by its own push: crashing before it lands would make
	// the re-claim re-derive that dispatch and replay its 120s action.
	waitUntil(t, 60*time.Second, "the hub chain never carried capsule.json, the fix step done and the verification step_start", func() bool {
		if !inst.bareHasPath(t, wi, "capsule.json") || !inst.bareHasPath(t, wi, "runtime-state/execution.json") {
			return false
		}
		var x executionView
		if json.Unmarshal([]byte(inst.bareFileAtMain(t, wi, "runtime-state/execution.json")), &x) != nil {
			return false
		}
		fix, ver := x.Steps["fix"], x.Steps["verification"]
		return fix != nil && fix.Status == "done" &&
			ver != nil && ver.Status == "in_progress" && ver.Dispatch > 0
	})

	if strings.Contains(inst.bareFileAtMain(t, wi, "capsule.json"), "manual_candidates") {
		t.Fatal("the published capsule must not carry the manual list")
	}
	if raw, err := os.ReadFile(filepath.Join(d.dataDir, "manual-models.json")); err != nil || !strings.Contains(string(raw), "s12-r1") {
		t.Fatalf("the daemon-side manual record is missing: %v %s", err, raw)
	}

	d.crash()
	if err := os.RemoveAll(capA); err != nil {
		t.Fatal(err)
	}
	d2 := restartSameDaemon(t, d, "daemon2.log")
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)

	d2.pf(t, "resume", wi)
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)
	capDir := d2.capsuleDir(wi)
	for _, step := range []string{"verification"} {
		if got := w079Model(t, capDir, step); got != "s12-r1" {
			t.Fatalf("step %s model %q after the re-claim, want the manual head s12-r1", step, got)
		}
	}
}
