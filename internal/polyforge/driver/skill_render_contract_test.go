package driver

// skill_render_contract_test.go (owner ruling 2026-11-11, workflow
// contract v2 change 15): the skill rendering contract. Every shipped
// lane step that declares a `skill:` renders that skill into its ASSEMBLED
// prompt — the real dispatch (SpawnStep from a materialized capsule), not
// the skill file — and the prompt carries the skill's key rules: the owner
// is asked only through `pf step ask` (contract 3's `pf ask`); a command
// is never backgrounded and polled, nor given a timeout or deadline of the
// agent's own; and, where the skill has one, its same-tree rule (verify,
// evidence, build, review's later round — since workflow contract 4 the
// daemon stops on no unchanged tree: build hands one off only with a
// reason per point, and review judges from the heads its earlier section's
// header stamps whether the build changed anything). It is red when a step stops declaring its skill, when the
// driver stops rendering a skill, and when a skill loses one of these
// sentences. The test adds no production code.

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// shippedSkillSteps is every shipped lane step declaring a skill, by
// `<lane>/<step>`, with the skill it declares: a step that stops declaring
// its skill (or a new skill step nobody pinned) is a finding.
var shippedSkillSteps = map[string]string{
	"develop/build":        "build",
	"develop/review":       "review",
	"develop/sync":         "sync",
	"develop/verification": "verify",
	"develop/publish":      "publish",
	"develop/merge":        "merge",
	"micro/fix":            "micro-bounds",
	"micro/sync":           "sync",
	"micro/evidence":       "evidence",
	"micro/publish":        "publish",
	"micro/merge":          "merge",
	"integrate/collect":    "collect",
	"integrate/revert":     "revert",
	"integrate/full-gate":  "full-gate",
	"integrate/close":      "close",
}

// skillKeyRule is one skill's key rules, each a sentence of the skill
// itself (whitespace-flattened): how it asks the owner, that it neither
// backgrounds-and-polls a command nor gives one its own timeout or
// deadline, and its same-tree rule ("" = the skill has none).
type skillKeyRule struct {
	ask, foreground, sameTree string
}

// foregroundRule is the sentence every skill that runs a pre-written
// command carries in its Never list.
const foregroundRule = "run a command in the background and poll it, or give it a timeout or deadline of your own"

// skillKeyRules pins each shipped skill's key rules.
var skillKeyRules = map[string]skillKeyRule{
	"build": {
		ask:        "end a turn that needs the owner without `pf step ask`",
		foreground: "run a unit's command in the background and poll it — each run is a foreground call that ends with its exit code",
		sameTree:   "hand off a round a FAIL opened on an unchanged tree with an unchanged units.json and no reason per point",
	},
	"review": {
		ask:        "end a turn that needs the owner without `pf step ask`",
		foreground: foregroundRule,
		sameTree:   "When every task repo's `git -C repos/<name> rev-parse HEAD` equals the head that round judged and `sha256sum artifacts/build/units.json` equals that line, the build since changed nothing (the daemon does not stop on it — you judge it). Then do not run the facts again",
	},
	"sync": {ask: "end a turn that needs the owner without `pf step ask`", foreground: foregroundRule},
	"verify": {
		ask:        "end a turn that needs the owner without `pf step ask`",
		foreground: foregroundRule,
		sameTree:   "**Same-tree rule:** an earlier round's result whose `heads` equal every repo's current HEAD and whose `verdict` is `fail` is not run again",
	},
	"publish":      {ask: "ending a turn that needs the owner without `pf step ask`", foreground: "a command run in the background and polled, or given a timeout or deadline of your own"},
	"merge":        {ask: "end a turn that needs the owner without `pf step ask`", foreground: foregroundRule},
	"micro-bounds": {ask: "end a turn that needs the owner without `pf step ask`", foreground: foregroundRule},
	"evidence": {
		ask:        "end a turn that needs the owner without `pf step ask`",
		foreground: foregroundRule,
		sameTree:   "**Same-tree rule:** an earlier round's result whose `heads` equal every repo's current HEAD and whose `verdict` is `fail` is not run again",
	},
	"collect":   {ask: "end your turn needing the owner without a `pf step ask`", foreground: foregroundRule},
	"revert":    {ask: "end your turn needing the owner without a `pf step ask`", foreground: foregroundRule},
	"full-gate": {ask: "end your turn needing the owner without a `pf step ask`", foreground: foregroundRule},
	"close":     {ask: "end your turn needing the owner without a `pf step ask`", foreground: foregroundRule},
}

