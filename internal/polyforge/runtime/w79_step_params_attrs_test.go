package runtime

// w79_step_params_attrs_test.go (u11, polyforge#79; review r3#2): the
// reserved step_params attrs key has ONE writer — the daemon's queued
// write — and it writes one entry OBJECT per param, {"value": …, "at": …}.
// Nothing else ever rides the key (the bare-string shape existed only in
// this change's own intermediate commits and never shipped; AGENTS.md
// keeps no compat path), so the parser drops it: a bare string entry, an
// entry whose value is missing, null or not a string, an entry without a
// name, junk that is no map at all — none of it reaches a claim. A key
// that holds nothing usable answers nil. The expected values come from
// the write side's own contract (the daemon stamps value and at per
// entry), never from the parser's output. The change it catches:
// re-adding a bare-value branch or keeping a valueless entry as "" would
// turn this red — the pre-change parser kept both.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestW79StepParamsOfAttrsDropsUnusableEntries(t *testing.T) {
	attrs := map[string]any{api.StepParamsKey: map[string]any{
		"build": map[string]any{
			// the one entry the key's writer produces: kept, value and
			// write time both
			"image": map[string]any{"value": "reg/x:1", "at": "2026-01-01T00:00:00Z"},
			// the pre-change bare-string shape (never shipped): junk
			"bare": "reg/x:2",
			// entry objects without a usable value: junk — never ""
			"null": map[string]any{"value": nil},
			"gone": map[string]any{"at": "2026-01-01T00:00:00Z"},
			"num":  map[string]any{"value": 3},
			// a param without a name: junk
			"": map[string]any{"value": "x"},
		},
		// a step entry that is no map at all: junk
		"bare-step": "junk",
	}}
	got := StepParamsOfAttrs(attrs)
	if len(got) != 1 || len(got["build"]) != 1 {
		t.Fatalf("only the entry the writer produces survives: %+v", got)
	}
	if e := got["build"]["image"]; e.Value != "reg/x:1" || e.At != "2026-01-01T00:00:00Z" {
		t.Fatalf("the kept entry's value and write time: %+v", e)
	}
	// a key that holds nothing usable answers nil
	if StepParamsOfAttrs(map[string]any{api.StepParamsKey: map[string]any{
		"build": map[string]any{"bare": "x", "null": map[string]any{"value": nil}},
	}}) != nil {
		t.Fatal("a key holding nothing usable answers nil")
	}
}
