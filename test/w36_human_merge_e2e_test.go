// carries: internal/polyforge/driver/*, internal/polyforge/daemon/*, internal/polyforge/cli/*, internal/polyforge/runtime/*, internal/polyforge/workspace/*, internal/common/workflow/*
// carries: workflows/develop/*, workflows/_shared/steps/*, workflows/_shared/skills/*, workflows/_shared/scripts/*

package test

// w36_human_merge_e2e_test.go (u9): a develop work item of a project that
// declares `steps.e2e.merge.by: human` (polyforge#36) runs the whole lane
// through the real daemon + Go driver and the fake harness, and the merge
// step waits for the person:
//
//   - `pf config set steps.e2e.merge.by human` is written (develop
//     declares its merge human_mergeable beside skippable) and
//     `steps.e2e.review.by human` is refused naming the lane key;
//   - the claim freezes the capsule's runtime-state/step-modes.json
//     ({"merge": "human"}), and publish's PR body closes on the
//     human-merge lines (await the person's merge, the up-to-date
//     branches setting) — never "do not merge it by hand";
//   - the merge step's first dispatch asks the owner (`pf step ask`)
//     listing the PR link and the verified head and ends its turn: the
//     question stands in the decision stream (`pf watch`), the fake forge
//     is never asked to merge, and the step's process is gone (the ask's
//     release — the same mechanism every step has);
//   - the person squashes the PR on the forge (GitHub's Squash and merge,
//     a plain commit on main); the owner's reply resumes the merge step,
//     merge.py takes the person's landing by the human landing rules
//     (exit 0, the landed set one plain commit) and the agent registers
//     the landing record before its done;
//   - the wrap carries integration.state landed — the landed set named
//     with its merge flag, the verified head as the head — and nothing
//     but the lane's own dispatches ran: no `gh pr merge`, no rewind
//     spent.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW36DevelopHumanMergeEndsLanded$' -count=1 -v -timeout 30m

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w36HumanBody is the publish skill's human-merge closing (AC2's words).
const w36HumanBody = "此 PR 等待人工审查并由人合并，合并后请回复车道"

// w36HumanPublishAction is the scripted publish agent of a human-merge
// work item: its checks first (publish-check.py), then the push and the PR
// as the skill says — the PR body closing on the human-merge lines the
// capsule's frozen step modes name (never the lane's own line), the
// publish.json the merge step reads and the delivery note.
func (x *w374DevelopWorld) w36HumanPublishAction(t *testing.T, branch string) map[string]any {
	t.Helper()
	check := "python3 -I " + filepath.Join(x.scripts, "publish-check.py")
	pub := `set -e; cap="$PWD"; cd repos/demo; git fetch -q origin; ` +
		`if grep -q '"proceed": true' "$cap/artifacts/publish-check.json"; then ` +
		`r=$(git ls-remote origin refs/heads/$b | cut -f1); ` +
		`if [ -z "$r" ] || git merge-base --is-ancestor "$r" HEAD; then git push -q origin HEAD:refs/heads/$b; else echo 'branch moved outside the lane' >&2; exit 1; fi; ` +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`if grep -q '"merge": *"human"' "$cap/runtime-state/step-modes.json"; then closing='` + w36HumanBody + `。请启用分支保护里的 Require branches to be up to date before merging（合并前分支必须最新）。'; else closing='The lane'"'"'s merge step merges this PR — do not merge it by hand.'; fi; ` +
		`printf '# PR body\n\nw36 change summary\n\n## Follow-up\n\nnone\n\n%s\n' "$closing" > "$cap/artifacts/publish-pr-demo.md"; ` +
		`if [ -n "$url" ]; then gh pr edit "$url" --body-file "$cap/artifacts/publish-pr-demo.md"; else url=$(gh pr create --base main --head "$b" --title "w36 human merge" --body-file "$cap/artifacts/publish-pr-demo.md"); fi; ` +
		`h=$(git rev-parse HEAD); ` +
		`printf '{"repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}]}' "$b" "$url" "$h" > "$cap/artifacts/publish.json"; ` +
		`cp "$cap/artifacts/publish-pr-demo.md" "$cap/artifacts/delivery.md"; ` +
		`printf '{"verdict":"pass","repos":[{"name":"demo","branch":"%s","pr_url":"%s","head":"%s"}],"delivery_note":"scripted publish"}' "$b" "$url" "$h" > "$cap/artifacts/publish-output.json"; ` +
		`else cp "$cap/artifacts/publish-check.json" "$cap/artifacts/publish-output.json"; fi`
	return map[string]any{"do": "envelope", "id": "publish", "outcome": "completed", "output_file": "artifacts/publish-output.json", "no_started_at": true,
		"run": literalBranch(branch, check, pub)}
}

