package driver

// stderr_incomplete_test.go: — an exit whose stderr tail was
// read before its copy caught up is recorded as such in the failure
// detail (never a silent exit_other), and the record never feeds the
// classification: the cause stays the text's (the rules are unchanged).

import (
	"errors"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

func TestIncompleteStderrTailIsRecordedNotClassified(t *testing.T) {
	providerErr := `upstream provider error: model "prov/a" not found`
	cases := []struct {
		name      string
		info      spawn.ExitInfo
		wantCause string
		wantNote  bool
	}{
		{"behind, nothing landed: exit_other, and the detail says why",
			spawn.ExitInfo{Code: 101, StderrIncomplete: true}, FailExitOther, true},
		{"behind, the provider error landed: classified by its text, still noted",
			spawn.ExitInfo{Code: 101, StderrTail: providerErr, StderrIncomplete: true}, FailExitModel, true},
		{"caught up: no note",
			spawn.ExitInfo{Code: 101, StderrTail: providerErr}, FailExitModel, false},
		{"caught up, empty tail: exit_other, no note",
			spawn.ExitInfo{Code: 101}, FailExitOther, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := &stepRun{sp: StepSpawn{StepID: "fix", SpawnN: 2}, handle: exitedHandle{c.info}}
			cause, code, detail := classifyWaitFailure(run.exited())
			if cause != c.wantCause || code != 101 {
				t.Fatalf("cause %q code %d, want %q code 101 (detail %q)", cause, code, c.wantCause, detail)
			}
			if got := strings.Contains(detail, spawn.StderrIncompleteNote); got != c.wantNote {
				t.Fatalf("detail %q: note present = %v, want %v", detail, got, c.wantNote)
			}
		})
	}
	// the note itself is no failure text: rendered into every classified
	// field, it matches no class
	note := (&ExitedError{StderrIncomplete: true}).Error()
	if hostPatterns.MatchString(note) || classifyText(note, true) != "" {
		t.Fatalf("the incomplete-tail note %q matches a failure class", note)
	}
	// the startup-death path (the runtime's injectionFailed) carries the
	// note inside the classified error text itself: the stderr decides
	injection := func(stderr string) error {
		return errors.New("runtime: initial prompt injection failed (process torn down): write |1: broken pipe; harness stderr: " +
			stderr + " [" + spawn.StderrIncompleteNote + "]")
	}
	if cause, _, _ := classifyWaitFailure(injection("warn")); cause != FailExitOther {
		t.Fatalf("an incomplete startup-death tail without failure text classified %q, want %q", cause, FailExitOther)
	}
	if cause, _, _ := classifyWaitFailure(injection(providerErr)); cause != FailExitModel {
		t.Fatalf("an incomplete startup-death tail with the provider error classified %q, want %q", cause, FailExitModel)
	}
}

// TestIncompleteStdoutIsRecordedNotClassified: an exit read before the
// stdout reader caught up is recorded as such in the failure detail, and
// the record never feeds the classification: the cause stays the text's.
func TestIncompleteStdoutIsRecordedNotClassified(t *testing.T) {
	providerErr := `upstream provider error: model "prov/a" not found`
	cases := []struct {
		name      string
		info      spawn.ExitInfo
		wantCause string
		wantNote  bool
	}{
		{"behind, nothing landed: exit_other, and the detail says why",
			spawn.ExitInfo{Code: 101, StdoutIncomplete: true}, FailExitOther, true},
		{"behind, the provider error landed: classified by its text, still noted",
			spawn.ExitInfo{Code: 101, StderrTail: providerErr, StdoutIncomplete: true}, FailExitModel, true},
		{"caught up: no note",
			spawn.ExitInfo{Code: 101, StderrTail: providerErr}, FailExitModel, false},
		{"caught up, nothing landed: exit_other, no note",
			spawn.ExitInfo{Code: 101}, FailExitOther, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := &stepRun{sp: StepSpawn{StepID: "fix", SpawnN: 2}, handle: exitedHandle{c.info}}
			cause, code, detail := classifyWaitFailure(run.exited())
			if cause != c.wantCause || code != 101 {
				t.Fatalf("cause %q code %d, want %q code 101 (detail %q)", cause, code, c.wantCause, detail)
			}
			if got := strings.Contains(detail, spawn.StdoutIncompleteNote); got != c.wantNote {
				t.Fatalf("detail %q: stdout note present = %v, want %v", detail, got, c.wantNote)
			}
		})
	}
	// the note itself is no failure text: alone, or rendered into the
	// archived detail, it matches no class
	for _, note := range []string{spawn.StdoutIncompleteNote, (&ExitedError{StdoutIncomplete: true}).Error()} {
		if hostPatterns.MatchString(note) || classifyText(note, true) != "" {
			t.Fatalf("the incomplete-stdout note %q matches a failure class", note)
		}
	}
}
