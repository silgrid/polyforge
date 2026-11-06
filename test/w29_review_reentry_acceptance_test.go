package test

// w29_review_reentry_acceptance_test.go: — the review
// re-entry mode, end to end through the real daemon + driver and the
// fake harness, on workflow contract 4.
//
// The re-entry contract: a later review round still runs with a FRESH
// conversation (fresh is the base — the old reviewer's blind spots are
// never inherited) and works from what the earlier round left: its
// numbered findings, in `artifacts/review.md` as that round left it, and
// its section of the hand-off document `artifacts/handoff.md` — the
// daemon-stamped status, reason and the heads it judged — followed by the
// 回退 event naming the steps that run again and the fix round's section.
// Two additive faces run: each earlier blocking finding still present is a
// residual, reported by its place (`r<round>#<k>`) without re-deriving it,
// and the fresh face is the full judgment. Under contract 4 the daemon
// renders no re-entry section into the prompt and archives no result
// envelope (contract 3's `_shared/review_reentry.md` section and its
// `artifacts/steps/review/s<n>/result.json` are gone): the review's
// definition carries the later-round procedure on every dispatch, and the
// step learns that its round is a later one from the hand-off document.
//
// DETERMINISM BOUNDARY: the reviewer's residual-vs-new *judgment* is an
// LLM call and is not machine-observable here. This test pins the INPUTS
// the re-entry contract guarantees: (a) the first round handed off fail
// (the call ledger), and at the second round's dispatch its findings file
// and its stamped section — the heads it judged — are there to read, with
// the 回退 event and the fix round's section after it; (b) the second
// dispatch's prompt carries the later-round procedure; (c) that second
// dispatch is fresh (`resume` false — conversation is never inherited);
// and (d) a one-round scenario's hand-off document carries no earlier
// review round and no rewind. The judgment itself is not machine-verified
// anywhere in the suite; it is observed on real work items during the
// contributing guide's discipline ③ observation window for workflow/prompt
// changes.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// The second review's scripted first commands: what a later round reads
// before it judges (the review definition's 开工前读什么、核对什么).
const (
	w29ReadHandoff  = "cat artifacts/handoff.md"
	w29ReadFindings = "cat artifacts/review.md"
)

// w29FirstFindings is the first round's artifacts/review.md: its verdict
// and its numbered findings, as the review definition asks.
const w29FirstFindings = "# review\n\nverdict: fail\n\n" +
	"#1 blocking — repos/demo-src: the new branch has no test\n" +
	"#2 non-blocking — repos/demo-src: nit: the helper name is terse\n"

// w29Prompt reads the fake pi's persisted prompt of one derivation
// ordinal (`logs/fakepi-prompt-<N>.txt`).
func w29Prompt(t *testing.T, capDir string, spawnN int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-"+strconv.Itoa(spawnN)+".txt"))
	if err != nil {
		t.Fatalf("fakepi prompt of spawn %d: %v", spawnN, err)
	}
	return string(raw)
}

// w29Read answers the journaled output of the plan's one run of the
// scripted command cmd (logs/fakepi.jsonl).
func w29Read(t *testing.T, capDir, cmd string) string {
	t.Helper()
	for _, e := range fakeJournal(t, capDir) {
		if e["type"] != "run" || e["cmd"] != cmd {
			continue
		}
		out, _ := e["output"].(string)
		if code, _ := e["exit_code"].(float64); code != 0 {
			t.Fatalf("%q exited %v: %s", cmd, code, out)
		}
		return out
	}
	t.Fatalf("the scripted read %q never ran", cmd)
	return ""
}

// w29Section answers the section of the hand-off document doc opened by
// the heading line prefix heading (up to the next section), and where it
// starts (-1, "" when there is none).
func w29Section(doc, heading string) (int, string) {
	at := strings.Index(doc, "\n"+heading)
	if at < 0 {
		return -1, ""
	}
	section := doc[at+1:]
	if end := strings.Index(section, "\n---\n"); end >= 0 {
		section = section[:end]
	}
	return at, section
}

