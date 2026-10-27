package test

// polyforge#235 repro: the darwin config path split. tools/install.sh
// writes a fresh machine's cli.toml at $HOME/.config/polyforge/cli.toml
// (its CFG — one path, no per-OS branch), but a bare darwin pf resolves
// its default config at $HOME/Library/Application Support/polyforge/cli.toml:
// cli.DefaultConfigPath delegates to os.UserConfigDir, whose darwin branch
// answers $HOME + "/Library/Application Support" (go1.26.3
// src/os/file.go — XDG_CONFIG_HOME is not consulted on darwin). The two
// paths disagree on a Mac, so a fresh install's config is unreadable by
// bare pf: LoadConfig answers the defaults — server
// http://localhost:8420, no api key — never the installed server/api_key.
//
// Expected behavior: one darwin authority path — a bare darwin pf
// resolves the cli.toml install.sh writes ($XDG_CONFIG_HOME|~/.config,
// the single documented location, on every OS).
//
// Driven hermetically on the w174 installer world with a Darwin uname
// (the w218 darwin setup): a fresh install (no cli.toml yet — the run
// that writes it) runs to completion on a Mac (uname answers
// Darwin/arm64, the release carries the darwin asset), then every
// cli.toml the installer wrote under the scratch HOME is compared with
// the darwin default config path.
//
// The darwin default is pinned on two faces (a darwin binary cannot run
// on this machine):
//  1. the repo's own resolver, cli.DefaultConfigPath, with XDG_CONFIG_HOME
//     unset (the macOS norm) and HOME the scratch Mac's, MUST equal the
//     one cli.toml install.sh wrote;
//  2. the darwin cross-build of cmd/pf MUST NOT carry os.UserConfigDir's
//     darwin branch ("Library/Application Support"), so the darwin binary
//     takes the same OS-independent resolution face 1 checks on Linux.
//     Pre-fix the darwin build carries that branch: this face is the red one.
//
// Rework (owner-approved, question w235-repro-test-rework): the first
// version emulated darwin by setting XDG_CONFIG_HOME to the Library dir.
// That emulation only matched darwin while the resolver delegated to
// os.UserConfigDir; a correct resolver honors XDG on every OS, so only a
// dual-path installer or a resolver breaking the XDG contract could pass
// it. The assertion is now the real contract, not the simulation.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestW235InstallShCoversDarwinDefaultConfigPath: a fresh install.sh run
// on a Mac MUST write its config where a bare darwin pf reads it — the
// darwin default config path is the cli.toml the installer wrote, and
// the darwin build resolves that default like every other OS.
func TestW235InstallShCoversDarwinDefaultConfigPath(t *testing.T) {
	in := w174NewInstall(t)
	// a fresh machine: no cli.toml yet — this run is the one that writes it
	if err := os.Remove(in.cfg); err != nil {
		t.Fatal(err)
	}
	// a Mac: uname answers Darwin/arm64, the release carries the darwin asset
	w174UnameShim(t, in.shim, "Darwin", "arm64")
	in.w174DarwinRelease(t, "v0.0.1-w235", "pf-darwin-arm64")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install.sh on a fresh Mac (Darwin/arm64): exit %d\n%s", code, out)
	}
	// every cli.toml the installer wrote under the scratch HOME
	var written []string
	err := filepath.WalkDir(in.home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "cli.toml" {
			written = append(written, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("a fresh install.sh run must write exactly one cli.toml under $HOME (one authority path), wrote [%s]",
			strings.Join(written, ", "))
	}

	// face 1: the bare default, macOS env (XDG_CONFIG_HOME unset), is the installed file
	t.Setenv("HOME", in.home)
	t.Setenv("XDG_CONFIG_HOME", "")
	os.Unsetenv("XDG_CONFIG_HOME")
	if got := cli.DefaultConfigPath(); got != written[0] {
		t.Fatalf("REPORTED FAILURE (polyforge#235): install.sh wrote %s but bare pf resolves its default config at %s — "+
			"the installed config is unreadable by bare pf, which falls back to the default server http://localhost:8420",
			written[0], got)
	}

	// face 2: the darwin binary takes the same resolution — no os.UserConfigDir darwin branch compiled in
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "pf-darwin-arm64")
	c := exec.Command(goBin, "build", "-o", bin, "./cmd/pf")
	c.Dir = repo
	c.Env = append(os.Environ(), "GOWORK=off", "GOOS=darwin", "GOARCH=arm64")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=darwin GOARCH=arm64 go build ./cmd/pf: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("Library/Application Support")) {
		t.Fatalf("REPORTED FAILURE (polyforge#235): the darwin pf build carries os.UserConfigDir's darwin branch " +
			"($HOME/Library/Application Support): a bare darwin pf resolves its default config there, not at the " +
			"$HOME/.config/polyforge/cli.toml install.sh writes, and falls back to the default server http://localhost:8420")
	}
}
