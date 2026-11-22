package contracttest

// The pi sub-agent compatibility gate's documentation contract
// (polyforge#227 AC4): the upgrade note states default-off, the on-only
// full check, the refusal at that dispatch, the five classes, the one
// 10-second budget, the in-memory cache, the absent version floor and
// --no-approve; config.md states WHEN the switch refuses; the workflow
// contract and the design note state the same gate. A released section's
// bytes are never asserted here — the append-only pin owns those.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func piCompatDoc(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

func TestPiCompatibilityDocs(t *testing.T) {
	upgrade := piCompatDoc(t, "docs/upgrade.md")
	i := strings.Index(upgrade, "(polyforge#227)")
	if i < 0 {
		t.Fatal("docs/upgrade.md carries no polyforge#227 section")
	}
	start := strings.LastIndex(upgrade[:i], "## ")
	section := upgrade[start:]
	if end := strings.Index(section[1:], " ## "); end >= 0 {
		section = section[:end+1]
	}
	for _, want := range []string{
		"pi_subagents",
		"default off",
		"--no-approve",
		"pi_version",
		"pi_flag",
		"pi_api",
		"pi_tool_registration",
		"pi_timeout",
		"10-second budget",
		"in memory",
		"no version floor",
		"refused at that dispatch",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the upgrade note on the pi compatibility gate lacks %q", want)
		}
	}

	config := piCompatDoc(t, "docs/reference/config.md")
	if !strings.Contains(config, "pi_subagents") {
		t.Fatal("config.md does not document pi_subagents")
	}
	for _, want := range []string{
		"refused at that dispatch",
		"pi_version",
		"probe",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config.md lacks %q — it must state that an invalid value or a failing on check is refused at dispatch, not when the value is written", want)
		}
	}

	contract := piCompatDoc(t, "docs/reference/workflow-contract.md")
	for _, want := range []string{
		"pi_subagents: on",
		"startup checks pass",
		"guard",
		"capability probe",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("the workflow contract's prompt facts lack %q", want)
		}
	}

	design := piCompatDoc(t, "docs/design/polyforge-vnext/v2-converge.md")
	for _, want := range []string{
		"10-second budget",
		"pi_tool_registration",
		"cached in memory",
	} {
		if !strings.Contains(design, want) {
			t.Errorf("the design note's isolation section lacks the startup gate fact %q", want)
		}
	}
}
