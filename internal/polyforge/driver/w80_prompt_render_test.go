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
func w80MeasurePrompt(t *testing.T, bundle, stepID, tier string, done map[string]bool) string {
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
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_w80", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: stepID, SpawnN: 1, ScopeTier: tier, Done: done}); err != nil {
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
		{"chore", "verification", 20000, nil},
		{"chore", "review", 18000, nil},
		{"feature-full", "verification", 27000, nil},
		{"feature-full", "review", 20000, nil},
		// 14000 + 1500: the deliverable-carrier block (
		// about 1.3 KB) renders into every plan prompt; + 500: re-baselined
		// (not trimmed) for the owner-review declaration (
		// about 1.2 KB with the carrier's owner-presentation item)
		{"chore", "plan", 16000, nil},
		// 11000 + 678: the waiting rule's background-work sentences
		// grow every principled prompt by that much;
		// + 1522: re-baselined (not trimmed) for the owner-review
		// declaration every build step renders (about 0.9 KB
		// measured; the rest, about 0.6 KB, is granted headroom)
		{"chore", "code", 13200, map[string]bool{"plan": true}},
	} {
		p := w80MeasurePrompt(t, c.bundle, c.step, tierFull, c.done)
		t.Logf("w80 prompt size %s/%s: %d bytes", c.bundle, c.step, len(p))
		if len(p) > c.budget {
			t.Errorf("%s/%s plain prompt is %d bytes, over its %d-byte budget: a rare-path or repeated block crept back into every dispatch", c.bundle, c.step, len(p), c.budget)
		}
	}
}