// w36MergeAskAction is the scripted merge agent's first dispatch in the
// human-merge mode: the skill's ask (the PR link and the verified head of
// the publish artifact and the verified result), then its turn ends — the
// step parks awaiting the owner.
func (x *w374DevelopWorld) w36MergeAskAction() map[string]any {
	ask := `url=$(python3 -I -c 'import json; print(json.load(open("artifacts/publish.json"))["repos"][0]["pr_url"])') && ` +
		`h=$(python3 -I -c 'import json; print(json.load(open("artifacts/verify/result.json"))["heads"]["demo"])') && ` +
		`pf step ask "本工单的 merge 步在等人合模式：请人工审查并合并以下 PR，合并完成后回复本车道；如需修改，请回复修改意见。demo: $url（验证过的 head $h）"`
	return map[string]any{"do": "envelope", "id": "merge", "end_turn": true, "run": []string{ask}}
}

// w36MergeRunAction is the scripted merge agent's resumed dispatch (the
// owner answered): the skill's line, the landing record registered when
// it landed, then the route (merge-out.py) — done on a landing.
func (x *w374DevelopWorld) w36MergeRunAction() map[string]any {
	return map[string]any{"do": "envelope", "id": "merge", "outcome": "completed", "output_file": "artifacts/merge-out.json", "no_started_at": true,
		"run": []string{w374MergeLine, w374RegisterLanding, "python3 -I " + filepath.Join(x.scripts, "merge-out.py")}}
}