// w29LaterRoundProcedure answers the review definition's later-round item
// from the lane snapshot the capsule runs (workflows/<lane>/steps/
// review.md): the paragraph opening with "**A later round.**".
func w29LaterRoundProcedure(t *testing.T, capDir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "workflows", vehicleLane, "steps", "review.md"))
	if err != nil {
		t.Fatal(err)
	}
	def := string(raw)
	at := strings.Index(def, "**A later round.**")
	if at < 0 {
		t.Fatalf("the %s review definition carries no later-round item", vehicleLane)
	}
	item := def[at:]
	if end := strings.Index(item, "\n\n"); end >= 0 {
		item = item[:end]
	}
	return item
}

// TestW29ReviewReentryTwoRound drives the vehicle lane through a review
// FAIL and its fix pass, then a second review dispatch, and asserts the
// re-entry inputs: what the first round left is readable at the second
// round's dispatch, and the second dispatch is fresh and carries the
// later-round procedure.
func TestW29ReviewReentryTwoRound(t *testing.T) {
	plan := append(w222MicroHead(),
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed",
			"files":  map[string]string{"artifacts/review.md": w29FirstFindings},
			"output": map[string]any{"verdict": "fail", "fail_reason": "the new branch has no test"},
		},
		w67ChangedRound("fix", "demo-src", 1), // the fix pass adds the missing test
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed",
			"run":    []string{w29ReadHandoff, w29ReadFindings},
			"output": map[string]any{"verdict": "pass"},
		},
	)
	capDir := w222RunPi(t, "w29: review re-entry reads the prior round's findings", plan)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	// One review FAIL rewind to fix, then the gate re-runs: a fresh review.
	if got := strings.Join(w222Order(t, capDir), ","); got != "fix,review,fix,review" {
		t.Errorf("dispatch order %s, want the two-round review tail", got)
	}

	// (a) The first round handed off fail: the first review is derivation
	// 2 (fix 1, review 2), the fix round derivation 3.
	first := acceptedHandoff(t, capDir, "review", 2)
	if first.Handoff.Status != stepcall.StatusFail || first.Handoff.Reason != "the new branch has no test" {
		t.Fatalf("the first review handed off %s (%q), want fail with its reason", first.Handoff.Status, first.Handoff.Reason)
	}
	fix := acceptedHandoff(t, capDir, "fix", 3)
	if len(first.Handoff.Heads) == 0 {
		t.Fatalf("the first review's hand-off stamps no judged heads: %+v", first.Handoff)
	}

	// (a) What the second round read at its dispatch: the first round's
	// findings as it left them, and the hand-off document in order — the
	// first round's stamped section, the 回退 event, the fix round.
	if got := w29Read(t, capDir, w29ReadFindings); got != w29FirstFindings {
		t.Errorf("the second review read artifacts/review.md as\n%s\nwant the first round's findings as it left them\n%s", got, w29FirstFindings)
	}
	doc := w29Read(t, capDir, w29ReadHandoff)
	atReview, review1 := w29Section(doc, "## review · round 1 · dispatch 2 · fail")
	atRewind, rewind := w29Section(doc, "## Event · Rewind · ")
	atFix, _ := w29Section(doc, "## fix · round 2 · dispatch 3 · done")
	if atReview < 0 || atRewind < atReview || atFix < atRewind {
		t.Fatalf("the second review read no first-round section → Rewind event → fix round 2 (at %d, %d, %d):\n%s", atReview, atRewind, atFix, doc)
	}
	if !strings.Contains(review1, "| Reason | the new branch has no test |") {
		t.Errorf("the first round's section lacks its reason:\n%s", review1)
	}
	for repo, head := range first.Handoff.Heads {
		// the head the first round judged, which a later round compares
		// its tree with — the fix round moved it
		if !strings.Contains(review1, "- `"+repo+"`: `") || !strings.Contains(review1, "→ `"+head+"`") {
			t.Errorf("the first round's section does not stamp %s's judged head %s:\n%s", repo, head, review1)
		}
		if fix.Handoff.Heads[repo] == head {
			t.Errorf("the fix round left %s at the judged head %s (the scripted round commits)", repo, head)
		}
	}
	for _, want := range []string{
		"`review` round 1 handed off `fail`",
		"Reason: the new branch has no test",
		"Steps to redo (in order): `fix` → `review`",
	} {
		if !strings.Contains(rewind, want) {
			t.Errorf("the Rewind event lacks %q:\n%s", want, rewind)
		}
	}

	// (b) The second review dispatch's persisted prompt carries the
	// later-round procedure: the definition's item, and the step header's
	// rule for a gate that runs again. The second review is derivation 4
	// (fix 3, review 4).
	prompt := w29Prompt(t, capDir, 4)
	for _, want := range []string{
		w29LaterRoundProcedure(t, capDir),
		"You run a gate again: check every issue the previous round of this gate raised",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("second review prompt (spawn 4) lost the later-round procedure %q", want)
		}
	}

	// (c) The second review dispatch is fresh: conversation is never
	// inherited across review rounds.
	rv := w222Steps(t, capDir, "review")
	if len(rv) != 2 {
		t.Fatalf("review dispatches %d, want 2: %v", len(rv), rv)
	}
	if rv[1]["resume"] != false {
		t.Errorf("the review re-entry must be fresh, got resume=%v (%v)", rv[1]["resume"], rv[1])
	}
}

