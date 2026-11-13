package cli

// w521_skip_key_test.go: the workspace yaml's per-step
// switch `steps.<project>.<step>.skip`. `pf config set` writes it only
// for a step some lane of the workspace declares `skippable: true` — any
// other step is refused naming skippable, the file untouched — and only a
// bool; the write touches the one leaf (comments and the entry's models
// survive), `unset` drops the leaf and an entry it left empty. A skip-only
// entry changes no candidate chain, and the key's class is next-claim (the
// switch is fixed when a work item is claimed), the models key's still live.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// w521Effective answers the --cwd effective chains of one step of one
// lane, as JSON (the comparison is byte for byte).
func w521Effective(t *testing.T, f wfFixture, lane, step string) string {
	t.Helper()
	code, stdout, stderr := runPf(t, f.env, "--json", "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("pf workflows exited %d: %s", code, stderr)
	}
	b := findBundle(decodeWorkflows(t, stdout), lane)
	if b == nil {
		t.Fatalf("pf workflows lists no lane %s:\n%s", lane, stdout)
	}
	for _, s := range b.Steps {
		if s.ID == step {
			raw, _ := json.Marshal(s.Effective)
			return string(raw)
		}
	}
	t.Fatalf("lane %s lists no step %s:\n%s", lane, step, stdout)
	return ""
}

func TestW521ConfigSetSkip(t *testing.T) {
	const before = "# keep me\nsteps:\n  alpha:\n    ship:\n      models: [\"pi:s\"]\n    code: { models: [\"pi:c\"] }\n"
	f := newWfFixture(t, before)
	writeLane(t, f.workflowsDir, "shipit", "steps:\n  - id: build\n  - id: ship\n    skippable: true\n  - id: land\n    skippable: true\n    model_candidates: [pi:si-land]\n")
	path := filepath.Join(f.ws, ".polyforge.yaml")

	// a skippable step: one leaf line, nothing else moves
	sha, _ := fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.ship.skip", "true", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("set steps.alpha.ship.skip true on a skippable step exited %d: %s", code, stderr)
	}
	want := strings.Replace(before, "    ship:\n", "    ship:\n      skip: true\n", 1)
	if _, now := fileHash(t, path); string(now) != want {
		t.Fatalf("the skip write is not the one leaf line:\n--- got\n%s--- want\n%s", now, want)
	}

	// a step no lane declares skippable: refused, naming why, file intact
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.skip", "true", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "skippable") {
		t.Fatalf("a skip of a step no lane declares skippable must be refused naming skippable: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatalf("a refused skip changed the file:\n%s", now)
	}

	// not a bool: refused, file intact
	code, _, stderr = runPf(t, f.env, "config", "set", "steps.alpha.ship.skip", "notabool", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "skip") {
		t.Fatalf("a non-bool skip must be refused: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatalf("a refused non-bool skip changed the file:\n%s", now)
	}

	// a skip-only entry changes no candidate chain
	chain := w521Effective(t, f, "shipit", "land")
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.land.skip", "true", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("set steps.alpha.land.skip true exited %d: %s", code, stderr)
	}
	if _, now := fileHash(t, path); !strings.Contains(string(now), "    land:\n      skip: true\n") {
		t.Fatalf("the skip-only entry is not written:\n%s", now)
	}
	if got := w521Effective(t, f, "shipit", "land"); got != chain {
		t.Fatalf("a skip-only entry changed the step's chain: %s, was %s", got, chain)
	}

	// unset: the leaf goes; an entry left empty goes with it
	sha, _ = fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "unset", "steps.alpha.land.skip", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("unset steps.alpha.land.skip exited %d: %s", code, stderr)
	}
	sha, _ = fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "unset", "steps.alpha.ship.skip", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("unset steps.alpha.ship.skip exited %d: %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != before {
		t.Fatalf("unset did not restore the file (the leaf, and the entry it left empty, removed; models kept):\n--- got\n%s--- want\n%s", now, before)
	}

	if got := WorkspaceConfigClass("steps.alpha.ship.skip"); got != ConfigNextClaim {
		t.Errorf("the class of steps.<p>.<s>.skip is %q, want %q (fixed when a work item is claimed)", got, ConfigNextClaim)
	}
	if got := WorkspaceConfigClass("steps.alpha.ship.models"); got != ConfigLive {
		t.Errorf("the class of steps.<p>.<s>.models is %q, want %q", got, ConfigLive)
	}
}
