// carries: skills/output-format/SKILL.md

package test

// pf101_output_format_contract_test.go (polyforge#101, AC5): the
// output-format skill's outdated mechanism claims are gone and the
// current ones are in — reverse pins (every absence paired with a
// positive marker in the same file, so none passes vacuously):
//
//   - "not for step agents (their outputs are schema-bound envelopes)":
//     step agents' narration is owner-facing free text; the step and
//     task prompts carry the short output-discipline rule themselves —
//     and the skill states the division: the persistent short rule sets
//     the floor, the skill carries the details;
//   - "It wakes the session (the harness turns it into a turn)": no
//     such mechanism exists; the attention grades stay, the wake claim
//     goes;
//   - "the skill loads again at the next session start": there is no
//     auto-reload; what persists across sessions and compaction is the
//     context-file rule (pf doctor --fix-harness-prompts keeps it
//     there);
//   - the envelope face is a `pf step ask` question card (the retired
//     v1 needs_input is gone);
//   - the retired summarize lane is gone from the panel face; the
//     200-character cap is Pi Web's own panel truncation.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPF101OutputFormatSkillContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.Join("skills", "output-format", "SKILL.md")))
	if err != nil {
		t.Fatalf("skills/output-format/SKILL.md: %v", err)
	}
	s := string(b)
	flat := strings.Join(strings.Fields(s), " ")

	// every retired claim gone, each with its replacement present
	for _, gone := range []string{"schema-bound envelopes", "wakes the session", "loads again at the next session start", "needs_input", "The `summarize` lane"} {
		if strings.Contains(flat, gone) {
			t.Errorf("the skill still carries the retired claim %q", gone)
		}
	}
	for _, want := range []string{
		"the persistent short rule sets the floor, this skill carries the details", // the division
		"the short output-discipline rule",                                         // what the step/task prompts and context files carry
		"Surface it immediately",                                                   // the decision line's demand, without the wake claim
		"`pf doctor --fix-harness-prompts`",                                        // what persists across sessions and compaction
		"`pf step ask`",                                                            // the envelope face's subject
		"Pi Web caps a panel summary",                                              // the panel face's 200-character fact
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the skill must carry %q", want)
		}
	}
	// the envelope face names the question card in its heading
	if !strings.Contains(s, "## Envelope face (a `pf step ask` question card)") {
		t.Errorf("the envelope face's heading must name the `pf step ask` question card")
	}
}
