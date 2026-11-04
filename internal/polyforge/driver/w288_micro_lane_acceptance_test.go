// carries: internal/polyforge/driver/*, internal/polyforge/runtime/*, internal/common/capsule/*, workflows/micro/*, workflows/_shared/steps/*, test/deliver_merge_rule_contract_test.go

package driver

// w288_micro_lane_acceptance_test.go: authored acceptance scenarios for
//'s three fixture oracles (artifacts/spec.md, "Acceptance
// criteria" oracle 1/2/3), proven red on the pre-implementation tree.
//
// Each scenario builds a real fixture repository the capsule carries
// (repos/demo + a bare origin whose default branch holds the
// repository's own .polyforge/verify-scope.conf), scripts the workflow's
// fix/review/deliver step results through the driver's existing scripted
// spawner, and judges only records the spec names: the workflow status in
// runtime-state/execution.json, the fix step's driver-written fast_lane
// plan (the credential the delivery must re-run), the termination reason,
// the spawn sequence, and the fixture origin's own history. No
// not-yet-existing symbol is referenced, so the baseline red is
// assertion-level (the lane's boundary/merge behaviour is absent), never a
// build failure.
//
// Run (targeted): GOWORK=off go test ./internal/polyforge/driver/ -run
// '^(TestW288MicroInBoundsAutoMerges|TestW288MicroOutOfBoundsTerminates|TestW288MicroLocaleKeyRedBlocksDelivery)$' -count=1 -v
//
// Every scenario prints one `PASS W288-M<n>` marker line when it passes,
// so verification can bind each scenario to its own output line.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w288FastCmd is the fixture repository's declared fast-phase evidence:
// it checks that the two locale message files carry the same key set.
const w288FastCmd = "sh checks/locale-keys.sh"

// w288Conf is the fixture's own micro declaration: docs and locale text
// paths, plus the fast gate the delivery credential must re-run.
const w288Conf = `# fixture repository micro declaration
micro_path=docs/**
micro_path=lib/i18n/**

fast_gate=sh checks/locale-keys.sh
`

// w288LocaleCheck is the declared locale key-set consistency check.
const w288LocaleCheck = `#!/bin/sh
set -e
keys() {
  sed -n 's/^[[:space:]]*"\([^"]*\)"[[:space:]]*:.*/\1/p' "$1" | sort
}
[ "$(keys lib/i18n/en.json)" = "$(keys lib/i18n/zh.json)" ] || {
  echo "locale key sets differ" >&2
  exit 1
}
echo "locale keys consistent"
`

// w288Env is one scenario's capsule: the fixture repo, its bare origin,
// the branch the capsule name fixes, and the driver over a scripted
// spawner.
type w288Env struct {
	t        *testing.T
	capDir   string
	dir      string // capDir/repos/demo
	bare     string // the fixture origin
	branch   string
	d        *Driver
	sp       *fakeSpawner
	execPath string
	log      *eventLog
}

