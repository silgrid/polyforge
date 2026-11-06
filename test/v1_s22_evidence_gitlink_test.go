package test

// v1_s22: the V8 gate repair, end to end with the REAL daemon,
// driver/envelope edition. (The S22a V4 wrap-evidence-gate scenario
// pinned the retired flush(wrap) seal path and was deleted with it; the
// driver track re-adds the evidence gate.)
//
//   - S22b (V8): an embedded git repository laid in the capsule (the
//     #48 poison shape, the fix envelope action) is refused by the
//     LOCAL precheck — classified push_poisoned_local, never
//     push_failed — and its retries back off exponentially (no #48
//     storm); the documented cleanup (the unpoison envelope action) plus
//     a fresh commit boundary recovers the chain, which seals with NO
//     gitlink anywhere in the published history.
import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

func TestV1S22LocalGitlinkPrecheckAndRetryBackoff(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s22: local gitlink precheck, poisoned-local classification, retry backoff",
		[]map[string]any{
			// The #48 poison shape: an embedded repository OUTSIDE
			// repos/ (which is gitignored), laid by the fix envelope
			// action; fix's acceptance bumps the Version, so the push
			// round runs against the poisoned worktree while the WI stays
			// live.
			{"do": "envelope", "id": "fix", "outcome": "completed", "poison_gitlink": true},
			// review then asks the owner (`pf ask`) and parks: no further
			// Version until the reply, so the refusals back off. The
			// documented cleanup is its answer to the owner's reply: the
			// unpoison action deletes the nested .git, and review burns
			// 15s before its envelope — the pushes of the clean tree land
			// while the workflow is still live; the wrap must never
			// outrun the recovery push.
			askAction("review", "s22b: parked while the poisoned push rounds run.",
				map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "unpoison_gitlink": true, "ms": 15000}),
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	waitAsk(t, d, wi, 30*time.Second)

	// The first local refusal lands within one poll+push round.
	waitForEvent(t, d, wi, "push_poisoned_local", 30*time.Second)

	// The retries back off: the open ask keeps the Version stable, so the
	// refusals come at the production delays (10s, then 20s, ...), never
	// at a fixed cadence.
	var refusals []protocol.EventEntry
	waitUntil(t, 90*time.Second, "three poisoned-local refusals", func() bool {
		refusals = refusals[:0]
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == "push_poisoned_local" {
				refusals = append(refusals, e)
			}
		}
		return len(refusals) >= 3
	})
	at := func(e protocol.EventEntry) time.Time {
		ts, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			t.Fatalf("event timestamp %q: %v", e.At, err)
		}
		return ts
	}
	if first, second := at(refusals[1]).Sub(at(refusals[0])), at(refusals[2]).Sub(at(refusals[1])); second < first+5*time.Second {
		t.Fatalf("retry delays %v then %v: the second must be longer (exponential backoff)", first, second)
	}
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "push_failed" {
			t.Fatalf("a local precheck refusal must never classify as push_failed: %+v", e.Body)
		}
	}

	// The cleanup recovers the chain: the owner's reply reaches review's
	// resident process, whose action unpoisons and then lands its
	// envelope — the clean tree's pushes succeed, and the workflow runs to
	// its terminal wrap.
	replyOwner(t, d, wi, "Cleaned up; resume.")
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)

	// The recovery push: the clean tree is pushed while review holds its
	// 15s window, BEFORE the wrap stops the monitor.
	waitUntil(t, 60*time.Second, "the cleanup never recovered the chain", func() bool {
		return countOf(d.eventKinds(t, wi), "published") >= 1
	})
	inst.waitBareCommits(t, wi, 1, 60*time.Second)

	// THE load-bearing assertion of the precheck: the published history
	// contains NO gitlink anywhere — the poison never entered it.
	out := gitRun(t, inst.bareRepo(wi), "ls-tree", "-r", "main")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "160000") {
			t.Fatalf("the sealed chain must not contain a gitlink:\n%s", out)
		}
	}
	// And the formerly embedded files landed as plain capsule files (the
	// cleanup's promise: nothing of the work is lost).
	if !inst.bareHasPath(t, wi, "poison/inner.txt") {
		t.Fatalf("the cleaned-up embedded file must ride the chain as a plain file")
	}
	// The driver faces: the whole chain done, the cleanup's envelope
	// (review's, its dispatch after fix's) archived.
	x := readExecution(t, d.capsuleDir(wi))
	for _, step := range []string{"fix", "review"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	if h := acceptedHandoff(t, d.capsuleDir(wi), "review", 2); h.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("the cleanup continuation handed off %s, want done", h.Handoff.Status)
	}
}
