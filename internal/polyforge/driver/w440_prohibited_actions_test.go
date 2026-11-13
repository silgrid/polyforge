package driver

// w440_prohibited_actions_test.go (owner ruling
// 2026-11-10: agent-side safety is a soft mitigation, no hard isolation):
//
//   - every agent dispatch of every shipped lane — each step — carries
//     the prohibited-actions list, scoped to its capsule root, exactly
//     once;
//     the list's text is harness.StepProhibitedActions alone (the task
//     lane reuses the list, internal/polyforge/task), and its one
//     exception is the step's own definition — never "this prompt", which
//     would let a carried-in rewind reason or prior-round summary require
//     a listed action; the git-config prohibition covers every git config,
//     the global and system ones included;
//   - every shipped review states the security scope: no security
//     probing, only functional correctness, acceptance, project rules and
//     regressions, a noticed security risk one non-blocking line, and the
//     two exceptions — one fragment, rendered once per review.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w440CapsuleRoot answers the capsule root a dispatched prompt binds.
var w440CapsuleRoot = regexp.MustCompile("Capsule root: `([^`]+)`")

// w440Dispatches answers every agent dispatch of the shipped lanes: each
// step (a plain dispatch). (The sub-definitions a daemon step's action
// dispatched left with the actions, workflow contract v2 change 16.)
func w440Dispatches(t *testing.T) map[string]StepSpawn {
	t.Helper()
	out := map[string]StepSpawn{}
	for _, dir := range shippedBundleDirs(t) {
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		bundle := filepath.Base(dir)
		for _, s := range wf.Steps {
			out[bundle+"/"+s.ID] = StepSpawn{StepID: s.ID, SpawnN: 1}
		}
	}
	return out
}

func TestW440EveryAgentDispatchCarriesTheProhibitedActions(t *testing.T) {
	// the list as the owner ruled it: the four prohibitions
	list := harness.StepProhibitedActions("/ws/capsules/demo")
	for _, want := range []string{
		"# Prohibited actions",
		"Unless your step definition explicitly requires it, never:",
		"`$POLYFORGE_DATA_DIR` (else `~/.local/share/polyforge`)",
		"`$CLAUDE_CONFIG_DIR`",
		"`.claude/settings*.json`",
		"a Claude Code or git hook",
		"a command-running key in any git config, `--global` and `--system` included",
		"`core.fsmonitor`, `core.hooksPath`",
		"another work item's capsule or worktree, or a workspace file outside `/ws/capsules/demo`",
		"kill or signal a process you did not start",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("the prohibited-actions list lacks %q:\n%s", want, list)
		}
	}
	// the exception is the step's own definition alone: "this prompt"
	// would cover what the daemon carries in (a rewind reason, a prior
	// round's summary), and `.git/config` alone would leave the global
	// config (a host's push guard is its global core.hooksPath) open
	for _, wrong := range []string{"this prompt", "`.git/config`"} {
		if strings.Contains(list, wrong) {
			t.Errorf("the prohibited-actions list says %q:\n%s", wrong, list)
		}
	}
	if len(list) > 1024 {
		t.Errorf("the prohibited-actions list is %d bytes; it rides every dispatch and stays short (1024)", len(list))
	}

	dispatches := w440Dispatches(t)
	keys := make([]string, 0, len(dispatches))
	for k := range dispatches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("checked dispatches: %s", strings.Join(keys, ", "))
	for key, sp := range dispatches {
		bundle := strings.SplitN(key, "/", 2)[0]
		prompt := spawnStepPromptWith(t, bundle, sp)
		m := w440CapsuleRoot.FindStringSubmatch(prompt)
		if m == nil {
			t.Fatalf("%s: the prompt binds no capsule root", key)
		}
		if n := strings.Count(prompt, harness.StepProhibitedActions(m[1])); n != 1 {
			t.Errorf("%s: the dispatched prompt carries the prohibited-actions list %d times, want 1", key, n)
		}
		if strings.Index(prompt, "# Prohibited actions") > strings.Index(prompt, "# Step definition") {
			t.Errorf("%s: the list belongs with the capsule-root binding, before the step definition", key)
		}
	}
	// every shipped lane's steps
	if len(dispatches) < 8 {
		t.Errorf("checked %d dispatches, want every shipped step", len(dispatches))
	}

	// one source: no shipped workflow text restates a prohibition
	err := filepath.WalkDir(shippedWorkflowsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "Prohibited actions") || strings.Contains(string(raw), "a process you did not start") {
			t.Errorf("%s restates the prohibited-actions list: it lives once, in internal/common/harness", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// w440SecurityScope are the facts of the review security scope.
var w440SecurityScope = []string{
	"## Security scope",
	"does no security probing: no offensive probing, no injection reproduction, no privilege-escalation attempt",
	"judges functional correctness, the acceptance criteria, the project rules and regressions",
	"A security risk you notice anyway is one `non-blocking` finding of one line",
	"the change under review is itself a security feature",
	"breaks a rule already written — a repository's `AGENTS.md`",
}

func TestW440ReviewsDoNoSecurityProbing(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "review_security.md"))
	if err != nil {
		t.Fatal(err)
	}
	fragment := strings.TrimSpace(string(raw))
	flat := strings.Join(strings.Fields(fragment), " ")
	for _, want := range w440SecurityScope {
		if !strings.Contains(flat, want) {
			t.Errorf("the review security scope lacks %q", want)
		}
	}
	reviews := 0
	for key, sp := range w440Dispatches(t) {
		if sp.StepID != reviewStepID {
			continue
		}
		reviews++
		bundle := strings.SplitN(key, "/", 2)[0]
		prompt := spawnStepPromptWith(t, bundle, sp)
		if n := strings.Count(prompt, fragment); n != 1 {
			t.Errorf("%s: the dispatched review renders the security scope %d times, want 1", key, n)
		}
		// the blocking class a security defect would otherwise fall in
		// defers to the scope
		if strings.Contains(prompt, "correctness, security, data-integrity") {
			t.Errorf("%s: a class still blocks on every security defect, past the security scope", key)
		}
	}
	if reviews < 2 {
		t.Errorf("checked %d shipped reviews, want every lane's (develop, micro)", reviews)
	}
	// the later-round contract (contract 3's review_reentry.md, the review
	// skill since workflow contract 4) names the same class, whose one
	// definition — the shared review's blocking classes — defers security
	// defects to the security scope
	dir := filepath.Join(shippedWorkflowsDir, developBundle)
	sk, err := workflow.ReadSkill(dir, "review")
	if err != nil {
		t.Fatal(err)
	}
	if r := strings.Join(strings.Fields(sk.Body), " "); strings.Contains(r, "correctness, security, data-integrity") || !strings.Contains(r, "a `bug` anywhere in the change") {
		t.Error("the later-round contract's `bug` must be the blocking class, never a class of its own that blocks on every security defect")
	}
	def, err := readStepDefinition(dir, reviewStepID, shippedVars(t, dir, reviewStepID))
	if err != nil {
		t.Fatal(err)
	}
	if d := strings.Join(strings.Fields(def), " "); !strings.Contains(d, "**`bug`** — a correctness, data-integrity or concurrency defect, or a security defect that `## Security scope` lets block.") {
		t.Error("the `bug` class must defer security defects to the security scope")
	}
}
