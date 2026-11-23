package runtime

// override_tier_test.go (polyforge#81 u2, AC2): the overlay record's
// capability-tier aspect — a set records the name, operator and time, a
// clear keeps a timed tombstone in the daemon-local pending record, and
// the pending merge lands the tier over the capsule's same aspect by
// time (newer wins, older loses, a newer clear drops an older chain and
// an older tier cannot resurrect it). The expected values come from
// AC2/Q9 A and #25/#79's recorded-time rule, not from this
// implementation's output; the round-trip goes through the real record
// file, so a write that drops the aspect or a getter that leaks it is
// caught.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestOverrideTierAspect(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, capsule.DirRuntimeState), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*capsule.StepOverride{}
	if changed, e := applyAspect(entries, OverrideChange{Step: "build", Operator: "usr_1", Tier: "main"}, false); !changed ||
		e == nil || e.Tier != "main" || e.TierBy != "usr_1" || e.TierAt == "" || e.TierCleared {
		t.Fatalf("a tier set must record name, operator and time: changed=%v %+v", changed, e)
	}
	// a repeated set changes nothing
	if changed, _ := applyAspect(entries, OverrideChange{Step: "build", Operator: "usr_2", Tier: "main"}, false); changed {
		t.Fatal("a repeated tier set must change nothing")
	}
	// the model chain and the tier aspect coexist (Q9 A)
	entries["build"].Models = []string{"pi:prov/m"}
	if err := capsule.WriteStepOverrides(dir, capsule.StepOverrides{Steps: entries}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(capsule.StepOverridesJSON)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tier": "main"`) || !strings.Contains(string(raw), `"models"`) {
		t.Fatalf("the record must carry the tier beside the model chain:\n%s", raw)
	}
	back, err := capsule.ReadStepOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b := back.Steps["build"]; b == nil || b.Tier != "main" || b.TierBy != "usr_1" || len(b.Models) != 1 {
		t.Fatalf("tier round-trip: %+v", b)
	}

	// the pending record's clear keeps its operator and time; a repeated
	// clear changes nothing.
	if changed, e := applyAspectAt(entries, OverrideChange{Step: "build", Operator: "usr_3", ClearTier: true}, true, false); !changed ||
		e.Tier != "" || e.TierBy != "usr_3" || e.TierAt == "" || !e.TierCleared {
		t.Fatalf("a pending tier clear must leave a timed tombstone: changed=%v %+v", changed, e)
	}
	if changed, _ := applyAspectAt(entries, OverrideChange{Step: "build", Operator: "usr_4", ClearTier: true}, true, false); changed {
		t.Fatal("a repeated pending tier clear must change nothing")
	}
}

func TestOverrideTierPendingMerge(t *testing.T) {
	ov := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"build": {Tier: "old", TierBy: "usr_cap", TierAt: "2026-11-23T00:00:00Z"},
	}}
	pending := map[string]*capsule.StepOverride{
		// newer pending tier wins
		"build": {Tier: "new", TierBy: "usr_pend", TierAt: "2026-11-23T01:00:00Z"},
	}
	merged, applied := MergedStepOverrides(ov, pending)
	if e := merged.Steps["build"]; e == nil || e.Tier != "new" || e.TierBy != "usr_pend" || e.TierAt != "2026-11-23T01:00:00Z" {
		t.Fatalf("a newer pending tier must win: %+v", e)
	}
	if !applied["build"].Tier {
		t.Fatalf("the merge must name the tier aspect as pending-supplied: %+v", applied["build"])
	}

	// an older pending tier loses to the capsule's
	ov = capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"build": {Tier: "old", TierBy: "usr_cap", TierAt: "2026-11-23T02:00:00Z"},
	}}
	pending = map[string]*capsule.StepOverride{
		"build": {Tier: "new", TierBy: "usr_pend", TierAt: "2026-11-23T01:00:00Z"},
	}
	merged, applied = MergedStepOverrides(ov, pending)
	if e := merged.Steps["build"]; e == nil || e.Tier != "old" {
		t.Fatalf("an older pending tier must lose: %+v", e)
	}
	if applied["build"].Tier {
		t.Fatalf("an older pending tier must not be named applied: %+v", applied["build"])
	}

	// a newer pending clear tombstone drops the capsule's tier, and an
	// older pending tier cannot bring it back afterwards.
	ov = capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"build": {Tier: "old", TierBy: "usr_cap", TierAt: "2026-11-23T00:00:00Z"},
	}}
	pending = map[string]*capsule.StepOverride{
		"build": {TierBy: "usr_clear", TierAt: "2026-11-23T03:00:00Z", TierCleared: true},
	}
	merged, applied = MergedStepOverrides(ov, pending)
	if e := merged.Steps["build"]; e == nil || e.Tier != "" || e.TierBy != "usr_clear" || e.TierAt != "2026-11-23T03:00:00Z" {
		t.Fatalf("a newer pending clear must drop the capsule's tier: %+v", e)
	}
	if !applied["build"].Tier {
		t.Fatalf("the clear must be named an applied tier aspect: %+v", applied["build"])
	}
	ov2 := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"build": {TierBy: "usr_clear", TierAt: "2026-11-23T03:00:00Z"},
	}}
	older := map[string]*capsule.StepOverride{
		"build": {Tier: "old", TierBy: "usr_pend", TierAt: "2026-11-23T02:00:00Z"},
	}
	merged, _ = MergedStepOverrides(ov2, older)
	if e := merged.Steps["build"]; e == nil || e.Tier != "" || e.TierAt != "2026-11-23T03:00:00Z" {
		t.Fatalf("an older pending tier must not resurrect a cleared tier: %+v", e)
	}

	// an unrelated aspect does not mark the tier as pending-supplied
	ov = capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"build": {Tier: "old", TierBy: "usr_cap", TierAt: "2026-11-23T00:00:00Z"},
	}}
	yes := true
	pending = map[string]*capsule.StepOverride{
		"build": {Skip: &yes, SkipAt: "2026-11-23T05:00:00Z"},
	}
	if _, applied = MergedStepOverrides(ov, pending); applied["build"].Tier {
		t.Fatalf("a pending skip must not mark the tier aspect: %+v", applied["build"])
	}
}
