package cli

// w36_by_key_test.go (u4): the workspace yaml's per-step mode leaf
// `steps.<project>.<step>.by`. `pf config set` writes it only for a step
// some lane of the workspace declares `human_mergeable: true` — any other
// step is refused naming the lane key, the file untouched — and only the
// one mode `human` (anything else is refused naming what it takes); a
// write that would leave the entry declaring both `skip` and `by` is
// refused by the strict loader's rule; the write touches the one leaf
// (comments and the entry's models survive), `unset` drops the leaf and
// an entry it left empty; the key's class is next-claim (the mode is
// fixed when a work item is claimed); `pf workflows` shows the lane's
// human_mergeable steps and each mapped project's declared by.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestW36ConfigWritesTheByLeaf(t *testing.T) {
	const before = "# keep me\nsteps:\n  alpha:\n    ship:\n      models: [\"pi:s\"]\n    code: { models: [\"pi:c\"] }\n"
	f := newWfFixture(t, before)
	writeLane(t, f.workflowsDir, "shipit", "steps:\n  - id: build\n  - id: ship\n    human_mergeable: true\n  - id: land\n    skippable: true\n    model_candidates: [pi:si-land]\n")
	path := filepath.Join(f.ws, ".polyforge.yaml")

	// a human_mergeable step: one leaf line, nothing else moves
	sha, _ := fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.ship.by", "human", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("set steps.alpha.ship.by human on a human_mergeable step exited %d: %s", code, stderr)
	}
	want := strings.Replace(before, "    ship:\n", "    ship:\n      by: human\n", 1)
	if _, now := fileHash(t, path); string(now) != want {
		t.Fatalf("the by write is not the one leaf line:\n--- got\n%s--- want\n%s", now, want)
	}

	// a step no lane declares human_mergeable: refused, naming the lane
	// key, the file intact
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.code.by", "human", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "human_mergeable") {
		t.Fatalf("a by of a step no lane declares human_mergeable must be refused naming the lane key: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatalf("a refused by changed the file:\n%s", now)
	}

	// a value other than the one mode: refused, the file intact
	code, _, stderr = runPf(t, f.env, "config", "set", "steps.alpha.ship.by", "lane", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "takes `human`") {
		t.Fatalf("a by value other than human must be refused naming what it takes: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatalf("a refused by value changed the file:\n%s", now)
	}

	// the class of the key: the mode is fixed when a work item is claimed
	if got := WorkspaceConfigClass("steps.alpha.ship.by"); got != ConfigNextClaim {
		t.Errorf("the class of steps.<p>.<s>.by is %q, want %q (fixed when a work item is claimed)", got, ConfigNextClaim)
	}

	// pf workflows: the lane's human_mergeable step, and the mapped
	// project's declared by
	code, stdout, stderr := runPf(t, f.env, "--json", "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("pf workflows exited %d: %s", code, stderr)
	}
	doc := decodeWorkflows(t, stdout)
	b := findBundle(doc, "shipit")
	if b == nil {
		t.Fatalf("pf workflows lists no lane shipit:\n%s", stdout)
	}
	by := map[string]string{}
	hm := map[string]bool{}
	for _, s := range b.Steps {
		hm[s.ID] = s.HumanMergeable
		for p, m := range s.By {
			by[p] = m
		}
	}
	if !hm["ship"] || hm["build"] || hm["land"] {
		t.Fatalf("the lane's human_mergeable steps = %v, want ship only", hm)
	}
	if by["alpha"] != "human" {
		t.Fatalf("the mapped project's declared by = %v, want alpha: human", by)
	}

	// unset: the leaf goes, an entry left empty goes with it
	sha, _ = fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "unset", "steps.alpha.ship.by", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("unset steps.alpha.ship.by exited %d: %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != before {
		t.Fatalf("unset did not restore the file (the leaf, and the entry it left empty, removed; models kept):\n--- got\n%s--- want\n%s", now, before)
	}
}

// (u4: the by leaf's red runs on this tree; the write, its refusals and the display land in C_1.)
