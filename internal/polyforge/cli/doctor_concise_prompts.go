package cli

// doctor_concise_prompts.go (polyforge#101, AC2): the `harness concise
// guidance` row of `pf doctor`, beside the `harness diagram guidance`
// row (doctor_harness_prompts.go). It reports, per installed harness,
// whether the context file that harness actually loads carries the
// concise-output guidance block (harness.ConciseGuidanceBlock — the one
// short output-discipline rule every step dispatch and every task input
// also carries, from the one source internal/common/harness/concise.go).
// Like the diagram row it never fails and never moves the exit code:
// pass = every installed harness carries the block, warn = some does not
// (the detail carries the block to add), skip = no harness is installed
// here. The report writes nothing; only the explicit
// `--fix-harness-prompts` appends the block — to an EXISTING, readable
// context file, never creating one, never rewriting a byte already
// there. There is no legacy shape to recognize: the block is new.
//
// Claude Code's context file is resolved (owner ruling 2026-11-17):
// ~/.claude/CLAUDE.md when it exists, else ~/.claude/AGENTS.md (the
// built-in cc-plugin-agents-md loads that one when no CLAUDE.md exists)
// — the fix appends to the file the harness actually loads.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// docChConcisePrompts is the row's name in the doctor report.
const docChConcisePrompts = "harness concise guidance"

// appendConciseGuidance appends the concise-output guidance block to
// the existing file at path unless it already carries it.
func appendConciseGuidance(path string) (bool, error) {
	return appendGuidanceBlock(path, harness.ConciseGuidanceBlock(), harness.HasConciseGuidance)
}

// concisePromptCheck judges the context file of every installed harness
// (harnessPromptTargets) for the concise-output guidance block. With
// fix, an existing file lacking the block gets it appended after its
// existing bytes first.
func concisePromptCheck(home, piDir, harnessRoot string, fix bool) doctorCheck {
	c := doctorCheck{Name: docChConcisePrompts}
	var rows []string
	warn, fixable, stepAgentMissing := false, false, false
	for _, t := range harnessPromptTargets(home, piDir, harnessRoot) {
		path := filepath.Join(t.dir, t.file)
		body, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			warn = true
			if t.name == "step-agent" {
				stepAgentMissing = true
			}
			rows = append(rows, fmt.Sprintf("%s %s: no file (not created)", t.name, path))
		case err != nil:
			warn = true
			rows = append(rows, fmt.Sprintf("%s %s: unreadable (%v)", t.name, path, err))
		case harness.HasConciseGuidance(string(body)):
			rows = append(rows, fmt.Sprintf("%s %s: pass", t.name, path))
		default:
			if !fix {
				warn, fixable = true, true
				rows = append(rows, fmt.Sprintf("%s %s: missing", t.name, path))
			} else if _, aerr := appendConciseGuidance(path); aerr != nil {
				warn = true
				rows = append(rows, fmt.Sprintf("%s %s: missing; append failed (%v)", t.name, path, aerr))
			} else {
				rows = append(rows, fmt.Sprintf("%s %s: missing; appended", t.name, path))
			}
		}
	}
	if len(rows) == 0 {
		c.Status, c.Detail = "skip", noHarnessInstalledDetail
		return c
	}
	c.Status, c.Detail = "pass", strings.Join(rows, "; ")
	if !warn {
		return c
	}
	c.Status = "warn"
	if fixable {
		c.Detail += " — run `pf doctor --fix-harness-prompts` to append the concise-output block below after the existing bytes of the file(s) (append-only, idempotent; a missing file is never created)"
	}
	if stepAgentMissing {
		c.Detail += " — " + stepAgentNoFileCaveat
	}
	c.Detail += " — the concise-output guidance block:\n" + harness.ConciseGuidanceBlock()
	return c
}
