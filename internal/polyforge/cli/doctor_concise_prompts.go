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

// docChConcisePrompts is the row's name in the doctor report.
const docChConcisePrompts = "harness concise guidance"

// concisePromptCheck judges every installed harness's context file for
// the concise-output guidance block; fix appends it where missing.
// Stub ahead of the tests (polyforge#101 u2): the zero-value body keeps
// the first red an assertion, never a compile error.
func concisePromptCheck(home, piDir, harnessRoot string, fix bool) doctorCheck {
	_ = home
	_ = piDir
	_ = harnessRoot
	_ = fix
	return doctorCheck{Name: docChConcisePrompts}
}
