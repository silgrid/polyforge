package test

// polyforge#212: tools/install.sh has no built-in aihub host. The host
// (argument 2, else PF_HOST) is required only by a first install — the run
// that writes cli.toml — and its absence is refused before anything on the
// machine changes; a machine whose cli.toml already carries an api_key
// upgrades with `install.sh <api-key>` alone. Driven on the hermetic w174
// installer world (scratch HOME/PF_BIN, private tmux, stubbed gh; the w174
// env strips every PF_* variable, so no PF_HOST leaks in from the caller).

import (
	"os"
	"strings"
	"testing"
)

// TestW212InstallFirstInstallNeedsHost: without a cli.toml carrying an
// api_key and without a host, the installer refuses with a usage message
// naming <server-host> and PF_HOST, exits non-zero and changes nothing (no
// binary, no release checkout, no config change, no daemon). Given PF_HOST, the same machine gets a
// config pointing at that host. Before #212 the first run silently wrote a
// config pointing at the built-in private address.
func TestW212InstallFirstInstallNeedsHost(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w212")
	// a machine without a config carrying an api_key (here: a cli.toml
	// with none in it — the refusal must leave it as it was)
	cfgBefore := "# no key yet\n"
	if err := os.WriteFile(in.cfg, []byte(cfgBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	in.resetJournal(t)
	out, code := in.runArgs(t, "w212key")
	if code == 0 {
		t.Fatalf("a first install without a host MUST refuse; observed exit 0:\n%s", out)
	}
	if !strings.Contains(out, "<server-host>") || !strings.Contains(out, "PF_HOST") {
		t.Fatalf("the refusal MUST name <server-host> and PF_HOST; observed:\n%s", out)
	}
	w218AssertMachineUntouched(t, in, "v0.0.1-w212", cfgBefore)

	in.extraEnv = append(in.extraEnv, "PF_HOST=127.0.0.1")
	out, _ = in.runArgs(t, "w212key")
	if cfg := w174ReadFile(t, in.cfg); !strings.Contains(cfg, `server = "http://127.0.0.1:8080"`) {
		t.Fatalf("a first install with PF_HOST MUST write a config pointing at that host; config:\n%s\noutput:\n%s", cfg, out)
	}
}

// TestW212InstallUpgradeNeedsNoHost: a machine whose cli.toml carries an
// api_key upgrades with `install.sh <api-key>` alone — exit 0, binary
// installed, config byte-unchanged.
func TestW212InstallUpgradeNeedsNoHost(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w212")
	cfgBefore := w174ReadFile(t, in.cfg)
	out, code := in.runArgs(t, "w212key")
	if code != 0 {
		t.Fatalf("an upgrade with an existing config MUST need no host; observed exit %d:\n%s", code, out)
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.1-w212") {
		t.Fatal("the binary was not installed")
	}
	if got := w174ReadFile(t, in.cfg); got != cfgBefore {
		t.Fatalf("the existing config MUST stay unchanged (was %q, now %q)", cfgBefore, got)
	}
}
