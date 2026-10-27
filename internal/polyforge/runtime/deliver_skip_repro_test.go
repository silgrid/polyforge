package runtime

// deliver_skip_repro_test.go: the repro, pinned on the
// real re-claim path. Production evidence (the delivery): a
// capsule re-materialization re-stamps capsule.json's repo baseline to
// the restored HEAD, and the deliver step's nothing-to-deliver check
// reads that field literally — a deliver result that skips the repo is
// then accepted although the repo holds the WI's own verified commits
// beyond origin/main, and the accepted skip strands that work in the
// capsule with nothing landing on main. Two scenarios are pinned:
//
//   - after a re-claim that restored the WI's own commit (a head beyond
//     origin/main), a deliver skip of that repo MUST be refused;
//   - after a re-claim with no work beyond the origin (no carrier), a
//     deliver skip MUST stay accepted — the bug must not be "fixed" by
//     turning an empty delivery into a hard one.
//
// The judgment goes through driver.ValidateStepResult — the same
// validation the daemon runs on a deliver step's result envelope — with
// the envelope shape a deliver step submits when it believes the
// capsule holds nothing to deliver (skipped_reason on the repo). The
// scenario is produced by the runtime itself (newCarrierRig's claim /
// carrier / re-claim sequence), not by hand-written capsule.json state,
// so the pinned expectation holds whichever layer a fix anchors the
// skip judgment on.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// judgeDeliverSkip runs the driver's delivery binding over a completed
// deliver result whose only task repo (app) is reported skipped with
// "nothing to deliver" — the envelope the deliver step submits when it
// believes the capsule holds no work. It answers the validation error:
// nil means the skip was accepted.
func judgeDeliverSkip(wi, reposDir string) error {
	res := &driver.StepResult{
		WIID:    wi,
		StepID:  "deliver",
		SpawnN:  1,
		Outcome: driver.OutcomeCompleted,
		Output: json.RawMessage(`{"verdict":"pass","repos":[{` +
			`"name":"app","skipped_reason":"nothing to deliver"}]}`),
		CommandEvidence: json.RawMessage(`[{"argv":["sh","-c","true"],` +
			`"exit_code":0,"duration_ms":5}]`),
	}
	spec := driver.ResultSpec{
		WIID:                   wi,
		StepID:                 "deliver",
		SpawnN:                 1,
		RequireCommandEvidence: true,
		ReposDir:               reposDir,
	}
	return driver.ValidateStepResult(res, spec)
}

// The repro scenario: a first claim does one commit in repos/app and
// publishes it to the carrier; a re-claim restores that commit on top of
// an origin that never moved. The deliver step then skips app — with the
// re-materialized capsule in that state, the skip must be refused: the
// repo holds the WI's own commit beyond origin/main, and an accepted
// skip strands it in the capsule (the ticket wraps "done" while main
// gains nothing).
func TestDeliverSkipAfterReclaimMustNotStrandVerifiedWork(t *testing.T) {
	g := newCarrierRig(t, "wi_repro36_work", true)
	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	capDir := testCapsuleDir(g.r, g.wi)
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")

	// The re-claim brought the WI's own commit back: it is the repo's
	// HEAD, and it sits beyond an origin/main that never moved.
	if got := g.repoHead(t); got != g.workHead {
		t.Fatalf("fixture: re-claimed repos/app HEAD = %q, want the carrier tip %q", got, g.workHead)
	}
	originMain := carrierGit(t, repoDir, "rev-parse", "refs/remotes/origin/main")
	if originMain != g.srcHead {
		t.Fatalf("fixture: repos/app origin/main = %q, want the claim-time base %q", originMain, g.srcHead)
	}
	beyond := carrierGit(t, repoDir, "rev-list", "--count", originMain+"..HEAD")
	if beyond == "0" {
		t.Fatalf("fixture: repos/app must hold commits beyond origin/main (%s..HEAD)", originMain)
	}
	// Observed for the record (not a pinned expectation): the
	// re-materialized capsule.json names as app's baseline.
	t.Logf("re-materialized capsule.json baseline for app: %q (origin/main %q, HEAD %q)",
		capsuleReposOf(t, capDir)["app"], originMain, g.workHead)

	err := judgeDeliverSkip(g.wi, filepath.Join(capDir, capsule.DirRepos))
	if err == nil {
		t.Fatalf("deliver skipped repos/app and the skip was ACCEPTED, although the repo holds %s commit(s) beyond origin/main (%s..%s): the WI's verified work is stranded in the capsule — the skip must be refused (deliver the work, or refuse loudly)",
			beyond, originMain, g.workHead)
	}
}

// The control: a re-claim with no carrier and no commit beyond the
// origin is the true nothing-to-deliver state — the skip must stay
// accepted there, whatever fixes the stranded-work scenario above.
func TestDeliverSkipWithoutWorkStaysAccepted(t *testing.T) {
	g := newCarrierRig(t, "wi_repro36_empty", false)
	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	capDir := testCapsuleDir(g.r, g.wi)
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")

	// No carrier, nothing beyond the origin: the plain re-claim state.
	if got := g.repoHead(t); got != g.srcHead {
		t.Fatalf("fixture: re-claimed repos/app HEAD = %q, want the plain clone's %q", got, g.srcHead)
	}
	originMain := carrierGit(t, repoDir, "rev-parse", "refs/remotes/origin/main")
	if originMain != g.srcHead {
		t.Fatalf("fixture: repos/app origin/main = %q, want %q", originMain, g.srcHead)
	}
	if beyond := carrierGit(t, repoDir, "rev-list", "--count", originMain+"..HEAD"); beyond != "0" {
		t.Fatalf("fixture: repos/app holds %s commit(s) beyond origin/main; the control must start empty", beyond)
	}

	if err := judgeDeliverSkip(g.wi, filepath.Join(capDir, capsule.DirRepos)); err != nil {
		t.Fatalf("the no-work deliver skip must stay accepted: %v", err)
	}
}
