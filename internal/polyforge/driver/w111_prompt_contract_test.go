package driver

// w111_prompt_contract_test.go: pins the waiting rule,
// the release steps' wait wording and the build steps' scoped report,
// mechanically (whitespace-normalized, so a rewrap never breaks a pin).
//
//  1. TestW111WaitingPrincipleInEveryPrincipledPrompt — every shipped
//     step whose definition renders {{principles}} carries the waiting
//     principle in its assembled prompt. Red on the pre-change tree (the
//     shared principles had no waiting rule).
//  2. TestW111ReleaseStepsAwaitInsideOneCall — both release-lane steps
//     run the procedure in the foreground with an explicit timeout, and a
//     backgrounded procedure is awaited inside one call by a condition
//     loop with a deadline; neither says "then poll". Red on the
//     pre-change tree (both said "background it … then poll").
//  3. TestW111BuildStepsRunTheScopedReport — the four build steps run the
//     release-shipped engine's JSON report before calling a unit green,
//     run its `fast` and `scoped` phase commands and never its `full`
//     phase, and still leave the full suite to verification. Red on the
//     pre-change tree (the per-unit bar named no engine) and on the
//     positional reading of a full report it replaced.
//  4. TestW111NoSleepOutsideTheWaitingRule — no shipped workflow text
//     carries the word "sleep" except inside the waiting rule's own
//     sentences and three named phrases about the code under change (not
//     the agent's waiting). The scan itself is a regression pin (the
//     pre-change tree had no sleep instruction; it goes red there only on
//     the rule sentences' absence); its teeth are the mutations it runs
//     on the real texts.
//  5. TestW111AgentsMDKeepsTheUpgradePinRule — the repo's AGENTS.md
//     states that docs/upgrade.md is append-only and that every append
//     moves the sha256 pin in the docs-set acceptance test in the same
//     commit, and that test still pins the file's sha256. Red on the
//     pre-change tree (AGENTS.md named no such rule).
//
// The file adds no production code, no driver field and no config key.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w111Flat whitespace-normalizes a text so a phrase matches however the
// markdown wraps it.
func w111Flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// w111Principle is the waiting principle's text (after its bold title)
// in workflows/_shared/principles.md.
const w111Principle = "Waiting is never a tool call of its own. Run a command in the foreground with an explicit timeout at least as long as you expect it to take (the harness allows up to the daemon's tool bound). If something must be awaited — a server coming up, a procedure already started — wait INSIDE the same call: a loop that checks a real condition, sleeps at most 3 s per iteration and has a deadline. A bare `sleep N` call, or a loop whose only exit is the deadline, is a violation and is recorded."

// w111RuleSentences are the waiting rule's sentences that carry the word
// "sleep": the principle states them and both release steps restate them
// verbatim, so they are the only "sleep" a workflow text may carry
// besides w111CodeSleepPhrases.
var w111RuleSentences = []string{
	"a loop that checks a real condition, sleeps at most 3 s per iteration and has a deadline",
	"A bare `sleep N` call, or a loop whose only exit is the deadline, is a violation and is recorded.",
}

// w111CodeSleepPhrases are the pre-existing phrases, per workflows/-
// relative file, that name a sleep in the code under change — a design
// rule and two review findings — never an instruction for the agent to
// wait. Each is exempt only in its own file and only as this exact
// phrase.
var w111CodeSleepPhrases = map[string][]string{
	"_shared/principles.md":   {"no sleep posing as synchronization"},
	"_shared/steps/review.md": {"(`time.Sleep` as a synchronization stand-in", "which is what drove a sleep+latch hack"},
}

var w111Sleep = regexp.MustCompile(`(?i)sleep`)

// w111StraySleeps answers every "sleep" (any case) left in text once the
// waiting rule's sentences and rel's exempt code phrases are removed,
// each with a little context.
func w111StraySleeps(rel, text string) []string {
	flat := w111Flat(text)
	for _, s := range w111RuleSentences {
		flat = strings.ReplaceAll(flat, s, "")
	}
	for _, s := range w111CodeSleepPhrases[rel] {
		flat = strings.ReplaceAll(flat, s, "")
	}
	var out []string
	for _, loc := range w111Sleep.FindAllStringIndex(flat, -1) {
		from, to := max(loc[0]-40, 0), min(loc[1]+40, len(flat))
		out = append(out, flat[from:to])
	}
	return out
}

