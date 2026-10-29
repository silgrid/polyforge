package driver

// scope.go: the verification-tier vocabulary and its pure judgments
//. Verification runs the full bar by default; a work item
// opts into the narrower SCOPED tier with one explicit marker line in its
// content, and only on a scoped-capable lane. A scoped result declares a
// `scope` object; the driver checks the declaration's completeness and
// that the tier was opted into — never which paths the declaration names.
//
// "Scope absent ⇒ full" is the single canonical shape of a full result
// (AGENTS.md: no backward-compatibility code): no code path treats an
// absent scope as a legacy shape needing a shim. Every helper here is
// pure over already-read facts — no I/O, no path classification.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/scopetier"
)

// The tier vocabulary.
const (
	tierFull   = "full"
	tierScoped = "scoped"
)

// scopeChainKey is the driver-written deliver output key carrying the
// three-ring scope audit chain.
const scopeChainKey = "scope_chain"

// scopeAdequateKey is the review output key answering whether a scoped
// verification's declaration was adequate.
const scopeAdequateKey = "scope_adequate"

// scopedLanes is the closed set of lanes allowed to verify scoped.
// autopilot has no review (so no scope_adequate can exist) and release has
// no verification step: both stay full.
var scopedLanes = map[string]bool{
	"feature-full": true,
	"bug-fix":      true,
	"chore":        true,
}

// effectiveTier answers the tier a work item's verification runs at:
// scoped only when the marker is present AND the lane is scoped-capable;
// full otherwise (absent marker, autopilot, release, any other lane).
func effectiveTier(scopedOptIn bool, lane string) string {
	if scopedOptIn && scopedLanes[lane] {
		return tierScoped
	}
	return tierFull
}

// scopeObject is the verification result's optional scope declaration
// (design A2). Prior is present only on an incremental re-verification
// after a review FAIL (design A3).
type scopeObject struct {
	Mode          string
	DeclaredPaths []string
	Rationale     string
	FastGatesRef  string
	Prior         *scopePrior
}

// scopePrior is the original scope an incremental re-verification carries
// forward.
type scopePrior struct {
	Tier          string
	DeclaredPaths []string
}

// stringList coerces a decoded JSON value to a string list. nil is an
// absent list (empty, no error); a non-list or a non-string element is a
// malformed value.
func stringList(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// parseScope decodes one decoded-JSON scope value. A nil value is no
// scope (full). A non-object, or a wrong-typed field, is an error naming
// the defect; every string field is kept verbatim.
func parseScope(v any) (*scopeObject, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scope must be a JSON object")
	}
	s := &scopeObject{}
	if v, ok := m["mode"]; ok && v != nil {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("scope.mode must be a string")
		}
		s.Mode = str
	}
	paths, ok := stringList(m["declared_paths"])
	if !ok {
		return nil, fmt.Errorf("scope.declared_paths must be a list of strings")
	}
	s.DeclaredPaths = paths
	if v, ok := m["rationale"]; ok && v != nil {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("scope.rationale must be a string")
		}
		s.Rationale = str
	}
	if v, ok := m["fast_gates_ref"]; ok && v != nil {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("scope.fast_gates_ref must be a string")
		}
		s.FastGatesRef = str
	}
	if pv, ok := m["prior"]; ok && pv != nil {
		pm, ok := pv.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("scope.prior must be a JSON object")
		}
		p := &scopePrior{}
		if v, ok := pm["tier"]; ok && v != nil {
			str, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("scope.prior.tier must be a string")
			}
			p.Tier = str
		}
		pp, ok := stringList(pm["declared_paths"])
		if !ok {
			return nil, fmt.Errorf("scope.prior.declared_paths must be a list of strings")
		}
		p.DeclaredPaths = pp
		s.Prior = p
	}
	return s, nil
}

// checkScopeComplete enforces the required scope fields when the scope is
// present: mode "scoped", at least one non-empty declared path, a
// rationale and a fast-gates reference. Presence/emptiness only — the
// driver never classifies the declared strings (req 12). A present
// `prior` is validated here too: tier must be scoped and it must name at
// least one declared path.
func checkScopeComplete(s *scopeObject) error {
	if s == nil {
		return fmt.Errorf("scope is absent")
	}
	if strings.TrimSpace(s.Mode) != tierScoped {
		return fmt.Errorf("scope.mode must be %q (got %q)", tierScoped, s.Mode)
	}
	if !anyNonEmpty(s.DeclaredPaths) {
		return fmt.Errorf("scope.declared_paths must name at least one path")
	}
	if strings.TrimSpace(s.Rationale) == "" {
		return fmt.Errorf("scope.rationale is required")
	}
	if strings.TrimSpace(s.FastGatesRef) == "" {
		return fmt.Errorf("scope.fast_gates_ref is required")
	}
	if s.Prior != nil {
		if s.Prior.Tier != tierScoped {
			return fmt.Errorf("scope.prior.tier must be %q (got %q)", tierScoped, s.Prior.Tier)
		}
		if !anyNonEmpty(s.Prior.DeclaredPaths) {
			return fmt.Errorf("scope.prior.declared_paths must name at least one path")
		}
	}
	return nil
}

