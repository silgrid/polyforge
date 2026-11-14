package test

// w320_step_text_contract_test.go: the shared review step text carries the
// supply-chain checklist for code from outside the team, unconditionally (no
// label, field or template selection reaches it), applying only to a change
// that brings in outside-team code and saying it does not apply otherwise, and
// in project-neutral wording: review reads the four readings from the diff and
// judges them, an unexplained hit blocks unless an owner ruling is on record,
// and the upstream item is N/A when no upstream is claimed. A revert of the
// section is red. (The agent verification step that recorded the readings as
// named checks was deleted.)
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW320StepText' -count=1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const w320stHeading = "## Supply-chain checklist (outside code)"

var w320stItems = []string{
	"**New scripts and install hooks**",
	"**New network endpoints**",
	"**Dependency diff**",
	"**Upstream consistency**",
}

// w320stSection answers the checklist section of a shared step text: from its
// heading to the next `## ` heading.
func w320stSection(t *testing.T, step string) string {
	t.Helper()
	p := filepath.Join("..", "workflows", "_shared", "steps", step+".md")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	// The section lives in the prompt body, after the frontmatter, so it
	// renders on every dispatch.
	if parts := strings.SplitN(body, "\n---\n", 2); len(parts) == 2 {
		body = parts[1]
	}
	i := strings.Index(body, "\n"+w320stHeading+"\n")
	if i < 0 {
		t.Fatalf("%s must carry the section %q in its prompt body", p, w320stHeading)
	}
	sec := body[i+1:]
	if j := strings.Index(sec[len(w320stHeading):], "\n## "); j >= 0 {
		sec = sec[:len(w320stHeading)+j]
	}
	return sec
}

func w320stRequire(t *testing.T, step, sec string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(sec, w) {
			t.Fatalf("%s's supply-chain checklist must say %q:\n%s", step, w, sec)
		}
	}
}

// TestW320StepTextChecklistRenders: the review text carries the heading and
// the four item labels, unconditionally and project-neutrally.
func TestW320StepTextChecklistRenders(t *testing.T) {
	for _, step := range []string{"review"} {
		sec := w320stSection(t, step)
		w320stRequire(t, step, sec, w320stItems...)
		w320stRequire(t, step, sec, "code written outside the team", "absorbed external pull request", "vendored third-party source")
		// The section renders always but self-triggers: its readings and
		// judgment apply only to a change bringing in outside-team code, and
		// it says it does not apply otherwise (spec; grill r1 row 12).
		w320stRequire(t, step, sec,
			"This section applies only to a change that brings in code written outside the team",
			"on\nany other change it does not apply")
		if strings.Contains(strings.ToLower(sec), "on every change") {
			t.Fatalf("%s's checklist must not gate every change; it self-triggers on outside-team code only:\n%s", step, sec)
		}
		if strings.Contains(sec, "{{") || strings.Contains(sec, "x-prompt-when") {
			t.Fatalf("%s's checklist must render unconditionally (no template selection):\n%s", step, sec)
		}
		lower := strings.ToLower(sec)
		for _, internal := range []string{"workbench", "gmi", "orbit", "silgrid", "label"} {
			if strings.Contains(lower, internal) {
				t.Fatalf("%s's checklist must stay project-neutral and carry no selector (found %q):\n%s", step, internal, sec)
			}
		}
	}
}

// TestW320StepTextReviewJudges: review judges the readings, an unexplained hit
// is blocking unless an owner ruling is on record, and that ruling is the only
// exit.
func TestW320StepTextReviewJudges(t *testing.T) {
	sec := w320stSection(t, "review")
	w320stRequire(t, "review", sec,
		"judge each hit",
		"An unexplained hit is `blocking` unless an owner ruling on record covers\nit: a recorded owner ruling is the only exit",
		"N/A, never a finding",
		"read the diff for\nthese four readings")
}
