package driver

// w138_background_rule_test.go: the waiting rule binds
// background work too. A step that started 36 spin loops to reproduce a
// load-only flake, then blocked in the shell's `wait` on a pid list an
// unquoted zsh variable never split, was invisible to a rule that only
// named `sleep`. The extended rule 6 states the three semantics — no
// artificial load on a shared host, every background process bounded and
// killed by process group, `wait` is a wait — and this test pins each in
// the shared principles and in the assembled prompt of every step that
// renders {{principles}}. Red on the pre-change tree (rule 6 named no
// background process). The file adds no production code.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w138BackgroundPhrases are the extended rule's semantic phrases.
var w138BackgroundPhrases = []string{
	"a `wait` is a wait like `sleep`",
	"await a real condition with a deadline, or bound and poll what you started",
	"A step never manufactures artificial load on a shared host",
	"use the test's own parallelism knobs",
	"Every background process a step starts is bounded",
	"kill it by process group before the call returns, with a `trap` as the backstop",
	"never pass a pid list through an unquoted shell variable",
}

func TestW138BackgroundProcessRule(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatal(err)
	}
	// the phrases extend rule 6 itself, which keeps its numbered style
	m := regexp.MustCompile(`(?m)^6\. \*\*Waiting\.\*\* (.*)$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("the shared principles lost rule 6 (`6. **Waiting.** …`)")
	}
	for _, p := range w138BackgroundPhrases {
		if !strings.Contains(string(m[1]), p) {
			t.Errorf("rule 6 must state %q", p)
		}
	}
	checked := 0
	for bundle, steps := range principledSteps {
		dir := filepath.Join(shippedWorkflowsDir, bundle)
		for _, step := range steps {
			rendered, err := readStepDefinition(dir, step, shippedVars(t, dir, step))
			if err != nil {
				t.Fatalf("render %s/%s: %v", bundle, step, err)
			}
			prompt := w111Flat(assembleStepPrompt(promptParts{
				WIID: "wi_w138", StepID: step, SpawnN: 1,
				CapsuleRoot: "/ws/capsules/w138",
				ContextMode: ContextFresh,
				Definition:  rendered,
			}))
			for _, p := range w138BackgroundPhrases {
				if !strings.Contains(prompt, p) {
					t.Errorf("the assembled %s/%s prompt lacks %q", bundle, step, p)
				}
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no principled step checked")
	}
}
