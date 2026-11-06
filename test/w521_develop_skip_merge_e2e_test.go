// carries: workflows/develop/*, workflows/_shared/skills/*, workflows/_shared/scripts/*, workflows/_shared/steps/*
// carries: internal/polyforge/driver/*, internal/polyforge/daemon/*, internal/polyforge/runtime/*, internal/polyforge/workspace/*, internal/polyforge/cli/*, internal/common/workflow/*

package test

// w521_develop_skip_merge_e2e_test.go: a project that
// skips the develop lane's merge step, end to end through the real daemon
// + Go driver and the fake harness, on the shipped develop lane.
//
// TestW521DevelopMergeSkippedEndsPublished —
//
//   - `pf config set steps.e2e.review.skip true` is refused naming
//     skippable (develop declares no such thing for review), and
//     `pf config set steps.e2e.merge.skip true` is written (develop
//     declares merge `skippable: true`, its only skippable step);
//   - the ticket runs build → review → sync → verification → publish and
//     ends: merge is never dispatched (no step_started for it, the fake gh
//     never asked to `pr merge`), execution.json records it skipped and
//     the event log carries step_skipped for it;
//   - the scripted publish reads the opening of artifacts/handoff.md the
//     way its skill says: it is noted the last step (merge noted skipped),
//     so the PR body says the PR awaits a human review, and it runs the
//     skill's published.py line and registers the record with the skill's
//     `pf step hub wrap-attrs` line — both lines taken from the skill file
//     itself, never restated here;
//   - the work item is wrapped with `integration.state = published` and
//     its first repo's `pr` the PR's link; the PR on the fake forge is
//     still OPEN, its body the human-review version.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW521DevelopMergeSkippedEndsPublished$' -count=1 -v -timeout 30m

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// The PR body's two closing lines of the publish skill (step 2): the
// lane's own merge, or — publish the lane's last step — a human review.
const (
	w521MergesLine = "The lane's merge step merges this PR — do not merge it by hand."
	w521AwaitsLine = "This lane does not merge this PR — it awaits a human review."
)

// w521LastStepPy exits 0 when the opening of artifacts/handoff.md (its
// lane step order, before the first section) notes publish as the last
// step — the publish skill's condition for its last-step branch — and 1
// otherwise.
const w521LastStepPy = `import re, sys
doc = open("artifacts/handoff.md", encoding="utf-8").read()
opening = doc.split("\n---\n", 1)[0]
m = re.search(r"(?m)^\d+\. ` + "`publish`" + `(.*)$", opening)
sys.exit(0 if m and re.search(r"last step|最后一步", m.group(1)) else 1)
`

// w521GHRecorder wraps the world's fake gh (moved beside it as gh-fake):
// it keeps the body file a `pr create` or `pr edit` was given as the PR's
// body on the fake forge (<state>/<repo>/prs/<n>/body); everything else is
// the fake's.
const w521GHRecorder = `#!/bin/sh
real="$(dirname "$0")/gh-fake"
sub=$2; bodyfile=""; target=""; prev=""
for a in "$@"; do
  [ "$prev" = --body-file ] && bodyfile=$a
  case "$a" in https://*/pull/*) target=$a ;; esac
  prev=$a
done
out=$("$real" "$@"); rc=$?
[ -n "$out" ] && printf '%s\n' "$out"
if [ $rc -eq 0 ] && [ -n "$bodyfile" ]; then
  [ "$sub" = create ] && target=$out
  S="$FAKE_GH_STATE/$(basename "$(git remote get-url origin)" .git)"
  n=${target##*/}
  [ -d "$S/prs/$n" ] && cp "$bodyfile" "$S/prs/$n/body"
fi
exit $rc
`

