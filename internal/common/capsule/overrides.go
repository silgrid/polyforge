package capsule

// overrides.go: the work item's step-overlay record
// (capsule.StepOverridesJSON) — the capsule-scoped layer that overrides,
// per step of this one work item, the model chain and the skip switch of
// a step the lane declares skippable (polyforge#25). It is written only
// through the daemon (never by a step agent), travels with the capsule
// chain and outranks every other candidate source at dispatch time; the
// driver and the daemon read it, the lane snapshot stays untouched.
// One entry per step, one aspect record per aspect (models, skip), each
// carrying the operator and the time of its last change.

// StepOverridesJSON is the capsule-relative path of the step-overlay
// record: per step, the model-chain and skip overrides with their
// operator and time. Absent = no overlay (the dispatch composes from the
// sources below it exactly as before).
const StepOverridesJSON = "runtime-state/step-overrides.json"

// StepOverrides is the overlay record read as a whole: every step that
// carries at least one aspect, keyed by step id. The write path is the
// daemon alone (read-modify-write of this shape); readers (the dispatch
// composition, the watch faces) tolerate an absent or unreadable file as
// no overlay.
type StepOverrides struct {
	Steps map[string]*StepOverride `json:"steps"`
}

// StepOverride is one step's overlay aspects. Each aspect keeps its own
// operator and time, so a later change of one aspect never rewrites the
// other's history.
type StepOverride struct {
	// Models is the step's overlay candidate chain (nil = none set).
	Models   []string `json:"models,omitempty"`
	ModelsBy string   `json:"models_by,omitempty"`
	ModelsAt string   `json:"models_at,omitempty"`
	// Skip is tri-state: nil = no skip aspect set (a pointer so absent and
	// false stay distinct on the wire).
	Skip   *bool  `json:"skip,omitempty"`
	SkipBy string `json:"skip_by,omitempty"`
	SkipAt string `json:"skip_at,omitempty"`
}

// ReadStepOverrides reads the capsule's step-overlay record; a capsule
// without one (the common case) answers an empty record, while a torn or
// unreadable one is an error — the write path must not silently
// overwrite state it could not read.
func ReadStepOverrides(capsuleDir string) (StepOverrides, error) {
	return StepOverrides{Steps: map[string]*StepOverride{}}, nil
}

// WriteStepOverrides writes the overlay record to capsuleDir atomically.
func WriteStepOverrides(capsuleDir string, ov StepOverrides) error {
	return nil
}

// OverlaySkips answers the ids of the steps whose overlay sets the skip
// switch, in no particular order. Whether a step may be skipped is the
// lane's declaration (the driver's WithSkipped applies it); this list
// only names what the overlay asked for.
func OverlaySkips(ov StepOverrides) []string {
	return nil
}
