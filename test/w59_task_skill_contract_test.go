package test

// w59_task_skill_contract_test.go: text contract for workbench#59.
//
// Work item #59 routes the `pf task` CLI face (shipped by workbench#53,
// PR #706) into the routing skill: one intent-table row, a new
// verbs/task.md fragment and the verb count in the SKILL.md description.
// The change is skill text plus this pin — no endpoint, verb, flag or
// protocol moves — so the only mechanical contract is the shipped text
// itself.
//
// It exists for two reasons the human eye cannot guard: (1) the skill
// description is the ONLY text a harness loads unconditionally, so a
// session only discovers `task` if the description advertises it; (2) that
// description sits at the Agent Skills 1024-byte limit, and the `task`
// insertion plus the verb-count bump can silently push it over (the W246
// guard, which runs in the same bar, owns the byte assertion).
//
// Red on the base tree: neither verbs/task.md nor the description row
// exists there. Green only once the routing text shipped. The fragment
// marker set is pinned to the surface facts #53 actually emits (no `--wi`:
// that flag does not exist, so it must never reappear here).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	w59SkillRel = filepath.Join("skills", "polyforge", "SKILL.md")
	w59FragRel  = filepath.Join("skills", "polyforge", "verbs", "task.md")
)

func TestW59TaskSkillContract(t *testing.T) {
	root := v1Root(t)

	raw, err := os.ReadFile(filepath.Join(root, w59SkillRel))
	if err != nil {
		t.Fatalf("workbench#59: %s is the routing skill the task intent must be routable from: %v", w59SkillRel, err)
	}
	skill := string(raw)

	// 1. The description is the only text a session loads unconditionally:
	// it must name `task` and carry the bumped verb count. The W246 guard in
	// this same bar pins the 1024-byte/char limit of the same value.
	desc, err := w246FrontmatterDescription(skill)
	if err != nil {
		t.Fatalf("workbench#59: %s's frontmatter must parse as strict YAML (the description carries the task advertising): %v", w59SkillRel, err)
	}
	for _, tok := range []string{"15 routing verbs", "task"} {
		if !strings.Contains(desc, tok) {
			t.Errorf("workbench#59: the skill description must advertise the task verb — the marker %q is missing; the description is the only text a harness loads unconditionally, so without it a session never discovers `pf task`", tok)
		}
	}

	// 2. The intent table routes the intent to the fragment, and the body's
	// verb count matches the description. The trigger markers are pinned on
	// intent (not prose order), so a later wording polish does not make the
	// pin brittle.
	for _, tok := range []string{"`verbs/task.md`", "起个任务", "只读跑腿"} {
		if !strings.Contains(skill, tok) {
			t.Errorf("workbench#59: %s must route the task intent to `verbs/task.md` — the marker %q is missing; without the intent-table row \"起个任务 / 派个只读跑腿\" matches nothing", w59SkillRel, tok)
		}
	}
	if !strings.Contains(skill, "decide which of the 15 routing verbs") {
		t.Errorf("workbench#59: %s's body must say 15 routing verbs — the description and the body sentence must not disagree on the verb count", w59SkillRel)
	}

	// 3. The fragment exists and carries the shipped surface's facts: the
	// command shapes, the budgets and ceilings, the id shape, the write-verb
	// guard, the four terminal states, retention and the presentation
	// discipline.
	fragRaw, err := os.ReadFile(filepath.Join(root, w59FragRel))
	if err != nil {
		t.Fatalf("workbench#59: %s must exist — the fragment that owns the task routing discipline; the intent table has no row to point at without it: %v", w59FragRel, err)
	}
	frag := string(fragRaw)
	for _, tok := range []string{
		"pf task spawn --project",
		"pf task list",
		"pf task show",
		"pf task cancel",
		"--max-duration",
		"--max-tokens",
		"20m",
		"300000",
		"60m",
		"1000000",
		"is above the 60m ceiling",
		"is above the 1000000 ceiling",
		"is not a duration",
		"is not an integer",
		"--max-duration … must be positive (got …)",
		"--max-tokens … must be positive (got …)",
		"task_",
		"steps.<project>.task.models",
		"PF_TASK_ID",
		"DAEMON-403",
		"DAEMON-414",
		"cannot report token usage",
		"interrupted",
		"capped",
		"7 days",
		"newest 50",
		"does not trigger an agent turn",
	} {
		if !strings.Contains(frag, tok) {
			t.Errorf("workbench#59: %s must carry the task surface marker %q — the fragment exists but does not teach the surface workbench#53 shipped", w59FragRel, tok)
		}
	}

	// 3b. A budget above its ceiling is a LOCAL refusal: `pf task spawn`
	// parses and validates `--max-duration` / `--max-tokens` before any wire
	// call and exits 1 with a `pf: task spawn: …` line. DAEMON-400 exists on
	// this surface only for a raw UDS request, which the fragment never
	// teaches, so no failure-table row may pair the code with a ceiling.
	for _, line := range strings.Split(frag, "\n") {
		if strings.Contains(line, "DAEMON-400") && strings.Contains(line, "ceiling") {
			t.Errorf("workbench#59: %s pairs DAEMON-400 with a budget ceiling — `pf task spawn` refuses an over-ceiling budget locally (exit 1, `is above the … ceiling`), before any wire call; the typed code only answers a raw UDS request", w59FragRel)
		}
	}
	if !strings.Contains(frag, "over-ceiling or malformed budget") {
		t.Errorf("workbench#59: %s must list an over-ceiling or malformed budget among the local `pf: task …` refusals — otherwise the closing paragraph contradicts the failure table", w59FragRel)
	}

	// 3c. The `cannot report token usage` branch fires at the FIRST such
	// candidate: task/manager.go's Spawn walks the resolved list and refuses
	// on any one candidate whose harness fails SupportsUsage, even when the
	// others report. A row claiming "every candidate" would make the reader
	// believe adding a reporting candidate fixes it, which it does not — the
	// named candidate has to be removed or replaced.
	for _, line := range strings.Split(frag, "\n") {
		if strings.Contains(line, "cannot report token usage") && strings.Contains(line, "every candidate") {
			t.Errorf("workbench#59: %s says the token-usage refusal needs EVERY candidate to be non-reporting — `task spawn` refuses on the first candidate whose harness cannot report usage, even when the others report; the row must name that one candidate and tell the reader to remove or replace it", w59FragRel)
		}
	}

	// 4. `--wi` does not exist in the shipped `pf task` face (workbench#53
	// deleted it): the fragment must never teach it back.
	if strings.Contains(frag, "--wi") {
		t.Errorf("workbench#59: %s teaches --wi, which the shipped `pf task` CLI does not define — `--project <name>` is the only selector; teaching a nonexistent flag also fails TestSkillTaughtFlagsExistInCLI", w59FragRel)
	}
}