// skillsWithSameTree are the skills whose rule set must carry a same-tree
// rule (the owner's ruling names them).
var skillsWithSameTree = []string{"build", "evidence", "review", "verify"}

// skillStandingRule is the driver's own line every rendered `# Skill`
// section opens with (renderSkill): the commands run as given, never a
// Bash timeout.
const skillStandingRule = "Follow this skill as written: run each command line exactly as given (never rewrite or add arguments, never pass a Bash timeout)"

func TestShippedSkillStepsRenderTheirSkillsKeyRules(t *testing.T) {
	got := map[string]string{}
	dirs := shippedBundleDirs(t)
	for _, dir := range dirs {
		lane := filepath.Base(dir)
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatalf("%s: %v", lane, err)
		}
		for _, s := range wf.Steps {
			if s.Skill == "" {
				continue
			}
			key := lane + "/" + s.ID
			got[key] = s.Skill
			t.Run(key, func(t *testing.T) {
				sk, err := workflow.ReadSkill(dir, s.Skill)
				if err != nil {
					t.Fatalf("skill %s: %v", s.Skill, err)
				}
				prompt := w111Flat(spawnStepPrompt(t, lane, s.ID))
				body := w111Flat(sk.Body)
				// the skill is rendered: the # Skill section, the driver's
				// standing line, the whole body verbatim
				if !strings.Contains(prompt, "# Skill "+skillStandingRule) || !strings.Contains(prompt, body) {
					t.Fatalf("SYMPTOM: %s's assembled prompt does not render its skill %s (the # Skill section with the skill's body)", key, s.Skill)
				}
				rules, ok := skillKeyRules[s.Skill]
				if !ok {
					t.Fatalf("skill %s has no key rules pinned in skillKeyRules", s.Skill)
				}
				for name, rule := range map[string]string{"ask the owner only through pf step ask": rules.ask, "no background-and-poll, no own timeout or deadline": rules.foreground, "same tree": rules.sameTree} {
					if rule == "" {
						continue
					}
					rule = w111Flat(rule)
					if !strings.Contains(body, rule) {
						t.Errorf("SYMPTOM: skill %s lost its %q rule: %q", s.Skill, name, rule)
					}
					if !strings.Contains(prompt, rule) {
						t.Errorf("SYMPTOM: %s's assembled prompt lacks its skill's %q rule: %q", key, name, rule)
					}
				}
			})
		}
	}
	if !equalStringMaps(got, shippedSkillSteps) {
		t.Errorf("SYMPTOM: the shipped skill steps are %v, want %v (a step that stops declaring its skill no longer renders it)", sortedPairs(got), sortedPairs(shippedSkillSteps))
	}
	used := map[string]bool{}
	for _, sk := range shippedSkillSteps {
		used[sk] = true
	}
	for sk, rules := range skillKeyRules {
		if !used[sk] {
			t.Errorf("skillKeyRules pins %s, which no shipped step declares", sk)
		}
		if rules.ask == "" || rules.foreground == "" {
			t.Errorf("skill %s: every skill carries the pf step ask rule and the foreground rule", sk)
		}
	}
	for _, sk := range skillsWithSameTree {
		if skillKeyRules[sk].sameTree == "" {
			t.Errorf("skill %s must carry its same-tree rule", sk)
		}
	}
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