// TestW29ReviewReentrySingleRound pins the first-round path: the lane
// completes exactly as before, and the only review round finds no earlier
// review round in the hand-off document.
func TestW29ReviewReentrySingleRound(t *testing.T) {
	plan := append(w222MicroHead(),
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
	)
	capDir := w222RunPi(t, "w29: a single review round has no prior round", plan)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	rv := w222Steps(t, capDir, "review")
	if len(rv) != 1 {
		t.Fatalf("review dispatches %d, want 1: %v", len(rv), rv)
	}
	// (d) The sole review hand-off is its own round (spawn 2: review runs
	// right after fix), and the hand-off document — which only ever grows,
	// so it holds everything the review could read at its dispatch —
	// carries no other review section and no rewind.
	if hs := stepcall.StepHandoffs(ledgerLines(t, capDir), "review"); len(hs) != 1 || hs[0].Spawn != 2 || hs[0].Handoff.Status != stepcall.StatusDone {
		t.Errorf("review hand-offs %+v, want exactly the done of dispatch 2 — a first round has no prior round", hs)
	}
	raw, err := os.ReadFile(filepath.Join(capDir, "artifacts", "handoff.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	if n := strings.Count(doc, "\n## review · "); n != 1 || !strings.Contains(doc, "\n## review · round 1 · dispatch 2 · done") {
		t.Errorf("the hand-off document carries %d review sections, want only round 1's (dispatch 2, done):\n%s", n, doc)
	}
	if strings.Contains(doc, "## Event · Rewind") {
		t.Errorf("a single-round run's hand-off document carries a Rewind event:\n%s", doc)
	}
}

// w67ChangedRound is the scripted stand-in for a build re-entry that really
// changed the code: one round-specific file committed in repos/<repo>, so the
// task-repo tree moves off the tree the rewinding gate judged (a re-entry
// that commits nothing would be an identical resubmission, which the
// skills' same-tree rules ask the owner about instead of judging again).
func w67ChangedRound(step, repo string, round int) map[string]any {
	return map[string]any{"do": "envelope", "id": step, "outcome": "completed",
		"run": []string{fmt.Sprintf("set -e; cd repos/%s; printf 'build round %d\\n' > build-round.txt; git add build-round.txt; "+
			"git -c user.name=fake-agent -c user.email=fake-agent@invalid commit -q -m 'build round %d'", repo, round, round)}}
}
