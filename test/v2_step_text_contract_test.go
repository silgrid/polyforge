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

func TestV2VerificationStepsRequireCommandEvidence(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(v1Root(t), "workflows", "*", "steps", "verification.md"))
	if len(files) == 0 {
		t.Fatal("no verification step texts found")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "command_evidence: [{argv, exit_code, duration_ms}]") {
			t.Errorf("%s must instruct command_evidence: [{argv, exit_code, duration_ms}]", f)
		}
		for _, want := range []string{"AT LEAST ONE entry", "actually run at least one check command", "measured, > 0"} {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s evidence obligation must state %q (driver gate: >=1 entry, argv, duration_ms>0)", f, want)
			}
		}
		if strings.Contains(string(b), "leave `command_evidence` empty") {
			t.Errorf("%s still permits an empty command_evidence, which the driver rejects", f)
		}
	}
}
