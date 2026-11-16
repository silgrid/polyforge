package daemon

// analysis_prompt.go (polyforge#72, AC7): the analysis task's prompt
// template, embedded into the daemon binary with go:embed so it ships
// with the executable and never depends on a skills directory being in
// sync. The template's text is project-neutral (no project or component
// name) and carries the 7-item report format defined by
// skills/polyforge/verbs/submit.md, the heading shape every item must
// use (`### <n>. <标题>`) and the epic-upgrade recommendation section
// on ruling 18's default rules — pinned by
// internal/contracttest/w72_analysis_prompt_contract_test.go, which
// reads this same file from the repository tree (the embedded copy IS
// this file at compile time).

import _ "embed"

// AnalysisPrompt is the embedded template. The analysis scheduler
// appends the ticket's own facts to it: the ticket body and the
// section's current draft (AC10: a candidate that takes over continues
// what its predecessor already wrote).
//
//go:embed analysis_prompt.md
var AnalysisPrompt string