// TestW111WaitingPrincipleInEveryPrincipledPrompt: the waiting principle
// lives once in the shared principles and reaches the assembled prompt
// of every shipped step whose definition includes {{principles}}.
func TestW111WaitingPrincipleInEveryPrincipledPrompt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatalf("read the shared principles: %v", err)
	}
	if n := strings.Count(w111Flat(string(raw)), w111Principle); n != 1 {
		t.Fatalf("the shared principles must state the waiting principle exactly once, found %d", n)
	}
	if !regexp.MustCompile(`(?m)^6\. \*\*Waiting\.\*\* `).Match(raw) {
		t.Error("the waiting principle keeps the numbered style (`6. **Waiting.** …`)")
	}
	principled := 0
	for _, bs := range shippedBundleSteps(t) {
		bundle, step := filepath.Base(bs[0]), bs[1]
		def, err := os.ReadFile(workflow.StepDefinitionPath(bs[0], step))
		if err != nil {
			t.Fatalf("%s/%s: %v", bundle, step, err)
		}
		if !strings.Contains(string(def), "{{"+workflow.PrinciplesVar+"}}") {
			continue
		}
		principled++
		rendered, err := readStepDefinition(bs[0], step, shippedVars(t, bs[0], step))
		if err != nil {
			t.Fatalf("render %s/%s: %v", bundle, step, err)
		}
		// assembled exactly as a dispatch assembles it (a fresh dispatch:
		// an inherit step's predecessor need not exist for the text)
		prompt := assembleStepPrompt(promptParts{
			WIID: "wi_w111", StepID: step, SpawnN: 1,
			CapsuleRoot: "/ws/capsules/w111",
			ContextMode: ContextFresh,
			Definition:  rendered,
		})
		if !strings.Contains(w111Flat(prompt), w111Principle) {
			t.Errorf("the assembled %s/%s prompt renders {{principles}} but lacks the waiting principle", bundle, step)
		}
	}
	// every principled step the principles test enumerates was checked
	want := 0
	for _, steps := range principledSteps {
		want += len(steps)
	}
	if principled != want {
		t.Errorf("checked %d steps rendering {{principles}}, the shipped bundles have %d", principled, want)
	}
}

// TestW111ReleaseStepsAwaitInsideOneCall: the release lane's two
// procedure-running steps state the waiting rule for their long run —
// foreground with an explicit timeout; only a procedure known to exceed
// the tool bound is backgrounded, then awaited inside one call by a
// condition loop of at most 3 s per iteration with a deadline; the
// invocation recorded as command evidence with its real duration — and
// carry no background-then-poll instruction.
func TestW111ReleaseStepsAwaitInsideOneCall(t *testing.T) {
	for _, step := range []string{"deploy", "release"} {
		bundle := filepath.Join(shippedWorkflowsDir, "release")
		def, err := readStepDefinition(bundle, step, shippedVars(t, bundle, step))
		if err != nil {
			t.Fatalf("read release/%s: %v", step, err)
		}
		flat := w111Flat(def)
		if strings.Contains(strings.ToLower(flat), "then poll") {
			t.Errorf("release/%s still says \"then poll\" — a backgrounded procedure is awaited inside one call", step)
		}
		for _, want := range append([]string{
			"Waiting is never a tool call of its own",
			"in the foreground with an explicit timeout at least as long as you expect it to take (the harness allows up to the daemon's tool bound)",
			"Only a procedure known to exceed that bound may be backgrounded",
			"it is then awaited inside one call",
			"The condition is the exit-code file existing",
			"Record the invocation as command evidence with its real duration.",
		}, w111RuleSentences...) {
			if !strings.Contains(flat, want) {
				t.Errorf("release/%s must state the waiting rule for its long run, missing %q", step, want)
			}
		}
	}
}

// w111ScopedReport is the scoped-report duty the four build steps carry
// in their per-unit bar, phrase by phrase: the engine's JSON report, and
// the commands chosen by their reported phase — never by reading a line
// report's order, which is ambiguous when the full phase has several
// commands.
var w111ScopedReport = []string{
	"Before reporting a unit green, also run the release-shipped engine's scoped report in each repo the unit touched: `<data-dir>/release/current/tools/verify-scope --json`",
	"`<data-dir>` is `$POLYFORGE_DATA_DIR` when set, else `~/.local/share/polyforge`) from the repo clone, no other argument: the diff against `merge-base HEAD origin/<default>`.",
	"Of its `commands` (the repo's `.polyforge/verify-scope.conf` or the engine's defaults decide them), run in order each whose `phase` is `fast` or `scoped` — its `cmd` via `bash -c`, its `timeout` as the explicit timeout — never one whose `phase` is `full` (scoped is not full).",
	"A red there is a red fast check; one missing a `requires_env` or `requires_cmd` prerequisite is skipped and named in your artifact.",
	"A failure confined to tests an earlier step committed red on purpose (this lane's authored or repro tests) is the work still to do, not a red fast check.",
	"A missing engine or a non-zero exit is stated in your artifact and leaves the checks above as the bar.",
}

// w111PositionalFull is the retired positional reading of a full report
// (its full phase taken to be whatever follows the fast gates).
var w111PositionalFull = []string{
	"ends with the full suite",
	"run the commands before it",
	"`command:` lines it reports",
}

