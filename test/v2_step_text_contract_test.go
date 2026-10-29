package test

// v2_step_text_contract_test.go: content floor for the workflow step
// texts the Go execution driver pastes verbatim into step prompts. The
// only delivery contract is the result envelope (needs_input +
// question_id/question; answers return under "# Answered questions";
// verification carries command_evidence). The retired WI-Agent protocol
// (flush, pending_question, execution.json writes) must not be taught.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"gopkg.in/yaml.v3"
)

func TestV2StepTextsTeachNoRetiredWIAgentProtocol(t *testing.T) {
	root := filepath.Join(v1Root(t), "workflows")
	// execution.json may be READ by a step; a write/persist/update/append
	// instruction is retired.
	execWrite := regexp.MustCompile(`(?i)\b(write|writes|persist|persists|update|updates|append|appends)\b[^.\n]{0,60}execution\.json`)
	forbidden := map[string]*regexp.Regexp{
		"flush(":               regexp.MustCompile(`flush\(`),
		"pending_question":     regexp.MustCompile(`pending_question`),
		"answers[]":            regexp.MustCompile(`answers\[\]`),
		"in place":             regexp.MustCompile(`continuing this same step in place`),
		"execution.json write": execWrite,
	}
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		n++
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for name, re := range forbidden {
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s teaches retired protocol (%s): %q", p, name, b[loc[0]:loc[1]])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no workflow markdown files found")
	}
}

// TestV2VerificationStepsRequireCommandEvidence pins that a verification
// step still owes structured command evidence. Since the
// obligation text lives once, in the driver's delivery contract — the
// command-evidence paragraph it renders under the step entry's
// requires_command_evidence flag — and the step text no longer restates
// it, so the shipped-tree contract here is that flag: every bundle's
// verification step entry declares `requires_command_evidence: true`.
// The paragraph itself (at least one real check command, the exact argv,
// the real exit_code, the measured duration_ms > 0, never fabricate) is
// pinned by the driver package, which renders every lane's dispatched
// prompt from these same workflow.md entries
// (TestDeliveryContractStatesGuaranteesOnlyWhenTheyApply,
// TestSharedStepPromptsKeepLaneFacts).
func TestV2VerificationStepsRequireCommandEvidence(t *testing.T) {
	bundles, err := filepath.Glob(filepath.Join(v1Root(t), "workflows", "*", "workflow.md"))
	if err != nil || len(bundles) == 0 {
		t.Fatalf("no bundle workflow.md: %v", err)
	}
	sawEntry := false
	for _, wf := range bundles {
		raw, err := os.ReadFile(wf)
		if err != nil {
			t.Fatal(err)
		}
		front, _, ok := workflow.SplitFrontmatter(raw)
		if !ok {
			t.Fatalf("%s has no frontmatter block", wf)
		}
		var doc struct {
			Steps []struct {
				ID                      string `yaml:"id"`
				RequiresCommandEvidence bool   `yaml:"requires_command_evidence"`
			} `yaml:"steps"`
		}
		if err := yaml.Unmarshal(front, &doc); err != nil {
			t.Fatalf("%s frontmatter: %v", wf, err)
		}
		for _, s := range doc.Steps {
			if s.ID != "verification" {
				continue
			}
			sawEntry = true
			if !s.RequiresCommandEvidence {
				t.Errorf("%s: the verification step entry must declare requires_command_evidence: true — "+
					"the driver renders the command-evidence obligation only under that flag", wf)
			}
		}
	}
	if !sawEntry {
		t.Fatal("no verification step entry found in any bundle")
	}
	// The step text carries no second copy and never permits an empty
	// command_evidence, which the driver rejects.
	files, _ := filepath.Glob(filepath.Join(v1Root(t), "workflows", "*", "steps", "verification.md"))
	if len(files) == 0 {
		t.Fatal("no verification step texts found")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "leave `command_evidence` empty") {
			t.Errorf("%s still permits an empty command_evidence, which the driver rejects", f)
		}
	}
}
