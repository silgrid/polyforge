package driver

// harness_test.go: the step harness in the driver — polyforge#175:
// each candidate is `<harness>:<model>` and its prefix is the harness of
// the dispatch that runs it (one chain may cross harnesses; the switch
// walks it in order), New refuses an unprefixed candidate, and the spawn
// adapter's harness-aware resume/fresh choice (#159).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
)

func harnessDriver(t *testing.T, list []string, attempts []capsule.Attempt) *Driver {
	t.Helper()
	d, err := harnessDriverErr(t, list, attempts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func harnessDriverErr(t *testing.T, list []string, attempts []capsule.Attempt) (*Driver, error) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "review", capsule.StepInProgress, attempts)
	cands := map[string][]string{}
	if list != nil {
		cands["review"] = list
	}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{
			{ID: "review", ContextPolicy: "fresh"},
		}, MaxTotal: 4, MaxPerStep: 2},
		Spawner: &fakeSpawner{},
		Log:     &eventLog{},
		Compose: composeOf(cands),
	})
	if err != nil {
		return nil, err
	}
	// the dispatch-time composition (polyforge#33), as the Run loop does it
	if err := validateCandidates(cands); err != nil {
		return nil, err
	}
	d.cands = cands
	return d, nil
}

func TestCandidatePrefixPicksHarness(t *testing.T) {
	mixed := []string{"claude:claude-opus-5-5", "pi:example-gateway/model-a", "claude:claude-sonnet-5"}
	model := infraAttempt(1, FailExitModel, mixed[0])
	cases := []struct {
		name      string
		list      []string
		attempts  []capsule.Attempt
		wantH     string
		wantModel string
	}{
		{"head runs on its own harness", mixed, nil, "claude", "claude-opus-5-5"},
		{"a model-class failure moves to the next candidate, across harnesses", mixed, []capsule.Attempt{model}, "pi", "example-gateway/model-a"},
		{"and back", mixed, []capsule.Attempt{model, infraAttempt(2, FailExitModel, mixed[1])}, "claude", "claude-sonnet-5"},
		{"the pointer caps at the last candidate", mixed, []capsule.Attempt{model, infraAttempt(2, FailExitModel, mixed[1]), infraAttempt(3, FailExitModel, mixed[2])}, "claude", "claude-sonnet-5"},
		{"the prefix, not the id shape, decides", []string{"pi:claude-opus-5-5"}, nil, "pi", "claude-opus-5-5"},
		{"the model keeps every further ':'", []string{"pi:ollama/llama3:8b"}, nil, "pi", "ollama/llama3:8b"},
		{"a present-empty list: pi's own default model", []string{}, nil, "pi", ""},
		{"no claim-time entry: pi's own default model", nil, nil, "pi", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := harnessDriver(t, c.list, c.attempts)
			sp, _, err := d.prepareDispatch("review")
			if err != nil {
				t.Fatal(err)
			}
			if sp.Harness != c.wantH || sp.Model != c.wantModel {
				t.Fatalf("dispatch harness %q model %q, want %q %q", sp.Harness, sp.Model, c.wantH, c.wantModel)
			}
		})
	}
}

// polyforge#176: the candidate's effort reaches the dispatch (and its
// step_start record) separately from the model id; switching walks
// whole candidates, so the effort changes with the candidate.
func TestCandidateEffortReachesDispatch(t *testing.T) {
	chain := []string{"claude:claude-opus-5-5:high", "pi:ollama/llama3:8b:low", "pi:ollama/llama3:8b"}
	for i, want := range []struct{ h, m, e string }{
		{"claude", "claude-opus-5-5", "high"},
		{"pi", "ollama/llama3:8b", "low"},
		{"pi", "ollama/llama3:8b", ""},
	} {
		var attempts []capsule.Attempt
		for n := 0; n < i; n++ {
			attempts = append(attempts, infraAttempt(n+1, FailExitModel, chain[n]))
		}
		d := harnessDriver(t, chain, attempts)
		sp, _, err := d.prepareDispatch("review")
		if err != nil {
			t.Fatal(err)
		}
		if sp.Harness != want.h || sp.Model != want.m || sp.Effort != want.e {
			t.Fatalf("candidate %d: dispatch %q %q %q, want %+v", i, sp.Harness, sp.Model, sp.Effort, want)
		}
	}
	if _, err := harnessDriverErr(t, []string{"pi:ok/x", "claude:m:minimal"}, nil); err == nil || !strings.Contains(err.Error(), `step review: models[1]: "claude:m:minimal": effort "minimal" is not accepted by claude`) {
		t.Fatalf("claude minimal: %v", err)
	}
}