// w521SkillLines answers the publish skill's published.py line and its
// wrap-attrs registration line, verbatim from workflows/_shared/skills/publish.md.
func w521SkillLines(t *testing.T) (published, wrap string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "_shared", "skills", "publish.md"))
	if err != nil {
		t.Fatal(err)
	}
	skill := string(raw)
	p := regexp.MustCompile("`(\"\\$PF_PYTHON\" \"\\$PF_SHARED_DIR/scripts/published\\.py\"[^`]*)`").FindStringSubmatch(skill)
	w := regexp.MustCompile("`(pf step hub wrap-attrs integration @[^`]*)`").FindStringSubmatch(skill)
	if p == nil || w == nil {
		t.Fatalf("the publish skill must carry its published.py line and its wrap-attrs line (published %v, wrap %v)", p, w)
	}
	// the skill wraps its prose: the body lines are matched with the
	// line breaks and indentation collapsed
	flat := strings.Join(strings.Fields(skill), " ")
	for _, line := range []string{w521MergesLine, w521AwaitsLine} {
		if !strings.Contains(flat, line) {
			t.Fatalf("the publish skill must carry the PR body line %q", line)
		}
	}
	return p[1], w[1]
}

// w521PublishAction is the scripted publish agent: its checks first
// (publish-check.py), then the push and the PR as the skill says — the PR
// body closing on the human-review line when the opening notes publish as
// the last step — publish.json and the delivery note; then, as the last
// step only, the skill's published.py line and, on its exit 0, the skill's
// wrap-attrs line (else the gate's fail naming the record).
func (x *w374DevelopWorld) w521PublishAction(t *testing.T, branch string) map[string]any {
	published, wrap := w521SkillLines(t)
	last := "python3 -I " + filepath.Join(x.scripts, "last-step.py")
	check := "python3 -I " + filepath.Join(x.scripts, "publish-check.py")
	pub := `set -e; cap="$PWD"; ` +
		`if ` + last + `; then closing=` + shellQuote(w521AwaitsLine) + `; else closing=` + shellQuote(w521MergesLine) + `; fi; ` +
		`cd repos/demo; git fetch -q origin; ` +
		`if grep -q '"proceed": true' "$cap/artifacts/publish-check.json"; then ` +
		`r=$(git ls-remote origin refs/heads/$b | cut -f1); ` +
		`if [ -z "$r" ] || git merge-base --is-ancestor "$r" HEAD; then git push -q origin HEAD:refs/heads/$b; else echo 'branch moved outside the lane' >&2; exit 1; fi; ` +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`printf '# PR body\n\nw521 change summary\n\n## Follow-up\n\nnone\n\n%s\n' "$closing" > "$cap/artifacts/publish-pr-demo.md"; ` +
		`if [ -n "$url" ]; then gh pr edit "$url" --body-file "$cap/artifacts/publish-pr-demo.md"; else url=$(gh pr create --base main --head "$b" --title "w521 publish" --body-file "$cap/artifacts/publish-pr-demo.md"); fi; ` +
		`h=$(git rev-parse HEAD); ` +
		`printf '{"repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}]}' "$b" "$url" "$h" > "$cap/artifacts/publish.json"; ` +
		`cp "$cap/artifacts/publish-pr-demo.md" "$cap/artifacts/delivery.md"; ` +
		`printf '{"verdict":"pass","repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}],"delivery_note":"scripted publish"}' "$b" "$url" "$h" > "$cap/artifacts/publish-output.json"; ` +
		`else cp "$cap/artifacts/publish-check.json" "$cap/artifacts/publish-output.json"; fi`
	record := `if ` + last + ` && grep -q '"verdict":"pass"' artifacts/publish-output.json; then ` +
		`if ` + published + `; then ` + wrap + `; ` +
		`else printf '{"verdict":"fail","fail_reason":"published.py: a published head is not the verified one"}' > artifacts/publish-output.json; fi; fi`
	return map[string]any{"do": "envelope", "id": "publish", "outcome": "completed", "output_file": "artifacts/publish-output.json", "no_started_at": true,
		"run": literalBranch(branch, check, pub, record)}
}

