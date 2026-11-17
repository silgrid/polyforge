package test

// carries: AGENTS.md, internal/contracttest/pf123_upgrade_section_version_rule_contract_test.go

// pf123_upgrade_section_version_rule_contract_test.go (polyforge#123,
// AC3): the repo's AGENTS.md names, in its coupled obligations, the rule a
// build follows when it appends a section to docs/upgrade.md — the
// heading version is the next patch of the latest tag, unless the ticket
// names the version — together with the exception that sanctions this very
// change: a section the latest tag does not include (not yet released)
// may be corrected in place, heading version included, while a released
// section's bytes are never rewritten. Without the written rule a build
// copies the file's newest heading, which is how the #121, #122 and #118
// sections came to say v0.2.4. The rule points at the guard test, and
// the guard must still carry what it points at — the shape w111's pin
// check keeps for the append-only rule itself.

import (
	"strings"
	"testing"
)

// TestPF123AgentsMDNamesTheNewSectionVersionRule: the coupled obligations
// carry the new-section version rule and the unreleased-section
// in-place-correction exception, and the guard test the rule names
// exists and still pins the version rule.
func TestPF123AgentsMDNamesTheNewSectionVersionRule(t *testing.T) {
	agents := w68Flat(t, "AGENTS.md")
	at := strings.Index(agents, "**Coupled obligations.**")
	if at < 0 {
		t.Fatal("AGENTS.md lost its coupled-obligations rule")
	}
	coupled := agents[at:]
	if i := strings.Index(coupled, "<!-- fork-only:begin -->"); i >= 0 {
		coupled = coupled[:i]
	}
	for _, want := range []string{
		"a section appended to `docs/upgrade.md` takes as its heading version the next patch of the latest tag",
		"unless the ticket names the version",
		"a section the latest tag does not include may still be corrected in place",
		"a released section's bytes are never rewritten",
	} {
		if !strings.Contains(coupled, want) {
			t.Errorf("AGENTS.md coupled obligations must carry the new-section version rule (polyforge#123, AC3), missing %q", want)
		}
	}
	// The rule points at a real guard, not a stale path: the named test
	// file carries the explicit-version constant and the next-patch
	// derivation (the w111 shape: a rule that names its pin keeps it).
	pin := w68Raw(t, "test/pf123_upgrade_section_version_test.go")
	if !strings.Contains(pin, "pf123WantNextVersion") || !strings.Contains(pin, "the latest tag's next patch") {
		t.Error("the AGENTS.md rule names test/pf123_upgrade_section_version_test.go, which must still pin the version rule: the pf123WantNextVersion override constant and the latest tag's next-patch derivation (polyforge#123, AC3)")
	}
}