// The switch decision walks the whole chain: a claude candidate's
// model-class failure switches onto the next candidate even when it is
// a pi one, and the switch starts a fresh session (the spawn adapter
// then carries the old transcript as reference).
func TestSwitchCrossesHarnesses(t *testing.T) {
	d := harnessDriver(t, []string{"claude:claude-opus-5-5", "pi:g/glm"}, nil)
	d.mu.Lock()
	dec := d.decideFailureLocked("review", d.exec.Steps["review"], &StepFailure{Cause: FailExitModel})
	d.mu.Unlock()
	if !dec.Switch || !dec.Fresh || dec.From != "claude:claude-opus-5-5" || dec.To != "pi:g/glm" {
		t.Fatalf("decision %+v, want a fresh switch claude:… → pi:…", dec)
	}
	d = harnessDriver(t, []string{"claude:claude-opus-5-5"}, nil)
	d.mu.Lock()
	dec = d.decideFailureLocked("review", d.exec.Steps["review"], &StepFailure{Cause: FailExitModel})
	d.mu.Unlock()
	if dec.Switch || !dec.Exhausted {
		t.Fatalf("decision %+v, want exhausted", dec)
	}
}

// The dispatch composition refuses a candidate without a valid `<harness>:`
// prefix.
func TestComposeRefusesUnprefixedCandidates(t *testing.T) {
	for in, want := range map[string]string{
		"g/glm":       `step review: models[1]: "g/glm" needs a harness prefix (pi:… or claude:…)`,
		"codex:gpt-6": `unknown harness prefix "codex"`,
		"claude:":     "names no model",
	} {
		if _, err := harnessDriverErr(t, []string{"pi:ok/x", in}, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: err %v, want %q", in, err, want)
		}
	}
}

// The adapter: a claude dispatch gets a minted session uuid and its own
// capsule transcript; a resume continues the previous claude session
// only when the native file exists; a harness change between dispatches
// never resumes.
func TestAdapterClaudeSessionAndResume(t *testing.T) {
	cfgDir := t.TempDir()
	ad, sp, capDir := spawnEnv(t)
	ad.ClaudeConfigDir = cfgDir

	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: "claude", Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := sp.last()
	if p.Harness != "claude" || !harness.ValidSessionID(p.HarnessSession) || p.Resume {
		t.Fatalf("fresh claude params %+v", p)
	}
	first := p.HarnessSession
	if m := run.(*stepRun).Meta(); m.Harness != "claude" || m.HarnessSession != first {
		t.Fatalf("meta %+v", m)
	}
	entries, _ := capsule.ReadSpawnJournal(capDir)
	if len(entries) != 0 {
		// the capture spawner never calls Journal; write the line the real
		// spawner would, so the resume lookup has its ledger
		t.Fatalf("unexpected journal %+v", entries)
	}
	if err := ad.appendSpawnJournal(p, "grill", "ab12cd34", 1, "2026-10-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	// resume without the native file: fresh, a NEW session, and the
	// previous capsule transcript (when readable) as read-only reference
	writeCapsuleFile(t, capDir, strings.TrimPrefix(p.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: "claude", Resume: true}); err != nil {
		t.Fatal(err)
	}
	p2, _ := sp.last()
	if p2.Resume || p2.HarnessSession == first {
		t.Fatalf("resume without a native session must start fresh: %+v", p2)
	}
	if !strings.Contains(p2.InitialPrompt, "Previous attempt (read-only reference)") || !strings.Contains(p2.InitialPrompt, p.TranscriptPath) {
		t.Fatalf("the degraded resume carries no reference to %s", p.TranscriptPath)
	}

	// the native file appears (under the capsule's project dir only): the
	// resume continues the SAME session, on a new capsule transcript
	other := filepath.Join(cfgDir, "projects", "-elsewhere", first+".jsonl")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: "claude", Resume: true}); err != nil {
		t.Fatal(err)
	}
	if p3, _ := sp.last(); p3.Resume {
		t.Fatal("resumed a same-uuid session of another cwd")
	}
	native := filepath.Join(cfgDir, "projects", harness.ClaudeProjectDirName(capDir), first+".jsonl")
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 3, Harness: "claude", Resume: true}); err != nil {
		t.Fatal(err)
	}
	p, _ = sp.last()
	if !p.Resume || p.HarnessSession != first {
		t.Fatalf("resume must continue session %s: %+v", first, p)
	}
	if p.TranscriptPath == entries0Transcript(t, capDir) {
		t.Fatalf("a claude resume writes its own capsule transcript, got the previous one %s", p.TranscriptPath)
	}

	// the step switched to pi: a pi resume never picks up the claude line,
	// but gets its transcript as reference
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 4, Harness: "pi", Resume: true}); err != nil {
		t.Fatal(err)
	}
	p4, _ := sp.last()
	if p4.Resume || p4.HarnessSession != "" {
		t.Fatalf("a pi dispatch after a claude one must start fresh: %+v", p4)
	}
	if !strings.Contains(p4.InitialPrompt, "Previous attempt (read-only reference)") {
		t.Fatal("the harness-change fallback carries no reference")
	}
}

