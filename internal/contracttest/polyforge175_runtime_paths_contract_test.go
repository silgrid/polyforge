// carries: docs/reference/commands.md, docs/workstation.md, docs/upgrade.md,
// skills/polyforge/verbs/supervise.md, skills/polyforge/verbs/doctor.md,
// test/w254_docs_set_acceptance_test.go, dist/workstation/entrypoint.sh,
// internal/polyforge/supervisor/supervise.go

package test

// polyforge175_runtime_paths_contract_test.go (polyforge#175, 84-components-B):
// the runtime-paths change's documents follow the code — the supervise
// verb doc and the commands reference name the 75 semantics and the
// components volume's current pf, the doctor verb doc's layout rows name
// the image's components current (and the pre-#162 refusal), the
// workstation doc names the base pf's absolute path, and
// docs/upgrade.md's appended section is the last one with its line in
// the byte-identity pin's list.

import (
	"strings"
	"testing"
)

func TestPF175RuntimePathsContract(t *testing.T) {
	// the supervise verb doc: the 75 semantics and the current paths
	supervise := pf101Read(t, "skills/polyforge/verbs/supervise.md")
	for _, want := range []string{
		"exits 75 (the restart-for-an-upgrade",
		"no failure backoff",
		"/components/current/bin/pf",
		"/usr/local/libexec/polyforge/pf",
	} {
		if !strings.Contains(supervise, want) {
			t.Errorf("skills/polyforge/verbs/supervise.md lacks %q", want)
		}
	}

	// the commands reference's supervise section: the same facts
	commands := pf101Read(t, "docs/reference/commands.md")
	for _, want := range []string{
		"exiting 75 (the restart-for-an-upgrade",
		"no failure backoff",
		"/components/current/bin/pf",
		"/components/current/workflows",
		"/components/current/pi-web",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md lacks %q", want)
		}
	}

	// the doctor verb doc's layout rows name the image's components
	// current and the pre-#162 refusal
	doctor := pf101Read(t, "skills/polyforge/verbs/doctor.md")
	for _, want := range []string{
		"/components/current/skills/polyforge",
		"/components/current",
		"pre-#162",
	} {
		if !strings.Contains(doctor, want) {
			t.Errorf("skills/polyforge/verbs/doctor.md lacks %q", want)
		}
	}

	// the workstation doc names the base pf's absolute path
	workstation := pf101Read(t, "docs/workstation.md")
	if !strings.Contains(workstation, "/usr/local/libexec/polyforge/pf") {
		t.Error("docs/workstation.md lacks the base pf's absolute path /usr/local/libexec/polyforge/pf")
	}

	// upgrade.md: the appended section is the LAST one and the pin list
	// carries its whole line (the append-only bar)
	upgrade := w68Raw(t, "docs/upgrade.md")
	heading := "## v0.2.4, the runtime paths and the upgrade-restart exit (polyforge#175)"
	if !strings.Contains(upgrade, heading) {
		t.Errorf("docs/upgrade.md lacks the appended section %q", heading)
	}
	if strings.LastIndex(upgrade, heading) < strings.LastIndex(upgrade, "## v0.2.4, the component seed and install transaction (polyforge#162)") {
		t.Error("docs/upgrade.md is append-only — the runtime-paths section must come after the component-seed one")
	}
	pin := w68Raw(t, "test/w254_docs_set_acceptance_test.go")
	b := strings.Index(pin, "// upgrade-sections:begin")
	e := strings.Index(pin, "// upgrade-sections:end")
	if b < 0 || e < b || !strings.Contains(pin[b:e], "the runtime paths and the upgrade-restart exit (polyforge#175") {
		t.Error("the upgrade-sections pin list lacks the runtime-paths section's line (TestW254UpgradeByteIdentical's comment)")
	}
}
