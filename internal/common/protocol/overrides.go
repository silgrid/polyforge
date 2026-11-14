package protocol

// overrides.go: the wire shapes of the step-overlay face (polyforge#25) —
// the daemon's per-work-item routes
//
//	GET  /jobs/{wi}/overrides        -> OverrideView
//	POST /jobs/{wi}/overrides        -> OverrideWriteResult
//
// The overlay is the capsule-scoped layer that overrides, per step of
// this one work item, the model chain and the skip switch of a step the
// lane declares skippable. It is written only through the daemon (a
// step agent's process tree is refused DAEMON-403 like every mutating
// route), travels with the capsule chain and outranks every other
// candidate source at dispatch time.

// OverrideStepView is one step's overlay face: its execution status,
// whether the lane declares it skippable, its overlay aspects (with the
// operator and time of each), and the effective model chain the step's
// next dispatch takes with the source it comes from.
type OverrideStepView struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"` // execution.json's status ("" when the step has no record yet)
	// Skippable mirrors the lane snapshot's `skippable` declaration.
	Skippable bool `json:"skippable"`
	// Models is the step's overlay chain (nil = none set).
	Models   []string `json:"models,omitempty"`
	ModelsBy string   `json:"models_by,omitempty"`
	ModelsAt string   `json:"models_at,omitempty"`
	// Skip is tri-state (nil = no skip aspect set).
	Skip   *bool  `json:"skip,omitempty"`
	SkipBy string `json:"skip_by,omitempty"`
	SkipAt string `json:"skip_at,omitempty"`
	// Pending marks the aspects as riding the daemon-local pending
	// record of a work item this daemon no longer holds (its blocked
	// capsule is sealed read-only and abandoned by the next claim's
	// fresh clone): they land with the next claim of the work item here.
	Pending bool `json:"pending,omitempty"`
	// Effective is the candidate chain the step's NEXT dispatch takes.
	Effective []string `json:"effective,omitempty"`
	// Source names where Effective comes from: "step override" (or
	// "step override (pending)"), "pf submit --models", the yaml/bundle
	// level (e.g. steps.<project>.<step>.models), or "" (no list: the
	// harness adapter's default model).
	Source string `json:"source,omitempty"`
}

// OverrideView is the GET answer: the work item's posture on this daemon
// and one row per declared step, in lane order.
type OverrideView struct {
	WorkItem string            `json:"work_item"`
	// Posture is how the daemon holds the work item: running (a live
	// job), paused (a retained paused record) or blocked (no job; the
	// sealed capsule of its last close, plus the pending record).
	Posture string            `json:"posture"`
	Steps   []OverrideStepView `json:"steps"`
}

// OverrideWriteRequest is the POST body: exactly one action —
// Models (set the chain), ClearModels, or Skip (true = skip, false =
// un-skip). Operator names who made the change (the event carries it).
type OverrideWriteRequest struct {
	Step        string   `json:"step"`
	Operator    string   `json:"operator,omitempty"`
	Models      []string `json:"models,omitempty"`
	ClearModels bool     `json:"clear_models,omitempty"`
	Skip        *bool    `json:"skip,omitempty"`
}

// OverrideWriteResult is the POST answer.
type OverrideWriteResult struct {
	WorkItem string `json:"work_item"`
	// Changed is false when the action was a clear that found nothing
	// (the idempotent success: nothing written, no event).
	Changed bool `json:"changed"`
	// Posture is the posture the write landed in (running | paused |
	// blocked).
	Posture string `json:"posture"`
	// Published is true when the write's capsule-chain commit reached
	// the server. False while the work item is paused (aihub refuses
	// pushes for a non-running work item: the local commit stands and
	// the next running-state publish carries it) or blocked (the change
	// rides the daemon-local pending record and enters the chain with
	// the next claim).
	Published bool `json:"published"`
	// Note explains Published=false, and anything else the operator
	// needs to know about where the change landed.
	Note string           `json:"note,omitempty"`
	Step OverrideStepView `json:"step"`
}
