package driver

// w80_prompt_render_test.go: the rendered size of the gate-step prompts
// (the w80 prompt trim). Each sampled prompt is the real dispatched
// prompt of a shipped bundle - a plain dispatch: first review round, the
// FULL verification tier, no rewind - assembled by the spawn adapter
// exactly as production assembles it. The per-prompt byte budget is
// generous on purpose: it does not pin today's size, it stops a removed
// block from silently creeping back into every dispatch. The sizes are
// logged (go test -v -run TestW80PromptSizeBudget) for the measurement
// record; the helper uses only long-standing test fixtures so the same
// file measures an older tree too.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w80MeasurePrompt dispatches one plain step of a shipped bundle at the
// given verification tier and answers its assembled prompt.
func w80MeasurePrompt(t *testing.T, bundle, stepID string, done map[string]bool) string {
	t.Helper()
	capDir := capsuleWithShippedBundle(t, bundle)
	shared, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "*.md"))
	stepDefs, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "*.md"))
	for _, p := range append(shared, stepDefs...) {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(shippedWorkflowsDir, p)
		writeCapsuleFile(t, capDir, "workflows/"+filepath.ToSlash(rel), string(raw))
	}
	sp := &captureSpawner{}
	ad, err := newTestSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_w80", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: stepID, SpawnN: 1}); err != nil {
		t.Fatalf("SpawnStep %s/%s: %v", bundle, stepID, err)
	}
	p, _ := sp.last()
	return p.InitialPrompt
}

// TestW80PromptSizeBudget logs the sampled prompts' byte sizes and holds
// each under its budget.
func TestW80PromptSizeBudget(t *testing.T) {
	for _, c := range []struct {
		bundle, step string
		budget       int
		done         map[string]bool
	}{
		// verification + 2000 and review + 1600: re-baselined (not trimmed)
		// for the unconditional supply-chain checklist both shared step
		// texts render (about 1.6 KB and 1.4 KB measured with its
		// outside-team-code trigger; the rest is the headroom the earlier
		// budgets granted); + ~800: re-baselined (not trimmed) for the
		// incomplete-evidence contract every result delivery renders;
		// review + 3000: re-baselined (not trimmed) for
		// the five blocking classes every review renders (D1:
		// the findings' class schema with tier_ok, the class definitions
		// and each checklist rule's class, about 2.9 KB); review + 300:
		// re-baselined (not trimmed) for the class definitions' e2e quality
		// and grill clauses (a fixed delay instead of
		// polling, the suites the engine discovers, a `clear` grill row the
		// ticket does not support — 220 bytes measured, past the 84 the
		// earlier headroom had left); review + 400: re-baselined (not
		// trimmed) for the authorization-request paragraph every gate's
		// result delivery renders (how a gate asks the owner
		// with options naming daemon actions, about 380 bytes); + 300:
		// re-baselined (not trimmed) for the downloads-directory sentence
		// every step prompt renders beside the capsule root (
		// where raw downloads go and the chain's blob limit, 288 bytes
		// measured, past the 48-82 the earlier headroom had left)
		// 14000 + 1500: the deliverable-carrier block (
		// about 1.3 KB) renders into every plan prompt; + 500: re-baselined
		// (not trimmed) for the owner-review declaration (
		// about 1.2 KB with the carrier's owner-presentation item); + ~800:
		// the incomplete-evidence contract
		// 11000 + 678: the waiting rule's background-work sentences
		// grow every principled prompt by that much;
		// + 1522: re-baselined (not trimmed) for the owner-review
		// declaration every build step renders;
		// + 800: re-baselined (not trimmed) for the incomplete-evidence
		// contract every result delivery renders; + 300:
		// re-baselined (not trimmed) for the downloads-directory sentence
		// (288 bytes; 73 bytes of headroom were left)
	} {
		p := w80MeasurePrompt(t, c.bundle, c.step, c.done)
		t.Logf("w80 prompt size %s/%s: %d bytes", c.bundle, c.step, len(p))
		if len(p) > c.budget {
			t.Errorf("%s/%s plain prompt is %d bytes, over its %d-byte budget: a rare-path or repeated block crept back into every dispatch", c.bundle, c.step, len(p), c.budget)
		}
	}
}