// w36HumanSquash performs the person's Squash and merge of the PR on the
// forge (a plain commit on origin's main, never a `gh pr merge`) and
// marks the PR MERGED at the forge's mergeCommit. Answers the squash.
func w36HumanSquash(t *testing.T, x *w374DevelopWorld, branch string) string {
	t.Helper()
	tmp := filepath.Join(x.w.root, "human-squash")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	origin := x.w.origin("demo")
	gitRun(t, tmp, "clone", "-q", origin, tmp)
	gitRun(t, tmp, "fetch", "-q", "origin")
	gitRun(t, tmp, "checkout", "-q", "-B", "main", "origin/main")
	gitRun(t, tmp, "merge", "-q", "--squash", "origin/"+branch)
	gitRun(t, tmp, "commit", "-q", "-m", branch+" (#1)")
	gitRun(t, tmp, "push", "-q", "origin", "HEAD:refs/heads/main")
	sha := strings.TrimSpace(gitRun(t, tmp, "rev-parse", "HEAD"))
	d := filepath.Join(x.w.state, "demo", "prs", "1")
	for k, v := range map[string]string{"state": "MERGED\n", "merge": sha + "\n"} {
		if err := os.WriteFile(filepath.Join(d, k), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return sha
}

func TestW36DevelopHumanMergeEndsLanded(t *testing.T) {
	// the shipped lanes: develop declares its merge human_mergeable (and
	// skippable — the one step of the lane), micro declares none
	for _, c := range []struct{ lane, skippable, human string }{{"develop", "merge", "merge"}, {"micro", "", ""}} {
		lane, errs := workflow.Load(filepath.Join(v1Root(t), "workflows", c.lane))
		if len(errs) > 0 || lane == nil {
			t.Fatalf("load the shipped %s lane: %v", c.lane, errs)
		}
		var skippable, human []string
		for _, s := range lane.Steps {
			if s.Skippable {
				skippable = append(skippable, s.ID)
			}
			if s.HumanMergeable {
				human = append(human, s.ID)
			}
		}
		if strings.Join(skippable, ",") != c.skippable || strings.Join(human, ",") != c.human {
			t.Fatalf("SYMPTOM: the shipped %s lane: skippable %v, human_mergeable %v", c.lane, skippable, human)
		}
	}

	x, wi, branch := newW374World(t, w374TicketNoStop)
	inst, w := x.inst, x.w
	// the fake forge keeps each PR's body (the PR body's closing line is
	// the assertion's)
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
		x.w36HumanPublishAction(t, branch),
		// the human-merge mode: the ask first, then the run on the answer
		x.w36MergeAskAction(),
		x.w36MergeRunAction(),
	}, nil)
	d := newV1Daemon(t, inst, w.daemonEnv())
	d.start()
	t.Cleanup(func() { killOrphans(wi) })

	// AC1: the workspace's by key, through pf config set against the
	// running daemon's workflows
	env := w113Env{socket: d.sock, config: d.configPath()}
	yamlPath := filepath.Join(d.workspace, ".polyforge.yaml")
	out, code := w113Pf(t, inst, env, "config", "set", "steps.e2e.review.by", "human", "--workspace", d.workspace, "--expect-hash", "absent")
	if code == 0 || !strings.Contains(out, "human_mergeable") {
		t.Fatalf("SYMPTOM: pf config set steps.e2e.review.by human must be refused naming the lane key: exit %d, %s", code, out)
	}
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
		t.Fatalf("a refused by write must leave the workspace yaml absent: %v", err)
	}
	out, code = w113Pf(t, inst, env, "config", "set", "steps.e2e.merge.by", "human", "--workspace", d.workspace, "--expect-hash", "absent")
	if code != 0 {
		t.Fatalf("SYMPTOM: pf config set steps.e2e.merge.by human must be written (develop declares merge human_mergeable): exit %d, %s", code, out)
	}
	if y := v1ReadString(yamlPath); !regexp.MustCompile(`(?s)e2e:.*merge:.*by: human`).MatchString(y) {
		t.Fatalf("the workspace yaml must carry steps.e2e.merge.by: human:\n%s", y)
	}
	fmt.Println("PASS S1 review's by refused naming human_mergeable; merge's by written")

	d.pf(t, "submit", wi)
	capDir := d.capsuleDir(wi)

	// AC2: the merge step asks the owner and parks — the question listing
	// each repo's PR link and the verified head
	waitUntil(t, 300*time.Second, "the merge step's awaiting_owner", func() bool {
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == "awaiting_owner" {
				var body struct {
					Step string `json:"step"`
				}
				_ = json.Unmarshal(e.Body, &body)
				if body.Step == "merge" {
					return true
				}
			}
		}
		return false
	})
	asks, err := capsule.ReadAsks(capDir)
	if err != nil {
		t.Fatalf("asks.jsonl: %v", err)
	}
	open := capsule.OpenAsk(asks, "merge", 6)
	if open == nil || !strings.Contains(open.Text, "等人合") {
		t.Fatalf("SYMPTOM: the merge step never asked the owner its human-merge question: %+v", asks)
	}
	askAt, question := open.At, open.Text
	pubJSON := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "publish.json"))
	prURL, _ := pubJSON["repos"].([]any)[0].(map[string]any)["pr_url"].(string)
	verHeads := w374ReadJSON(t, filepath.Join(capDir, "artifacts", "verify", "result.json"))["heads"].(map[string]any)
	verifiedHead := verHeads["demo"].(string)
	if !strings.Contains(question, prURL) || !strings.Contains(question, verifiedHead) {
		t.Fatalf("SYMPTOM: the question must list the PR link and the verified head: %q (PR %s, head %s)", question, prURL, verifiedHead)
	}
	// the decision stream carries the question
	if out := d.pf(t, "watch", wi, "--once"); !strings.Contains(out, "等人合") || !strings.Contains(out, prURL) {
		t.Fatalf("SYMPTOM: pf watch must show the human-merge question with the PR link: %s", out)
	}
	// the claim froze the step modes; publish's PR body closed on them
	if mode := v1ReadString(filepath.Join(capDir, "runtime-state", "step-modes.json")); !strings.Contains(mode, `"merge": "human"`) {
		t.Fatalf("SYMPTOM: the capsule's frozen step modes must name merge human: %s", mode)
	}
	body := v1ReadString(filepath.Join(w.state, "demo", "prs", "1", "body"))
	if !strings.Contains(body, w36HumanBody) || strings.Contains(body, "do not merge it by hand") {
		t.Fatalf("SYMPTOM: the PR body must close on the human-merge lines: %q", body)
	}
	// the forge was never asked to merge while the lane waited
	if n := w.calls("gh pr merge"); n != 0 {
		t.Fatalf("SYMPTOM: the fake forge was asked to merge %d time(s)", n)
	}
	fmt.Println("PASS S2 the merge step asked over the PR link and the verified head; the PR body awaits the person")

	// AC7/AC3: the person squashes the PR (Squash and merge) and the owner
	// answers; merge.py takes the person's landing, the wrap is landed
	squash := w36HumanSquash(t, x, branch)
	if out := d.pf(t, "answer", wi, "--text", "已合并（squash）", "--by", "usr_root"); !strings.Contains(out, "reply to ask "+askAt) {
		t.Fatalf("SYMPTOM: pf answer must reply to the merge step's ask: %s", out)
	}
	waitWorkflowStatus(t, d, wi, "completed", 600*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// every pf step call of the run was accepted, and no rewind was spent
	ledger := w374Ledger(t, capDir)
	for _, l := range ledger {
		if l.Kind == stepcall.KindResult && l.Outcome != stepcall.OutcomeOK {
			t.Fatalf("SYMPTOM: a pf step call was not accepted: %+v", l)
		}
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHubPrefix+"wrap-attrs" {
			// the landing record was registered in the merge step's round
		}
	}
	x2 := readExecution(t, capDir)
	for _, s := range []string{"build", "review", "sync", "verification", "publish", "merge"} {
		if st := x2.Steps[s]; st == nil || st.Status != "done" {
			t.Fatalf("step %s must be done: %+v", s, st)
		}
	}

	// the wrap: landed, the landed set named with its merge flag
	got, err := inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("read the wrapped work item: %v", err)
	}
	in, _ := got.Attrs["integration"].(map[string]any)
	if in["state"] != "landed" {
		t.Fatalf("SYMPTOM: the wrap must carry integration.state landed: %v", got.Attrs)
	}
	mcs, _ := in["merge_commits"].(map[string]any)
	demo, _ := mcs["demo"].([]any)
	if len(demo) != 1 {
		t.Fatalf("SYMPTOM: the landed set is the person's one squash commit: %v", mcs)
	}
	entry, _ := demo[0].(map[string]any)
	if entry["sha"] != squash || entry["merge"] != false {
		t.Fatalf("SYMPTOM: the landed set names the squash with its merge flag: %v", entry)
	}
	repos, _ := in["repos"].([]any)
	r0, _ := repos[0].(map[string]any)
	if r0["pr"] != prURL || r0["head"] != verifiedHead || r0["light_verified_head"] != verifiedHead || r0["merge_commit"] != squash {
		t.Fatalf("SYMPTOM: the landing record's repo entry: %v (PR %s, head %s, squash %s)", r0, prURL, verifiedHead, squash)
	}
	if n := w.calls("gh pr merge"); n != 0 {
		t.Fatalf("SYMPTOM: the lane never asked the forge to merge: %d call(s)", n)
	}
	if st := w.prState("demo", "1"); st != "MERGED" {
		t.Fatalf("SYMPTOM: the person's squash merged the PR: state %q", st)
	}
	fmt.Println("PASS S3 the person's squash landed; wrapped landed with the landed set and its merge flag")
}