// w288New builds the fixture and the micro-shaped driver (fix -> review ->
// deliver; no verification step), exactly the shape the spec fixes.
func w288New(t *testing.T) *w288Env {
	t.Helper()
	capDir := t.TempDir()
	repos := filepath.Join(capDir, capsule.DirRepos)
	dir := filepath.Join(repos, "demo")
	bare := filepath.Join(t.TempDir(), "origin.git")
	for _, d := range []string{dir, filepath.Dir(bare)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tgit(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	tgit(t, dir, "init", "-q", "-b", "main")
	tgit(t, dir, "remote", "add", "origin", bare)
	w288Write(t, filepath.Join(dir, "docs", "help.md"), "# Help\n\nInitial help text.\n")
	w288Write(t, filepath.Join(dir, "lib", "i18n", "en.json"), w288Locale("Read this"))
	w288Write(t, filepath.Join(dir, "lib", "i18n", "zh.json"), w288Locale("Read this"))
	w288Write(t, filepath.Join(dir, ".polyforge", "verify-scope.conf"), w288Conf)
	w288Write(t, filepath.Join(dir, "src", "app.go"), "package app\n")
	w288Write(t, filepath.Join(dir, "checks", "locale-keys.sh"), w288LocaleCheck)
	if err := os.Chmod(filepath.Join(dir, "checks", "locale-keys.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "baseline: the fixture's micro declaration")
	tgit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "remote", "set-head", "origin", "main")

	env := &w288Env{
		t:      t,
		capDir: capDir,
		dir:    dir,
		bare:   bare,
		branch: "polyforge/" + filepath.Base(capDir),
		sp:     &fakeSpawner{},
		log:    &eventLog{},
	}
	env.execPath = filepath.Join(capDir, capsule.ExecutionJSON)
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_w288", CapsuleDir: capDir, BundleName: "micro"},
		ExecPath: env.execPath,
		Workflow: &Workflow{Steps: []StepSpec{
			{ID: "fix", ContextPolicy: "inherit"},
			gateSpec("review", "fix"),
			{ID: "deliver", Gate: true, ContextPolicy: "fresh", OnFail: &OnFail{Target: "fix"}, RequiresCommandEvidence: true},
		}, MaxTotal: 2, MaxPerStep: 1},
		Spawner: env.sp,
		Log:     env.log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env.d = d
	return env
}

// w288FixOut is the fix step's report: the ticket text edit, the changed
// paths and the ordered fast evidence it ran (the driver builds the
// delivery credential from this report).
func w288FixOut(paths ...string) json.RawMessage {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = `"` + p + `"`
	}
	return json.RawMessage(`{"commits":["head"],"build_status":"green",` +
		`"changed_paths":[` + strings.Join(quoted, ",") + `],` +
		`"evidence":[{"repo":"demo","command":"` + w288FastCmd + `"}],` +
		`"summary":"the ticket's text edit"}`)
}

// w288DeliverOut is a delivered repo record.
func w288DeliverOut(branch, prURL, mergeCommit string) json.RawMessage {
	return json.RawMessage(`{"verdict":"pass","repos":[{"name":"demo","branch":"` + branch +
		`","pr_url":"` + prURL + `","merged":true,"merge_commit":"` + mergeCommit +
		`","tests_passed":true}],"delivery_note":"the fast evidence is green on the delivered HEAD"}`)
}

// w288RunFast runs the declared fast command in the fixture repo and
// answers its exit code; a non-zero code is the red evidence the locale
// key oracle needs.
func w288RunFast(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", w288FastCmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	t.Fatalf("fixture fast check: %v (%s)", err, out)
	return -1
}

// w288MergePR performs what `gh pr merge --merge` does in the scenarios:
// merge the pushed feature branch into the fixture origin's main and
// answer the merge commit. The capsule clone then fetches it.
func w288MergePR(t *testing.T, dir, bare, branch string) string {
	t.Helper()
	w := t.TempDir()
	tgit(t, w, "clone", "-q", bare, "c")
	c := filepath.Join(w, "c")
	tgit(t, c, "fetch", "-q", "origin", "refs/heads/"+branch+":refs/remotes/origin/"+branch)
	tgit(t, c, "merge", "-q", "--no-ff", "-m", "Merge pull request #1 from "+branch, "origin/"+branch)
	tgit(t, c, "push", "-q", "origin", "HEAD:refs/heads/main")
	sha := tgit(t, c, "rev-parse", "HEAD")
	tgit(t, dir, "fetch", "-q", "origin")
	return sha
}

// w288Deliver is the deliver step's scripted result: it performs the
// delivery procedure on the fixture (merge the default branch, run the
// declared fast evidence, push the feature branch, merge the PR when the
// evidence is green) and reports the result the driver judges.
func w288Deliver(t *testing.T, e *w288Env, prURL string, green bool) fakeEntry {
	t.Helper()
	res := &StepResult{Outcome: OutcomeCompleted}
	return fakeEntry{res: res, act: func() {
		tgit(t, e.dir, "merge", "-q", "--no-edit", "origin/main")
		rc := w288RunFast(t, e.dir)
		if green && rc != 0 {
			t.Fatalf("fixture: the declared fast check is red on a green scenario (exit %d)", rc)
		}
		if !green && rc == 0 {
			t.Fatalf("fixture: the dropped locale key did not turn the declared fast check red")
		}
		tgit(t, e.dir, "push", "-q", "origin", "HEAD:refs/heads/"+e.branch)
		if !green {
			res.Outcome = OutcomeGateFailed
			res.Output = json.RawMessage(`{"verdict":"fail","fail_reason":"the locale key-set fast evidence is red: ` + w288FastCmd + ` exits non-zero, so the PR is not merged"}`)
			res.CommandEvidence = rawEvidence([]CommandEvidence{
				{Argv: []string{"sh", "-c", "cd repos/demo && git merge --no-edit origin/main"}, DurationMs: 5},
				{Argv: []string{"sh", "-c", "cd repos/demo && " + w288FastCmd}, ExitCode: 1, DurationMs: 5},
				{Argv: []string{"sh", "-c", "cd repos/demo && git push origin HEAD:refs/heads/" + e.branch}, DurationMs: 5},
			})
			return
		}
		mergeCommit := w288MergePR(t, e.dir, e.bare, e.branch)
		res.Output = w288DeliverOut(e.branch, prURL, mergeCommit)
		res.CommandEvidence = rawEvidence(ev(
			"cd repos/demo && git merge --no-edit origin/main",
			"cd repos/demo && "+w288FastCmd,
			"cd repos/demo && git push origin HEAD:refs/heads/"+e.branch,
			"cd repos/demo && gh pr merge "+prURL+" --merge"))
	}}
}

// w288Write writes one fixture file (parents created).
func w288Write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w288Locale is the fixture locale message file.
func w288Locale(body string) string {
	return "{\n  \"help.title\": \"Help\",\n  \"help.body\": \"" + body + "\"\n}\n"
}

// w288Doc is the fixture docs file.
func w288Doc(body string) string { return "# Help\n\n" + body + "\n" }

// w288FixDocsAndLocale commits the ticket's docs + locale text edit.
func w288FixDocsAndLocale(t *testing.T, e *w288Env, body string) {
	t.Helper()
	w288Write(t, filepath.Join(e.dir, "docs", "help.md"), w288Doc(body))
	w288Write(t, filepath.Join(e.dir, "lib", "i18n", "en.json"), w288Locale(body))
	w288Write(t, filepath.Join(e.dir, "lib", "i18n", "zh.json"), w288Locale(body))
	tgit(t, e.dir, "add", "-A")
	tgit(t, e.dir, "commit", "-q", "-m", "docs: the ticket's help text")
}

// w288RawExec answers execution.json's loose map (the termination reason
// and any other field are read by name, never through a not-yet-existing
// Go field).
func w288RawExec(t *testing.T, execPath string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("execution.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("execution.json unparsable: %v", err)
	}
	return m
}

// w288Merges answers the fixture origin's first-parent merge count.
func w288Merges(t *testing.T, bare string) int {
	t.Helper()
	out, err := repoGit(bare, "rev-list", "--merges", "--first-parent", "refs/heads/main")
	if err != nil {
		return 0
	}
	return len(strings.Fields(out))
}

// w288Dispatched is the ordered step-id list the spawner was asked for.
func w288Dispatched(sp *fakeSpawner) []string {
	var out []string
	for _, d := range sp.dispatched() {
		step, _, _ := strings.Cut(d, "@")
		out = append(out, step)
	}
	return out
}

// w288LastRejection answers the last refused envelope's detail — the
// pre-change driver's stated reason, quoted in a failing scenario's
// symptom line as baseline evidence.
func w288LastRejection(l *eventLog) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].kind == EvEnvelopeRejected {
			detail, _ := l.entries[i].body["detail"].(string)
			return detail
		}
	}
	return ""
}

