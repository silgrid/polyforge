package spawn

import (
	"errors"
	"strings"
	"testing"
)

// TestPiSubagentsParamContract pins the one pi_subagents parse rule the
// driver and the runtime share (polyforge#227, owner ruling 2026-11-22 Q1):
// empty and off disable the worker, on enables it after trimming and
// case-folding, and every other value is refused before any probe or
// dispatch.
func TestPiSubagentsParamContract(t *testing.T) {
	disabled := []map[string]string{
		nil,
		{},
		{"pi_subagents": ""},
		{"pi_subagents": "off"},
		{"pi_subagents": " OFF "},
	}
	for _, params := range disabled {
		if on, err := PiSubagents(params); err != nil || on {
			t.Errorf("PiSubagents(%v) = (%v, %v), want (false, nil)", params, on, err)
		}
	}
	enabled := []string{"on", "ON", " On "}
	for _, v := range enabled {
		if on, err := PiSubagents(map[string]string{"pi_subagents": v}); err != nil || !on {
			t.Errorf("PiSubagents(%q) = (%v, %v), want (true, nil)", v, on, err)
		}
	}
	for _, v := range []string{"yes", "1", "true", "enable", "on,off"} {
		on, err := PiSubagents(map[string]string{"pi_subagents": v})
		if err == nil || on {
			t.Errorf("PiSubagents(%q) = (%v, %v), want a refusal", v, on, err)
		}
		var invalid *InvalidPiSubagentsError
		if !errors.As(err, &invalid) {
			t.Fatalf("PiSubagents(%q) error is %T, want *InvalidPiSubagentsError", v, err)
		}
		for _, want := range []string{"pi_subagents", v, "want on or off"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("PiSubagents(%q) error %q lacks %q", v, err, want)
			}
		}
	}
}
