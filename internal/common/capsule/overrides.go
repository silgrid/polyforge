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

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

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
	// ModelsCleared is the daemon-local pending record's clear tombstone
	// (never in a capsule's own record): a clear recorded while the work
	// item was held by no job, of a chain its sealed capsule already
	// carries — the next claim's merge drops that chain.
	ModelsCleared bool `json:"models_cleared,omitempty"`
	// Skip is tri-state: nil = no skip aspect set (a pointer so absent and
	// false stay distinct on the wire).
	Skip   *bool  `json:"skip,omitempty"`
	SkipBy string `json:"skip_by,omitempty"`
	SkipAt string `json:"skip_at,omitempty"`
}

// emptyStepOverrides answers the record's empty shape (a non-nil steps
// map, the shape every writer keeps on the wire).
func emptyStepOverrides() StepOverrides {
	return StepOverrides{Steps: map[string]*StepOverride{}}
}

// ReadStepOverrides reads the capsule's step-overlay record; a capsule
// without one (the common case) answers an empty record, while a torn or
// unreadable one is an error — the write path must not silently
// overwrite state it could not read.
func ReadStepOverrides(capsuleDir string) (StepOverrides, error) {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(StepOverridesJSON)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return emptyStepOverrides(), nil
		}
		return StepOverrides{}, fmt.Errorf("read %s: %w", StepOverridesJSON, err)
	}
	var ov StepOverrides
	if err := json.Unmarshal(raw, &ov); err != nil {
		return StepOverrides{}, fmt.Errorf("%s does not parse (not overwriting it; fix or remove the file, then retry): %w", StepOverridesJSON, err)
	}
	if ov.Steps == nil {
		ov.Steps = map[string]*StepOverride{}
	}
	return ov, nil
}

// WriteStepOverrides writes the overlay record to capsuleDir atomically.
// An entry that carries no aspect is dropped before the write (a reader
// never sees an empty step entry); the steps map itself stays an object,
// never null.
func WriteStepOverrides(capsuleDir string, ov StepOverrides) error {
	if ov.Steps == nil {
		ov.Steps = map[string]*StepOverride{}
	}
	for id, e := range ov.Steps {
		if e == nil || (len(e.Models) == 0 && e.ModelsBy == "" && e.ModelsAt == "" && e.Skip == nil && e.SkipBy == "" && e.SkipAt == "") {
			delete(ov.Steps, id)
		}
	}
	blob, err := json.MarshalIndent(ov, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(capsuleDir, filepath.FromSlash(StepOverridesJSON)), append(blob, '\n'), 0o644)
}

// OverlaySkips answers the ids of the steps whose overlay sets the skip
// switch, sorted. Whether a step may be skipped is the lane's
// declaration (the driver's WithSkipped applies it); this list only
// names what the overlay asked for.
func OverlaySkips(ov StepOverrides) []string {
	var out []string
	for id, e := range ov.Steps {
		if e != nil && e.Skip != nil && *e.Skip {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
