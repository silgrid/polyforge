package harness

// concise.go (polyforge#101): the one short output-discipline rule —
// concise, plain speech — every agent surface carries: the harness
// context files (appended by `pf doctor --fix-harness-prompts` as a
// marked block, beside the diagram guidance block), every step dispatch
// (driver.assembleStepPrompt, under the prohibited actions) and every
// task-lane input (task.dispatchedPrompt, the daemon's ticket-analysis
// prompt). One source, English (the reply language is a separate
// instruction), project-neutral (no project or component name: it ships
// with the binary and serves any project): every surface renders
// ConciseRule's exact bytes, pinned byte-identical by the contract
// tests. The rule is the floor; the output-format skill (loaded by the
// interactive session) carries the details.
//
// The stub bodies ship one ordinary commit ahead of the tests so the
// first red is an assertion, never a compile error.

// The concise-guidance block's markers: the idempotency and rollback
// anchors (the doctor appends the block between them; deleting the two
// marker lines and what sits between rolls it back).
const (
	conciseGuidanceBegin = "<!-- polyforge:concise-guidance:begin -->"
	conciseGuidanceEnd   = "<!-- polyforge:concise-guidance:end -->"
)

// ConciseRule answers the one short output-discipline rule, its `## `
// heading included, ending in a newline — the exact bytes every surface
// (the doctor block, the step prompt, the three task prompts) carries.
func ConciseRule() string {
	return ""
}

// ConciseGuidanceBlock answers the marked block the doctor appends to a
// harness context file that lacks it: the begin marker, the rule, the
// end marker — the same shape as the diagram guidance block.
func ConciseGuidanceBlock() string {
	return ""
}

// HasConciseGuidance reports whether a context file's body carries the
// concise-guidance block (its begin marker).
func HasConciseGuidance(body string) bool {
	return false
}
