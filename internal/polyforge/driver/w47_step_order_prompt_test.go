package driver

// w47_step_order_prompt_test.go (polyforge#47, AC1): every dispatch's
// header restates the lane's step order as it stands for that dispatch —
// the skipped steps and the last step an agent runs marked — so a
// mid-run override skip reaches the steps dispatched after it (the
// hand-off document's opening is the claim-time snapshot and never
// updates). The order renders in the dispatch part, before the step
// definition, in the dispatch's language; a spawn without lines carries
// no order section at all.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w47Order is a develop-shaped order: build, a waivable gate that
// rewinds, the publishing step and a skippable merge tail.
func w47Order() []StepOrderLine {
	return []StepOrderLine{
		{ID: "build"},
		{ID: "review", Gate: true, OnFail: "build", Waivable: true},
		{ID: "publish", Last: true},
		{ID: "merge", Skipped: true},
	}
}

func TestW47DispatchHeaderRestatesStepOrder(t *testing.T) {
	for _, tc := range []struct {
		lang string
		want []string
	}{
		{lang: "", want: []string{
			"1. `build`\n",
			"2. `review` (gate, fail rewinds to `build`, waivable)\n",
			"3. `publish` (last step)\n",
			"4. `merge` (skipped)\n",
		}},
		{lang: "zh-CN", want: []string{
			"1. `build`\n",
			"2. `review`（gate，fail 回退到 `build`，可放行）\n",
			"3. `publish`（最后一步）\n",
			"4. `merge`（已跳过）\n",
		}},
	} {
		prompt := assembleStepPrompt(promptParts{
			WIID: "wi_x", StepID: "publish", SpawnN: 1, Round: 1,
			CapsuleRoot: "/ws/capsules/demo-47", Language: tc.lang,
			StepHeader: "How a step runs.", Definition: "Do the step.",
			StepOrder: w47Order(),
		})
		flat := strings.Join(strings.Fields(prompt), " ")
		for _, want := range tc.want {
			if !strings.Contains(prompt, want) {
				t.Errorf("lang %q: the dispatch header must carry the order line %q", tc.lang, want)
			}
		}
		// the order rides the dispatch part: the claim-time snapshot note
		// names the hand-off document, and the whole section comes before
		// the step definition
		if !strings.Contains(flat, "as it stands for this dispatch") ||
			!strings.Contains(flat, "artifacts/handoff.md") {
			t.Errorf("lang %q: the order section must say it is the current order and name the opening's claim-time copy:\n%s", tc.lang, prompt)
		}
		if idx := strings.Index(prompt, "4. `merge`"); idx < 0 || idx > strings.Index(prompt, "# Step definition") {
			t.Errorf("lang %q: the step order belongs in the dispatch part, before the step definition", tc.lang)
		}

		// a spawn without lines carries no order section
		bare := assembleStepPrompt(promptParts{
			WIID: "wi_x", StepID: "publish", SpawnN: 1, Round: 1,
			CapsuleRoot: "/ws/capsules/demo-47", Language: tc.lang,
			Definition: "Do the step.",
		})
		if strings.Contains(bare, "step order") || strings.Contains(bare, "步骤顺序") {
			t.Errorf("lang %q: a dispatch without StepOrder must not render an order section", tc.lang)
		}
	}
}

// The driver fills the spawn's StepOrder from its CURRENT workflow — the
// skips applied — so a step dispatched after a mid-run override skip
// reads the order that skip made (AC1's first half).
func TestW47PrepareDispatchCarriesCurrentOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	// merge is skipped at the project level, fixed in the capsule at claim
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(capsule.SkippedStepsJSON)), []byte(`["merge"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "publish", capsule.StepInProgress, nil)
	sp := &fakeSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(3,
			StepSpec{ID: "build"},
			StepSpec{ID: "review", Gate: true, OnFail: &OnFail{Target: "build"}},
			StepSpec{ID: "publish"},
			StepSpec{ID: "merge", Skippable: true},
		),
		Spawner: sp,
		Log:     &eventLog{},
		Compose: composeOf(map[string][]string{"publish": {"pi:eff-a"}}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.composeDispatch("publish")
	spawn, _, err := d.prepareDispatch("publish")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	var got []string
	for _, l := range spawn.StepOrder {
		got = append(got, l.ID)
	}
	if strings.Join(got, ",") != "build,review,publish,merge" {
		t.Fatalf("the spawn's step order is %v, want the lane's declaration order", got)
	}
	last := spawn.StepOrder[len(spawn.StepOrder)-2]
	merge := spawn.StepOrder[len(spawn.StepOrder)-1]
	if !last.Last || last.ID != "publish" {
		t.Fatalf("with merge skipped, publish is the last agent step: %+v", spawn.StepOrder)
	}
	if !merge.Skipped || merge.Last {
		t.Fatalf("merge must carry the skip and not the last-step note: %+v", merge)
	}
	if !spawn.StepOrder[1].Gate || spawn.StepOrder[1].OnFail != "build" {
		t.Fatalf("the gate's declaration must ride its line: %+v", spawn.StepOrder[1])
	}
}
