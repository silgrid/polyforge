package test

// polyforge#237: on darwin, tools/install.sh clears the quarantine
// attribute (`xattr -c`), ad-hoc signs (`codesign -f -s -`) and proves
// (`--version` exits 0) the STAGED pf before the swap. macOS can kill an
// unsigned or quarantined downloaded binary on first run (`Killed: 9`);
// without these steps such a pf was swapped in and the later steps failed
// after the old pf was gone. A missing or failing xattr/codesign, or a
// failed run-proof, refuses in the prepare phase: nothing changed. Linux
// never runs any of it.
//
// Driven hermetically on the w174 installer world with the w218 Darwin
// uname shim; w174UnameShim gives a Darwin machine journaling xattr and
// codesign stubs (w237MacTools), so one journal records the order.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestW237InstallDarwinClearsSignsProvesStagedBeforeSwap: on a Mac the
// installer runs xattr -c, then codesign -f -s -, then the staged pf's
// --version, all on the staged file ($BIN.new.<pid>), before the rename
// that swaps it in.
func TestW237InstallDarwinClearsSignsProvesStagedBeforeSwap(t *testing.T) {
	in := w174NewInstall(t)
	w174UnameShim(t, in.shim, "Darwin", "arm64")
	in.w174DarwinRelease(t, "v0.0.1-w237", "pf-darwin-arm64")
	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("install.sh on a Mac: exit %d\n%s", code, out)
	}
	lines := in.journalLines()
	staged := in.bin + ".new."
	ix := journalIndex(lines, "xattr -c "+staged)
	cs := journalIndex(lines, "codesign -f -s - "+staged)
	// a fresh machine has no $BIN: the first `pf --version` is the run-proof
	rp := journalIndex(lines, "pf --version")
	sw := -1
	for i, l := range lines {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "mv" && strings.HasPrefix(f[len(f)-2], staged) && f[len(f)-1] == in.bin {
			sw = i
			break
		}
	}
	if ix < 0 || cs < 0 || rp < 0 || sw < 0 || !(ix < cs && cs < rp && rp < sw) {
		t.Fatalf("want xattr -c, codesign -f -s -, --version on the staged pf (%s*), in that order, before the swap; got xattr=%d codesign=%d run-proof=%d swap=%d:\n%s",
			staged, ix, cs, rp, sw, strings.Join(lines, "\n"))
	}
}

// TestW237InstallDarwinRefusesWithNothingChanged: on a Mac upgrade, a
// failing xattr, a failing codesign, a staged pf that gets killed when
// run, and a missing xattr or codesign each refuse in the prepare phase,
// naming the failing step: the old pf, release/current and the running
// daemon stay as they were, the staged file is removed. Fixed, the same
// command upgrades.
func TestW237InstallDarwinRefusesWithNothingChanged(t *testing.T) {
	in := w174NewInstall(t)
	w174UnameShim(t, in.shim, "Darwin", "arm64")
	v1Bin := in.w174DarwinRelease(t, "v0.0.1-w174", "pf-darwin-arm64")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1 on a Mac: exit %d\n%s", code, out)
	}
	pid := in.oneDaemon(t, 10*time.Second)
	in.w174DarwinRelease(t, "v0.0.2-w237", "pf-darwin-arm64")
	macPath := in.path

	for _, c := range []struct {
		name, want string
		env        []string
		missing    string
	}{
		{name: "xattr fails", want: "xattr -c", env: []string{"W237_XATTR_FAIL=1"}},
		{name: "codesign fails", want: "codesign -f -s -", env: []string{"W237_CODESIGN_FAIL=1"}},
		{name: "staged pf killed", want: "run-proof failed", env: []string{"W237_CODESIGN_KILL=1"}},
		{name: "xattr missing", want: "xattr not on PATH", missing: "xattr"},
		{name: "codesign missing", want: "codesign not on PATH", missing: "codesign"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in.path = macPath
			if c.missing != "" {
				in.path = w174FilteredPath(t, in.shim, c.missing)
			}
			in.extraEnv = c.env
			in.resetJournal(t)
			out, code := in.run(t)
			if code == 0 {
				t.Fatalf("the upgrade must refuse (%s), observed exit 0:\n%s", c.name, out)
			}
			for _, want := range []string{c.want, "nothing on this machine was changed"} {
				if !strings.Contains(out, want) {
					t.Fatalf("the refusal lacks %q:\n%s", want, out)
				}
			}
			w174AssertStillV1(t, in, v1Bin)
			if again := in.oneDaemon(t, 5*time.Second); again != pid {
				t.Fatalf("the old daemon was replaced: %d -> %d", pid, again)
			}
		})
	}

	// the cause fixed, the same command upgrades
	in.path, in.extraEnv = macPath, nil
	if out, code := in.run(t); code != 0 {
		t.Fatalf("upgrade after the fix: exit %d\n%s", code, out)
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.2-w237") {
		t.Fatal("the binary was not swapped")
	}
}

// TestW237InstallLinuxNeverSigns: on Linux, with xattr and codesign on
// PATH, the installer invokes neither and runs no run-proof (no
// `pf --version` before the swap on a fresh machine); its output carries
// nothing of the darwin steps.
func TestW237InstallLinuxNeverSigns(t *testing.T) {
	in := w174NewInstall(t)
	w174UnameShim(t, in.shim, "Linux", "x86_64")
	w237MacTools(t, in.shim)
	in.release(t, "v0.0.1-w237")
	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("install.sh on Linux: exit %d\n%s", code, out)
	}
	lines := in.journalLines()
	for _, l := range lines {
		if strings.HasPrefix(l, "xattr ") || strings.HasPrefix(l, "codesign ") {
			t.Fatalf("Linux ran a macOS tool: %q", l)
		}
	}
	sw := -1
	for i, l := range lines {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "mv" && f[len(f)-1] == in.bin {
			sw = i
			break
		}
	}
	if sw < 0 {
		t.Fatalf("no binary swap in the journal:\n%s", strings.Join(lines, "\n"))
	}
	if rp := journalIndex(lines, "pf --version"); rp >= 0 && rp < sw {
		t.Fatalf("Linux ran a run-proof before the swap (line %d):\n%s", rp, strings.Join(lines, "\n"))
	}
	for _, bad := range []string{"quarantine", "codesign", "xattr", "run-proof"} {
		if strings.Contains(out, bad) {
			t.Fatalf("Linux output mentions %q:\n%s", bad, out)
		}
	}
	if _, err := os.Stat(filepath.Join(in.data, "release", "current")); err != nil {
		t.Fatalf("the Linux install did not complete: %v", err)
	}
}