// TestW111BuildStepsRunTheScopedReport: every build step's per-unit bar
// runs the engine's JSON report, picks its fast and scoped phases by the
// reported phase, and keeps the full phase (and the full suite) out of it.
func TestW111BuildStepsRunTheScopedReport(t *testing.T) {
	for _, bs := range [][2]string{
		{"feature-full", "code"}, {"chore", "code"}, {"bug-fix", "fix"}, {"autopilot", "fix"},
	} {
		bundle := filepath.Join(shippedWorkflowsDir, bs[0])
		def, err := readStepDefinition(bundle, bs[1], shippedVars(t, bundle, bs[1]))
		if err != nil {
			t.Fatalf("read %s/%s: %v", bs[0], bs[1], err)
		}
		flat := w111Flat(def)
		for _, want := range append(w111ScopedReport,
			"do not run the full suite (e.g. `go test ./...`) here",
			"do not proceed on top of a red fast check",
		) {
			if !strings.Contains(flat, want) {
				t.Errorf("%s/%s's per-unit bar must carry %q", bs[0], bs[1], want)
			}
		}
		for _, gone := range w111PositionalFull {
			if strings.Contains(flat, gone) {
				t.Errorf("%s/%s still selects the per-unit commands by position (%q) — the JSON report's `phase` decides", bs[0], bs[1], gone)
			}
		}
	}
}

// TestW111NoSleepOutsideTheWaitingRule: no shipped workflows/**.md text
// carries "sleep" outside the waiting rule's sentences and the named code
// phrases; the check catches an instruction to sleep reintroduced in a
// real text.
func TestW111NoSleepOutsideTheWaitingRule(t *testing.T) {
	files := w79ShippedWorkflowTexts(t)
	if len(files) == 0 {
		t.Fatal("no shipped workflow text found")
	}
	byRel := map[string]string{}
	for _, f := range files {
		rel, err := filepath.Rel(shippedWorkflowsDir, f.path)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		byRel[rel] = f.text
		for _, hit := range w111StraySleeps(rel, f.text) {
			t.Errorf("%s carries \"sleep\" outside the waiting rule: …%s…", rel, hit)
		}
	}
	// The rule's sentences are where the exemption assumes them.
	for _, rel := range []string{"_shared/principles.md", "release/steps/deploy.md", "release/steps/release.md"} {
		for _, s := range w111RuleSentences {
			if !strings.Contains(w111Flat(byRel[rel]), s) {
				t.Errorf("%s lost the waiting rule's sentence %q", rel, s)
			}
		}
	}
	// Teeth: a sleep instruction added beside the rule in a release step,
	// a reworded rule sentence, and an exempt phrase moved to another
	// file are each caught.
	deploy := byRel["release/steps/deploy.md"]
	for _, m := range []struct{ rel, text string }{
		{"release/steps/deploy.md", deploy + "\nIf it is still running, `sleep 30` and check again.\n"},
		{"release/steps/deploy.md", strings.Replace(deploy, "sleeps at most 3 s", "sleeps at most 30 s", 1)},
		{"_shared/steps/verification.md", byRel["_shared/steps/verification.md"] + "\nno sleep posing as synchronization\n"},
		{"_shared/principles.md", byRel["_shared/principles.md"] + "\nSleep a while between checks.\n"},
	} {
		if len(w111StraySleeps(m.rel, m.text)) == 0 {
			t.Errorf("a mutated %s carrying a stray sleep went unnoticed", m.rel)
		}
	}
}

// TestW111AgentsMDKeepsTheUpgradePinRule: the append-only rule for the
// upgrade history lives in the repo's AGENTS.md coupled obligations, and
// the test file it names still carries the sha256 pin of that file — the
// rule points at a real pin, not a stale path.
func TestW111AgentsMDKeepsTheUpgradePinRule(t *testing.T) {
	root := filepath.Join(shippedWorkflowsDir, "..")
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	const pinFile = "test/w254_docs_set_acceptance_test.go"
	flat := w111Flat(string(agents))
	for _, want := range []string{
		"`docs/upgrade.md` is append-only",
		"every append moves the file's sha256 pin in `" + pinFile + "` in the same commit",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("AGENTS.md must carry the upgrade-history pin rule, missing %q", want)
		}
	}
	at := strings.Index(flat, "**Coupled obligations.**")
	if at < 0 {
		t.Fatal("AGENTS.md lost its coupled-obligations rule")
	}
	coupled := flat[at:]
	if i := strings.Index(coupled, "<!-- fork-only:begin -->"); i < 0 || !strings.Contains(coupled[:i], "`docs/upgrade.md` is append-only") {
		t.Error("the pin rule belongs in AGENTS.md's coupled obligations, outside any fork-only block (both files it names are projected)")
	}
	pin, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pinFile)))
	if err != nil {
		t.Fatalf("AGENTS.md names %s, which must exist: %v", pinFile, err)
	}
	if !strings.Contains(string(pin), `w254Read(t, "docs/upgrade.md")`) || !strings.Contains(string(pin), "sha256.Sum256") {
		t.Errorf("%s no longer pins docs/upgrade.md's sha256 — the AGENTS.md rule points at a stale pin", pinFile)
	}
}
