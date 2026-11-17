package test

// carries: tools/install.sh, test/w20_install_wording_test.go

// w20_install_wording_test.go (polyforge#20, review r1#1): the
// installer's mapping-gate wordings print VERBATIM. The two lines that
// name `pf daemon ensure` inside a double-quoted echo must carry escaped
// backticks — unescaped, the shell runs a command substitution at print
// time: a fresh install really ran `pf daemon ensure` once (its refusal
// spilling into the installer's output) and both hints lost those very
// words. Red on the unfixed tree: neither hint prints verbatim there.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestW20InstallGateWording (review r1#1): both gate wordings print
// literally — the fresh-install hint, the step-4 SKIP line — and no
// stray `pf daemon ensure` runs while printing them.
func TestW20InstallGateWording(t *testing.T) {
	t.Run("fresh install", func(t *testing.T) {
		ic := w113InstallPrep(t)
		t.Cleanup(func() { w113KillArgv(ic.home) })
		out, code := w113RunInstall(t, ic)
		if code != 0 {
			t.Fatalf("install.sh (fresh): exit %d (output %q)", code, out)
		}
		const want = "`pf daemon ensure` starts the daemon only once at least one Cell has a real mapping — this installer runs it only then."
		if !strings.Contains(out, want) {
			t.Fatalf("the fresh-install gate hint does not print verbatim (a command substitution ate it):\n%s", out)
		}
		if strings.Contains(out, "not starting the daemon:") {
			t.Fatalf("printing the hint ran a stray pf daemon ensure:\n%s", out)
		}
	})
	t.Run("no Cell maps", func(t *testing.T) {
		ic := w113InstallPrep(t)
		t.Cleanup(func() { w113KillArgv(ic.home) })
		cfgPath := filepath.Join(ic.home, ".config", "polyforge", "cli.toml")
		w113ConfigFile(t, cfgPath, "http://127.0.0.1:8080", "w113key", "", nil)
		out, code := w113RunInstall(t, ic)
		if code != 0 {
			t.Fatalf("install.sh (no mapping in any Cell): exit %d (output %q)", code, out)
		}
		want := "SKIP: no [workspace] section in any Cell yet (" + cfgPath + ", or a cli-*.toml beside it) — `pf daemon ensure` starts the daemon only once at least one has one"
		if !strings.Contains(out, want) {
			t.Fatalf("the step-4 SKIP line does not print verbatim (a command substitution ate it):\n%s", out)
		}
		if strings.Contains(out, "not starting the daemon:") {
			t.Fatalf("printing the SKIP line ran a stray pf daemon ensure:\n%s", out)
		}
		// nothing starts a daemon on a machine no Cell of maps
		if procs := w113ProcArgv(ic.home); len(procs) > 0 {
			t.Fatalf("a daemon survives a no-mapping install: %v", procs)
		}
	})
}