// anyNonEmpty reports whether the list carries at least one non-blank
// entry.
func anyNonEmpty(list []string) bool {
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// checkVerificationScope enforces the verification scope contract on one
// completed result's output: a present `scope` requires the recorded tier
// to be scoped (the marker was present and the lane is scoped-capable)
// and the declaration to be complete; an absent `scope` is full and
// nothing is checked. The driver performs no path judgment.
func checkVerificationScope(out map[string]any, recordedTier string) error {
	raw, present := out["scope"]
	if !present || raw == nil {
		return nil // the canonical full shape: no scope object
	}
	sc, err := parseScope(raw)
	if err != nil {
		return fmt.Errorf("the verification result's scope is malformed: %w", err)
	}
	if recordedTier != tierScoped {
		return fmt.Errorf("the verification result claims the scoped tier but this work item is not opted in (no `%s` marker, or the lane is not scoped-capable)", scopetier.Marker)
	}
	if err := checkScopeComplete(sc); err != nil {
		return fmt.Errorf("the verification result's scoped declaration is incomplete: %w", err)
	}
	return nil
}

// scopeCredentialUnion answers the merge credential of an incremental
// re-verification: the set-union of the original and increment declared
// paths, prior first, deduplicated, order preserved. The strings are
// opaque — the driver never classifies them.
func scopeCredentialUnion(prior, declared []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(prior)+len(declared))
	for _, list := range [][]string{prior, declared} {
		for _, p := range list {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// reviewScope is the review output's scope record: the
// scope review itself judged adequate for the diff it reviewed — mode
// scoped plus the declared paths, opaque strings the driver never
// classifies. It is the scope the following verification MUST use for
// the two to form one merging credential.
type reviewScope struct {
	Mode          string
	DeclaredPaths []string
}

// reviewScopeRecord reads the review step's recorded scope declaration
//. present is false only when no scope record exists; a
// present-but-malformed record answers (nil, true) — present, but
// proving nothing (fail-closed at the merge rule, which names it).
func reviewScopeRecord(st *capsule.Step) (rec *reviewScope, present bool) {
	if st == nil || st.Status != capsule.StepDone {
		return nil, false
	}
	v, ok := st.Output["scope"]
	if !ok || v == nil {
		return nil, false
	}
	m, isObj := v.(map[string]any)
	if !isObj {
		return nil, true
	}
	rc := &reviewScope{}
	if mode, _ := m["mode"].(string); strings.TrimSpace(mode) != "" {
		rc.Mode = mode
	} else {
		return nil, true
	}
	paths, okPaths := stringList(m["declared_paths"])
	if !okPaths || !anyNonEmpty(paths) {
		return nil, true
	}
	rc.DeclaredPaths = paths
	return rc, true
}

// scopeConsistent answers whether one verification pass used exactly the
// scope review judged adequate: the declared path sets
// are equal — verification's set is the union with its prior when an
// incremental re-verification carried one. The strings stay opaque; a
// missing side is never consistent.
func scopeConsistent(ver *scopeObject, rev *reviewScope) bool {
	if ver == nil || rev == nil {
		return false
	}
	declared := ver.DeclaredPaths
	if ver.Prior != nil {
		declared = scopeCredentialUnion(ver.Prior.DeclaredPaths, ver.DeclaredPaths)
	}
	return samePathSet(declared, rev.DeclaredPaths)
}

// samePathSet answers multiset equality of two opaque path lists.
func samePathSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// verificationScoped reports whether the completed verification step's
// output carried the scoped declaration.
func verificationScoped(st *capsule.Step) bool {
	if st == nil || st.Status != capsule.StepDone {
		return false
	}
	v, ok := st.Output["scope"]
	return ok && v != nil
}

// reviewScopeAdequate reads the review step's recorded scope_adequate
// answer (nil when the field is absent or the step is not done).
func reviewScopeAdequate(st *capsule.Step) *bool {
	if st == nil || st.Status != capsule.StepDone {
		return nil
	}
	b, ok := st.Output[scopeAdequateKey].(bool)
	if !ok {
		return nil
	}
	return &b
}

// rewindToOf answers the optional deliver-only `rewind_to` destination a
// deliver result's output carries ("" when absent or not a string).
func rewindToOf(raw json.RawMessage) string {
	var probe struct {
		RewindTo string `json:"rewind_to"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &probe) != nil {
		return ""
	}
	return strings.TrimSpace(probe.RewindTo)
}