func TestW521DevelopMergeSkippedEndsPublished(t *testing.T) {
	// the shipped lanes: develop declares merge skippable and no other
	// step; micro (its merge never skipped) declares none
	for _, c := range []struct{ lane, want string }{{"develop", "merge"}, {"micro", ""}} {
		lane, errs := workflow.Load(filepath.Join(v1Root(t), "workflows", c.lane))
		if len(errs) > 0 || lane == nil {
			t.Fatalf("load the shipped %s lane: %v", c.lane, errs)
		}
		var skippable []string
		for _, s := range lane.Steps {
			if s.Skippable {
				skippable = append(skippable, s.ID)
			}
		}
		if strings.Join(skippable, ",") != c.want {
			t.Fatalf("SYMPTOM: the shipped %s lane's skippable steps = %v, want [%s]", c.lane, skippable, c.want)
		}
	}

	x, wi, branch := newW374World(t, w374TicketNoStop)
	inst, w := x.inst, x.w
	if err := os.WriteFile(filepath.Join(x.scripts, "last-step.py"), []byte(w521LastStepPy), 0o644); err != nil {
		t.Fatal(err)
	}
	// the fake forge keeps each PR's body
	if err := os.Rename(filepath.Join(w.bin, "gh"), filepath.Join(w.bin, "gh-fake")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), []byte(w521GHRecorder), 0o755); err != nil {
		t.Fatal(err)
	}

	inst.setPlanFull(t, wi, []map[string]any{
		buildAction(w374Build("u1 red then green", x.planFiles())),
		x.reviewAction("facts"),
		x.syncAction(),
		verifyAction(map[string]any{"verdict": "pass", "summary": "light verification passed"}),
		x.w521PublishAction(t, branch),
		// never dispatched: merge is skipped
		x.mergeAction(),
	}, nil)
	d := newV1Daemon(t, inst, w.daemonEnv())
	d.start()
	t.Cleanup(func() { killOrphans(wi) })

	// AC1 on the shipped lane: the workspace's switch, through pf config
	// set against the running daemon's workflows
	env := w113Env{socket: d.sock, config: d.configPath()}
	yaml := filepath.Join(d.workspace, ".polyforge.yaml")
	out, code := w113Pf(t, inst, env, "config", "set", "steps.e2e.review.skip", "true", "--workspace", d.workspace, "--expect-hash", "absent")
	if code == 0 || !strings.Contains(out, "skippable") {
		t.Fatalf("SYMPTOM: pf config set steps.e2e.review.skip true must be refused naming skippable: exit %d, %s", code, out)
	}
	if _, err := os.Stat(yaml); !os.IsNotExist(err) {
		t.Fatalf("a refused skip write must leave the workspace yaml absent: %v", err)
	}
	out, code = w113Pf(t, inst, env, "config", "set", "steps.e2e.merge.skip", "true", "--workspace", d.workspace, "--expect-hash", "absent")
	if code != 0 {
		t.Fatalf("SYMPTOM: pf config set steps.e2e.merge.skip true must be written (develop declares merge skippable): exit %d, %s", code, out)
	}
	if y := v1ReadString(yaml); !regexp.MustCompile(`(?s)e2e:.*merge:.*skip: true`).MatchString(y) {
		t.Fatalf("the workspace yaml must carry steps.e2e.merge.skip: true:\n%s", y)
	}
	fmt.Println("PASS S1 review's skip refused naming skippable; merge's skip written")

	d.pf(t, "submit", wi)
	waitWorkflowStatus(t, d, wi, "completed", 600*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	x2 := readExecution(t, capDir)

	// every pf step call of the run was accepted
	ledger := w374Ledger(t, capDir)
	for _, l := range ledger {
		if l.Kind == stepcall.KindResult && l.Outcome != stepcall.OutcomeOK {
			t.Fatalf("SYMPTOM: a pf step call was not accepted: %+v", l)
		}
	}

	// the lane ran build → review → sync → verification → publish, each
	// done once; merge skipped, never started
	for _, s := range []string{"build", "review", "sync", "verification", "publish"} {
		if st := x2.Steps[s]; st == nil || st.Status != "done" {
			t.Fatalf("step %s must be done: %+v", s, st)
		}
		if hs := stepcall.StepHandoffs(ledger, s); len(hs) != 1 || hs[0].Handoff.Status != stepcall.StatusDone {
			t.Fatalf("step %s must hand off done once: %+v", s, hs)
		}
	}
	if st := x2.Steps["merge"]; st == nil || st.Status != "skipped" || st.StepStartedAt != "" || len(st.Attempts) != 0 {
		t.Fatalf("SYMPTOM: execution.json must record merge skipped, never started: %+v", st)
	}
	if hs := stepcall.StepHandoffs(ledger, "merge"); len(hs) != 0 {
		t.Fatalf("SYMPTOM: merge handed off: %+v", hs)
	}
	skipped := 0
	for _, e := range d.eventsOf(t, wi) {
		var body struct {
			Step string `json:"step"`
		}
		_ = json.Unmarshal(e.Body, &body)
		if e.Kind == "step_started" && body.Step == "merge" {
			t.Fatalf("SYMPTOM: merge was dispatched: %s %s", e.Kind, e.Body)
		}
		if e.Kind == "step_skipped" && body.Step == "merge" {
			skipped++
		}
	}
	if skipped == 0 {
		t.Fatalf("SYMPTOM: the event log must carry step_skipped for merge")
	}
	if n := w.calls("gh pr merge"); n != 0 {
		t.Fatalf("SYMPTOM: the fake forge was asked to merge %d time(s)", n)
	}
	if exits := w374RunExits(t, capDir, w374MergeLine); len(exits) != 0 {
		t.Fatalf("SYMPTOM: merge.py ran: %v", exits)
	}
	fmt.Println("PASS S2 the lane ended after publish; merge skipped (execution.json, step_skipped), never dispatched")

	// the opening notes merge skipped and publish the last step — what the
	// scripted publish read
	opening, _, _ := strings.Cut(v1ReadString(filepath.Join(capDir, "artifacts", "handoff.md")), "\n---\n")
	pubLine := regexp.MustCompile("(?m)^\\d+\\. `publish`.*$").FindString(opening)
	mergeLine := regexp.MustCompile("(?m)^\\d+\\. `merge`.*$").FindString(opening)
	if !regexp.MustCompile(`last step|最后一步`).MatchString(pubLine) || !regexp.MustCompile(`skipped|已跳过`).MatchString(mergeLine) {
		t.Fatalf("the hand-off opening must note publish the last step and merge skipped: %q / %q", pubLine, mergeLine)
	}

	// publish ran the skill's published.py line (exit 0) and registered
	// its record with the skill's wrap-attrs line, in its round, before done
	published, _ := w521SkillLines(t)
	if exits := w374RunExits(t, capDir, published); len(exits) != 1 || exits[0] != 0 {
		t.Fatalf("the publish step must run the skill's published.py line once, exit 0: %v", exits)
	}
	ph := stepcall.StepHandoffs(ledger, "publish")
	var wraps []stepcall.Line
	for _, l := range ledger {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHubPrefix+"wrap-attrs" && l.Outcome == stepcall.OutcomeOK {
			wraps = append(wraps, l)
		}
	}
	if len(wraps) != 1 || wraps[0].Step != "publish" || wraps[0].Round != ph[0].Round || wraps[0].Seq > ph[0].Seq {
		t.Fatalf("publish must register the published record once, before its done: wrap-attrs %+v, hand-offs %+v", wraps, ph)
	}

	// the wrap carries the published record
	repo := filepath.Join(capDir, "repos", "demo")
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	pubJSON := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "publish.json"))
	prs, _ := pubJSON["repos"].([]any)
	var prURL string
	if len(prs) == 1 {
		if m, ok := prs[0].(map[string]any); ok {
			prURL, _ = m["pr_url"].(string)
		}
	}
	got, err := inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	repos, _ := in["repos"].([]any)
	var r0 map[string]any
	if len(repos) == 1 {
		r0, _ = repos[0].(map[string]any)
	}
	if in["state"] != "published" || r0 == nil || prURL == "" || r0["pr"] != prURL || !strings.HasSuffix(prURL, "/pull/1") ||
		r0["head"] != head || r0["light_verified_head"] != head || in["merge_commits"] != nil || r0["merge_commit"] != nil {
		t.Fatalf("SYMPTOM: the wrap must carry integration.state published with the PR link %q at head %s: attrs %v", prURL, head, got.Attrs)
	}
	fmt.Println("PASS S3 wrapped with integration.state = published and the PR link")

	// the PR on the fake forge: still open, its body the human-review one
	body := v1ReadString(filepath.Join(w.state, "demo", "prs", "1", "body"))
	if st := w.prState("demo", "1"); st != "OPEN" || !strings.Contains(body, "it awaits a human review") || strings.Contains(body, "do not merge it by hand") {
		t.Fatalf("SYMPTOM: the PR must stay OPEN with the human-review body: state %q, body %q", st, body)
	}
	fmt.Println("PASS S4 the PR stays open, its body saying it awaits a human review")
}
