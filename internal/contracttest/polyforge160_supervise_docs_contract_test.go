// carries: docs/reference/commands.md, docs/reference/uds-routes.md, docs/install.md, skills/polyforge/verbs/doctor.md, skills/polyforge/verbs/ops.md, skills/polyforge/verbs/supervise.md, docs/upgrade.md, internal/polyforge/supervisor/supervise.go, internal/polyforge/daemon/upgradedrain.go

package test

// polyforge160_supervise_docs_contract_test.go (polyforge#160, AC2): the
// container-supervision change's documents follow the code — the commands
// reference names `pf supervise` (its Process-verb row and its own
// section, with the ensure no-op note), the doctor verb doc carries the
// container pass row and the tmux exemption, the ops verb doc names the
// drain action, the UDS route table names POST /ops/drain, and
// docs/upgrade.md's appended section is listed in the byte-identity
// pin's comment (the pin itself is TestW254UpgradeByteIdentical's bar).

import (
	"strings"
	"testing"
)

// pf160Read answers one repo file, whitespace collapsed.
func pf160Read(t *testing.T, rel string) string {
	t.Helper()
	return pf101Read(t, rel)
}

func TestPF160SuperviseDocsContract(t *testing.T) {
	// the commands reference: the Process row names the verb, the verb has
	// its own section (the marker, the grace, the children), the daemon
	// section notes the container no-op, and the ops row names the drain
	commands := pf160Read(t, "docs/reference/commands.md")
	for _, want := range []string{
		"`supervise` (run the container process supervisor",
		"## `pf supervise` — the container process supervisor (polyforge#160)",
		"`POLYFORGE_SUPERVISOR=container:pf-supervise`",
		"the `--grace` (default 60s, floor 45s) to finish its shutdown",
		"`pf daemon ensure` is a successful no-op that starts nothing",
		"`drain` — `drain --upgrade [--force]`, the Host-level upgrade drain",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md lacks %q", want)
		}
	}

	// the doctor verb doc: the container pass row and the tmux exemption
	doctor := pf160Read(t, "skills/polyforge/verbs/doctor.md")
	for _, want := range []string{
		"the container supervision marker in its environment (`POLYFORGE_SUPERVISOR=container:…`, what `pf supervise` sets)",
		"tmux is NOT required — `pf supervise` watches the daemon and the container image carries no tmux",
		"inside that container `pf daemon ensure` is a successful no-op that starts nothing",
	} {
		if !strings.Contains(doctor, want) {
			t.Errorf("skills/polyforge/verbs/doctor.md lacks %q", want)
		}
	}

	// the ops verb doc: the drain action, its success and its refusal
	ops := pf160Read(t, "skills/polyforge/verbs/ops.md")
	for _, want := range []string{
		"pf ops drain --upgrade [--force]",
		"the upgrade refusal of new work covers every Cell and takes no `--profile`",
		"the drain IS set (new work is refused from now on)",
	} {
		if !strings.Contains(ops, want) {
			t.Errorf("skills/polyforge/verbs/ops.md lacks %q", want)
		}
	}

	// the supervise verb doc exists and points the operator at the flows
	supervise := pf160Read(t, "skills/polyforge/verbs/supervise.md")
	for _, want := range []string{
		"the container process supervisor (the image's entry point)",
		"`pf daemon ensure` is a successful no-op under this supervision",
	} {
		if !strings.Contains(supervise, want) {
			t.Errorf("skills/polyforge/verbs/supervise.md lacks %q", want)
		}
	}

	// the UDS route table names the new Host route
	routes := pf160Read(t, "docs/reference/uds-routes.md")
	if !strings.Contains(routes, "| POST | `/ops/drain` |") {
		t.Error("docs/reference/uds-routes.md lacks the POST /ops/drain row")
	}

	// install.md's tmux paragraph names the container exception
	install := pf160Read(t, "docs/install.md")
	if !strings.Contains(install, "its image entry point is `pf supervise`") {
		t.Error("docs/install.md's tmux paragraph lacks the container exception")
	}

	// upgrade.md: the appended section is the LAST one and the pin's list
	// carries its whole line (the append-only bar)
	upgrade := w68Raw(t, "docs/upgrade.md")
	heading := "## v0.2.4, the container supervisor and the upgrade drain (polyforge#160)"
	if !strings.Contains(upgrade, heading) {
		t.Errorf("docs/upgrade.md lacks the appended section %q", heading)
	}
	if strings.LastIndex(upgrade, heading) < strings.LastIndex(upgrade, "## v0.2.4, the per-clone-path cache volume (polyforge#153)") {
		t.Error("docs/upgrade.md is append-only — the new section must come after the cache-volume one")
	}
	// the pin list lives in test/w254's own source comment; check the
	// whole line is there between the two markers
	pin := w68Raw(t, "test/w254_docs_set_acceptance_test.go")
	b := strings.Index(pin, "// upgrade-sections:begin")
	e := strings.Index(pin, "// upgrade-sections:end")
	if b < 0 || e < b || !strings.Contains(pin[b:e], "the container supervisor and the upgrade drain (polyforge#160") {
		t.Error("the upgrade-sections pin list lacks the new section's line (TestW254UpgradeByteIdentical's comment)")
	}
}