func entries0Transcript(t *testing.T, capDir string) string {
	t.Helper()
	entries, err := capsule.ReadSpawnJournal(capDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("journal %v %v", entries, err)
	}
	return entries[0].Transcript
}

// The driver's own bundle parse no longer knows a `harness` key (it is
// refused by the claim-side frontmatter read, polyforge#175); an old
// capsule snapshot that still carries one loads, its key ignored.
func TestWorkflowParseIgnoresRetiredHarnessKey(t *testing.T) {
	wf, err := parseWorkflow([]byte("defaults:\n  harness: claude\nsteps:\n  - { id: a }\n  - { id: b, harness: pi }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflow(wf); err != nil {
		t.Fatal(err)
	}
}

// polyforge#175 ruling 3: a candidate switch onto another harness is a
// FRESH dispatch (the switch decision) on the new harness; the previous
// dispatch's capsule transcript — the other harness's JSONL — rides the
// prompt as read-only reference, both ways.
func TestAdapterCrossHarnessSwitchCarriesReference(t *testing.T) {
	for _, c := range []struct{ from, to string }{{"claude", "pi"}, {"pi", "claude"}} {
		t.Run(c.from+"→"+c.to, func(t *testing.T) {
			ad, sp, capDir := spawnEnv(t)
			ad.ClaudeConfigDir = t.TempDir()
			if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: c.from, Model: "m-" + c.from}); err != nil {
				t.Fatal(err)
			}
			p, _ := sp.last()
			if err := ad.appendSpawnJournal(p, "grill", "ab12cd34", 1, "2026-10-15T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			writeCapsuleFile(t, capDir, strings.TrimPrefix(p.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
			if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: c.to, Model: "m-" + c.to, Fresh: true}); err != nil {
				t.Fatal(err)
			}
			p2, _ := sp.last()
			if p2.Resume || p2.Harness != c.to || p2.Model != "m-"+c.to || p2.TranscriptPath == p.TranscriptPath {
				t.Fatalf("switch dispatch %+v", p2)
			}
			if !strings.Contains(p2.InitialPrompt, "Previous attempt (read-only reference)") || !strings.Contains(p2.InitialPrompt, p.TranscriptPath) {
				t.Fatalf("the cross-harness switch carries no reference to %s", p.TranscriptPath)
			}
		})
	}
}

// A claude→pi switch right after the claude dispatch ended: the capsule
// copy of the claude session is written asynchronously at the commit
// boundary and may not exist yet — the reference is copied from the
// native session at the switch, so the next harness always gets it.
func TestCrossHarnessReferenceDoesNotWaitForTheCommitBoundaryCopy(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	cfgDir := t.TempDir()
	ad.ClaudeConfigDir = cfgDir
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: "claude", Model: "m-claude"}); err != nil {
		t.Fatal(err)
	}
	p, _ := sp.last()
	if err := ad.appendSpawnJournal(p, "grill", "ab12cd34", 1, "2026-10-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(cfgDir, "projects", harness.ClaudeProjectDirName(capDir), p.HarnessSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	const conv = "{\"type\":\"user\",\"message\":\"the claude conversation\"}\n"
	if err := os.WriteFile(native, []byte(conv), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.TranscriptPath); err == nil {
		t.Fatal("precondition: the capsule copy must not exist yet")
	}
	for _, next := range []StepSpawn{
		{StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: "pi", Model: "m-pi", Resume: true},
		{StepID: "grill", WIID: "wi_sp", SpawnN: 3, Harness: "pi", Model: "m-pi", Fresh: true},
	} {
		_ = os.Remove(p.TranscriptPath)
		if _, err := ad.SpawnStep(context.Background(), next); err != nil {
			t.Fatal(err)
		}
		p2, _ := sp.last()
		if !strings.Contains(p2.InitialPrompt, "Previous attempt (read-only reference)") || !strings.Contains(p2.InitialPrompt, p.TranscriptPath) {
			t.Fatalf("spawn %d (resume=%v fresh=%v) carries no reference to %s", next.SpawnN, next.Resume, next.Fresh, p.TranscriptPath)
		}
		if raw, err := os.ReadFile(p.TranscriptPath); err != nil || string(raw) != conv {
			t.Fatalf("the referenced capsule transcript = %q (%v), want the native session", raw, err)
		}
	}
}