// w288Run drives the workflow with a bounded context. A workflow that
// parks on a human question (the pre-change driver's exit for a delivery
// the merge rule cannot place) ends at the bound; the scenario's own
// assertions then report the observed state.
func w288Run(t *testing.T, e *w288Env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.d.Run(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}

// TestW288MicroInBoundsAutoMerges: oracle 1 — an opted-in fixture
// repository with a docs+locale micro diff walks fix -> review ->
// deliver, the delivery's fast evidence is green on the delivered HEAD,
// the PR auto-merges with no human answer, and the execution record
// contains neither a verification step nor verified_heads.
func TestW288MicroInBoundsAutoMerges(t *testing.T) {
	e := w288New(t)
	const prURL = "https://fixture.test/demo/pull/1"
	e.sp.script = []fakeEntry{
		{res: &StepResult{Outcome: OutcomeCompleted, Output: w288FixOut("docs/help.md", "lib/i18n/en.json", "lib/i18n/zh.json")},
			act: func() { w288FixDocsAndLocale(t, e, "Read the help") }},
		done(`{"verdict":"pass","findings":[]}`),
		w288Deliver(t, e, prURL, true),
		// the pre-change driver's only exit for a delivered repo the merge
		// rule cannot place: the human's deliver-merge question. The fixed
		// driver must never reach it.
		asks("w288-deliver-merge", "the merge rule did not hold; merge, don't merge or back to code?"),
	}
	w288Run(t, e)

	exec := readExec(t, e.execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("SYMPTOM: an in-bounds docs+locale micro delivery did not complete (status %q); a verification-less "+
			"delivery lane must auto-merge on its review pass plus this dispatch's green fast evidence (dispatches %v; last refusal %q)",
			exec.WorkflowStatus, w288Dispatched(e.sp), w288LastRejection(e.log))
	}
	if exec.Steps["verification"] != nil {
		t.Fatalf("the micro execution record carries a verification step: %v", exec.Steps["verification"])
	}
	raw, err := os.ReadFile(e.execPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"verified_heads"`) {
		t.Fatalf("the micro execution record fabricates verified_heads:\n%s", raw)
	}
	if got := w288Dispatched(e.sp); strings.Join(got, ",") != "fix,review,deliver" {
		t.Fatalf("micro dispatched %v, want exactly fix,review,deliver", got)
	}
	fl, _ := exec.Steps["fix"].Output["fast_lane"].(map[string]any)
	if fl == nil || fl["in_bounds"] != true {
		t.Fatalf("the driver recorded no in-bounds fast_lane credential plan on the fix step: %v", exec.Steps["fix"].Output)
	}
	if exec.Steps["deliver"].PendingQuestion != nil {
		t.Fatalf("the delivery parked on a human question although the merge credential held: %v", exec.Steps["deliver"].PendingQuestion)
	}
	if n := w288Merges(t, e.bare); n != 1 {
		t.Fatalf("the fixture origin's main carries %d PR merges, want exactly 1", n)
	}
	fmt.Println("PASS W288-M1")
}

// TestW288MicroOutOfBoundsTerminates: oracle 2 — the same opted-in
// fixture with one changed source-code line mixed into the docs diff is
// terminated before any merge, the reason names autopilot, and no fix
// re-entry or rewind happens.
func TestW288MicroOutOfBoundsTerminates(t *testing.T) {
	e := w288New(t)
	const prURL = "https://fixture.test/demo/pull/1"
	e.sp.script = []fakeEntry{
		{res: &StepResult{Outcome: OutcomeCompleted, Output: w288FixOut("docs/help.md", "src/app.go")},
			act: func() {
				w288FixDocsAndLocale(t, e, "Read the help")
				w288Write(t, filepath.Join(e.dir, "src", "app.go"), "package app\n\n// a code line the ticket did not ask for\nvar MixedIn = true\n")
				tgit(t, e.dir, "add", "-A")
				tgit(t, e.dir, "commit", "-q", "-m", "docs: help text, plus a code line")
			}},
		done(`{"verdict":"pass","findings":[]}`),
		w288Deliver(t, e, prURL, true),
		asks("w288-deliver-merge", "the merge rule did not hold; merge, don't merge or back to code?"),
	}
	w288Run(t, e)

	exec := readExec(t, e.execPath)
	if exec.WorkflowStatus != StatusFailedGate {
		t.Fatalf("SYMPTOM: a code line mixed into a docs micro diff did not terminate the run (status %q, dispatches %v); "+
			"an out-of-bounds diff must end the run before any merge, without a rewind to fix (last refusal %q)",
			exec.WorkflowStatus, w288Dispatched(e.sp), w288LastRejection(e.log))
	}
	raw := w288RawExec(t, e.execPath)
	reason, _ := raw["termination_reason"].(string)
	if !strings.Contains(strings.ToLower(reason), "autopilot") {
		t.Fatalf("the termination reason does not name autopilot as the re-file lane: %q", reason)
	}
	if got := w288Dispatched(e.sp); strings.Join(got, ",") != "fix" {
		t.Fatalf("the out-of-bounds run dispatched %v, want only the one fix dispatch", got)
	}
	if exec.RewindStats.Count != 0 {
		t.Fatalf("the out-of-bounds termination charged %d rewind(s): %+v", exec.RewindStats.Count, exec.RewindStats)
	}
	if exec.Steps["review"].Status == capsule.StepDone || exec.Steps["deliver"].Status == capsule.StepDone {
		t.Fatalf("a gate was dispatched after the out-of-bounds verdict: review=%q deliver=%q",
			exec.Steps["review"].Status, exec.Steps["deliver"].Status)
	}
	if exec.Steps["deliver"].PendingQuestion != nil {
		t.Fatalf("the out-of-bounds run asked a human merge question instead of terminating: %v", exec.Steps["deliver"].PendingQuestion)
	}
	if n := w288Merges(t, e.bare); n != 0 {
		t.Fatalf("the fixture origin's main carries %d PR merges; an out-of-bounds run must not deliver", n)
	}
	if _, err := repoGit(e.bare, "rev-parse", "--verify", "-q", "refs/heads/"+e.branch); err == nil {
		t.Fatalf("the out-of-bounds run pushed its feature branch %s; nothing may be delivered", e.branch)
	}
	fmt.Println("PASS W288-M2")
}

// TestW288MicroLocaleKeyRedBlocksDelivery: oracle 3 — a micro diff that
// omits one locale key turns the declared fast evidence red: the delivery
// does not pass or merge, the run rewinds to fix, and only the repaired
// delivery (green evidence on the delivered HEAD) merges.
func TestW288MicroLocaleKeyRedBlocksDelivery(t *testing.T) {
	e := w288New(t)
	const prURL = "https://fixture.test/demo/pull/1"
	e.sp.script = []fakeEntry{
		// fix 1: the ticket edit drops the help.body key from one locale file.
		{res: &StepResult{Outcome: OutcomeCompleted, Output: w288FixOut("lib/i18n/zh.json")},
			act: func() {
				w288Write(t, filepath.Join(e.dir, "lib", "i18n", "zh.json"),
					"{\n  \"help.title\": \"Help\"\n}\n")
				tgit(t, e.dir, "add", "-A")
				tgit(t, e.dir, "commit", "-q", "-m", "docs: locale text, one key short")
			}},
		done(`{"verdict":"pass","findings":[]}`),
		// deliver 1: the fast evidence is red, so nothing is merged and the
		// delivery reports the gate failure.
		w288Deliver(t, e, prURL, false),
		// fix 2 (the rewind target): the missing key is restored.
		{res: &StepResult{Outcome: OutcomeCompleted, Output: w288FixOut("lib/i18n/en.json", "lib/i18n/zh.json")},
			act: func() { w288FixDocsAndLocale(t, e, "Read the help") }},
		done(`{"verdict":"pass","findings":[]}`),
		w288Deliver(t, e, prURL, true),
		asks("w288-deliver-merge", "the merge rule did not hold; merge, don't merge or back to code?"),
	}
	w288Run(t, e)

	exec := readExec(t, e.execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("SYMPTOM: after the red locale fast evidence rewound the delivery to fix, the repaired micro delivery "+
			"did not complete (status %q, dispatches %v; last refusal %q)", exec.WorkflowStatus, w288Dispatched(e.sp), w288LastRejection(e.log))
	}
	if got := w288Dispatched(e.sp); strings.Join(got, ",") != "fix,review,deliver,fix,review,deliver" {
		t.Fatalf("micro dispatched %v, want fix,review,deliver,fix,review,deliver (the red delivery rewinds to fix)", got)
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("the red fast evidence charged %d rewind(s), want exactly the one deliver->fix rewind: %+v",
			exec.RewindStats.Count, exec.RewindStats)
	}
	fl, _ := exec.Steps["fix"].Output["fast_lane"].(map[string]any)
	if fl == nil || fl["in_bounds"] != true {
		t.Fatalf("the driver recorded no in-bounds fast_lane credential plan on the fix step: %v", exec.Steps["fix"].Output)
	}
	if n := w288Merges(t, e.bare); n != 1 {
		t.Fatalf("the fixture origin's main carries %d PR merges; only the repaired, green delivery may merge", n)
	}
	red := exec.Steps["deliver"].Attempts
	foundRed := false
	for _, a := range red {
		if a.Cause == CauseGateFailVerdict {
			foundRed = true
		}
	}
	if !foundRed {
		t.Fatalf("the red delivery left no gate_failed attempt on the deliver step: %+v", red)
	}
	if exec.Steps["deliver"].PendingQuestion != nil {
		t.Fatalf("the repaired delivery still parks on a human question: %v", exec.Steps["deliver"].PendingQuestion)
	}
	fmt.Println("PASS W288-M3")
}
