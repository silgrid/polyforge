package components

// install_test.go — the install transaction's observable behaviour,
// authored test-first against AC1's own words: an empty volume
// installs a complete version; the same version and digest writes
// nothing; a newer seed upgrades and keeps previous; an older seed
// never downgrades; a bad digest, a wrong platform, an unsatisfied
// base, an illegal version and a cross-channel seed are all refused
// with current untouched; the #161-era layout is refused with a
// migration note. Expected values come from the ticket and the settled
// layout, never from the implementation's own output.

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// dataDir answers one fresh data directory for an install's options
// (the pre-#162 layout would live there; a fresh one carries none).
func dataDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// installSeed writes one synthetic seed (in its own temp directory) and
// answers its path.
func installSeed(t *testing.T, spec seedSpec) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "seed")
	writeVersionDir(t, dir, spec)
	return dir
}

// rootState answers a fingerprint of the root's pointers: where
// current and previous point, and which version directories and
// staging entries exist. Two fingerprints equal means the install
// wrote nothing that matters.
func rootState(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{CurrentLinkname, PreviousLinkname} {
		target, err := os.Readlink(filepath.Join(root, name))
		if err != nil {
			if os.IsNotExist(err) {
				fmtb(&b, name, "(absent)")
			}
			continue
		}
		fmtb(&b, name, target)
	}
	for _, dir := range []string{ReleasesDirname, StagingDirname} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		fmtb(&b, dir, strings.Join(names, ","))
	}
	return b.String()
}

// fmtb appends one "key: value\n" line.
func fmtb(b *strings.Builder, k, v string) {
	b.WriteString(k)
	b.WriteString(": ")
	b.WriteString(v)
	b.WriteString("\n")
}

// TestInstallEmptyVolume pins the first install: an empty root gains a
// complete version — the manifest verifies against the published tree,
// the pf binary is executable — current points at it, and nothing else
// moved.
func TestInstallEmptyVolume(t *testing.T) {
	root := t.TempDir()
	seed := installSeed(t, seedSpec{})

	res, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: seed, BaseVersion: "v0.2.4", Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("Install on an empty volume: %v", err)
	}
	if res.Action != ActionInstalled {
		t.Errorf("Action = %q, want %q", res.Action, ActionInstalled)
	}
	if res.Version != "v0.2.4" {
		t.Errorf("Version = %q, want v0.2.4", res.Version)
	}
	if res.From != "" {
		t.Errorf("From = %q, want \"\" (there was no current before)", res.From)
	}
	target, err := CurrentTarget(root)
	if err != nil {
		t.Fatalf("CurrentTarget: %v", err)
	}
	if target != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Errorf("current -> %q, want releases/v0.2.4", target)
	}
	dir := filepath.Join(root, ReleasesDirname, "v0.2.4")
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("the installed version's manifest: %v", err)
	}
	if err := m.VerifyTree(dir); err != nil {
		t.Errorf("the installed tree does not match its manifest: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, PathPF))
	if err != nil {
		t.Fatalf("the installed pf: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed pf is not executable (mode %s)", fi.Mode().Perm())
	}
	if entries, err := os.ReadDir(filepath.Join(root, StagingDirname)); err == nil && len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("staging keeps entries after the install: %s", strings.Join(names, ","))
	}
}

// TestInstallSameVersionNoop pins "同版不写": the same seed again is a
// noop — not one pointer, directory or file moves.
func TestInstallSameVersionNoop(t *testing.T) {
	root := t.TempDir()
	seed := installSeed(t, seedSpec{})
	if _, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: seed, BaseVersion: "v0.2.4", Platform: "linux/amd64"}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	before := rootState(t, root)
	curBefore, err := os.Lstat(filepath.Join(root, CurrentLinkname))
	if err != nil {
		t.Fatal(err)
	}

	res, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: seed, BaseVersion: "v0.2.4", Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("second install of the same seed: %v", err)
	}
	if res.Action != ActionNoop {
		t.Errorf("Action = %q, want %q", res.Action, ActionNoop)
	}
	if after := rootState(t, root); after != before {
		t.Errorf("the noop install wrote something:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	curAfter, err := os.Lstat(filepath.Join(root, CurrentLinkname))
	if err != nil {
		t.Fatal(err)
	}
	if !curAfter.ModTime().Equal(curBefore.ModTime()) {
		t.Error("the noop install replaced the current symlink")
	}
}

// TestInstallSameVersionReorderedNoop pins that the same-version
// comparison is order-independent (review r1#N6): Validate does not
// order the manifest's components, so a seed whose manifest lists them
// in another order — the same names, the same digests — is the same
// content, a noop, not a "different digest" refusal.
func TestInstallSameVersionReorderedNoop(t *testing.T) {
	root := t.TempDir()
	seed := installSeed(t, seedSpec{})
	if _, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: seed, BaseVersion: "v0.2.4", Platform: "linux/amd64"}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	before := rootState(t, root)

	// the same seed, its manifest's components listed backwards: the
	// bytes the digest covers are unchanged, only the manifest's list
	// order differs.
	reordered := filepath.Join(t.TempDir(), "seed")
	assembleTree(t, reordered, seedSpec{})
	m := manifestOf(seedSpec{})
	if err := m.FillDigests(reordered); err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(m.Components)-1; i < j; i, j = i+1, j-1 {
		m.Components[i], m.Components[j] = m.Components[j], m.Components[i]
	}
	if err := WriteManifest(reordered, m); err != nil {
		t.Fatal(err)
	}

	res, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: reordered, BaseVersion: "v0.2.4", Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("the reordered same-version seed: %v (want the noop: the content is the same)", err)
	}
	if res.Action != ActionNoop {
		t.Errorf("Action = %q, want %q", res.Action, ActionNoop)
	}
	if after := rootState(t, root); after != before {
		t.Errorf("the noop install wrote something:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestInstallUpgradeKeepsPrevious pins the upgrade: a newer seed
// switches current, previous names the version it replaced, and the
// old version's directory stays complete (never deleted, never
// modified in place).
func TestInstallUpgradeKeepsPrevious(t *testing.T) {
	root := t.TempDir()
	old := installSeed(t, seedSpec{})
	if _, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: old, BaseVersion: "v0.2.4", Platform: "linux/amd64"}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	newer := installSeed(t, seedSpec{version: "v0.2.5", minBase: "v0.2.4"})

	res, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: newer, BaseVersion: "v0.2.5", Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("upgrade install: %v", err)
	}
	if res.Action != ActionUpgraded {
		t.Errorf("Action = %q, want %q", res.Action, ActionUpgraded)
	}
	if res.From != "v0.2.4" {
		t.Errorf("From = %q, want v0.2.4", res.From)
	}
	if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.5") {
		t.Errorf("current -> %q, want releases/v0.2.5", target)
	}
	prev, err := os.Readlink(filepath.Join(root, PreviousLinkname))
	if err != nil {
		t.Fatalf("previous after an upgrade: %v", err)
	}
	if prev != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Errorf("previous -> %q, want releases/v0.2.4", prev)
	}
	for _, ver := range []string{"v0.2.4", "v0.2.5"} {
		dir := filepath.Join(root, ReleasesDirname, ver)
		m, err := ReadManifest(dir)
		if err != nil {
			t.Fatalf("version %s after the upgrade: %v", ver, err)
		}
		if err := m.VerifyTree(dir); err != nil {
			t.Errorf("version %s is not complete after the upgrade: %v", ver, err)
		}
	}
}

// TestInstallRefusals pins every refusal of AC1's list — each leaves
// the root exactly as it was, and each answers the sentinel a caller
// can match on.
func TestInstallRefusals(t *testing.T) {
	// a root with v0.2.4 current, the state every refusal must keep.
	setup := func(t *testing.T) (root string, before string) {
		root = t.TempDir()
		if _, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: installSeed(t, seedSpec{}), BaseVersion: "v0.2.4", Platform: "linux/amd64"}); err != nil {
			t.Fatalf("setup install: %v", err)
		}
		return root, rootState(t, root)
	}
	seedAt := installSeed

	cases := []struct {
		name  string
		setup func(t *testing.T) (root, before string, opts func(Options) Options)
		want  error
	}{
		{
			name: "an older seed is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.3"})
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrDowngrade,
		},
		{
			name: "the same version with a different digest is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{piVersion: "1.4.1"}) // same version, other content
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrSameVersionDifferentDigest,
		},
		{
			name: "a bad digest is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.5", minBase: "v0.2.4"})
				// tamper AFTER the manifest was written: the seed's
				// content no longer matches its manifest.
				wfile(t, filepath.Join(seed, PathPiWeb, "app/page.tsx"), "export default function Page() { /* tampered */ }\n", 0o644)
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrDigestMismatch,
		},
		{
			name: "a seed of another platform is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.5", platform: "linux/arm64", minBase: "v0.2.4"})
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrPlatformMismatch,
		},
		{
			name: "a base older than the seed's minimum is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.5", minBase: "v0.2.5"})
				return root, before, func(o Options) Options {
					o.Seed = seed
					o.BaseVersion = "v0.2.4"
					return o
				}
			},
			want: ErrBaseTooOld,
		},
		{
			name: "an illegal version in the seed is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.5", minBase: "v0.2.4"})
				rewriteManifestVersion(t, seed, "0.2.5") // of no accepted form
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: nil, // any error: the manifest itself is invalid
		},
		{
			name: "a cross-channel seed (another kind) is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.5-gmi.1", minBase: "v0.2.4"})
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrChannelMismatch,
		},
		{
			name: "a cross-channel seed (the same version, another kind) is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.4-gmi.1"})
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrChannelMismatch,
		},
		{
			name: "a cross-channel seed (another repository) is refused",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, before := setup(t)
				seed := seedAt(t, seedSpec{version: "v0.2.5", channelRepo: "gmi/polyforge", minBase: "v0.2.4"})
				return root, before, func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrChannelMismatch,
		},
		{
			name: "a corrupt current is refused with a recovery hint",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, _ := setup(t)
				if err := os.Remove(filepath.Join(root, ReleasesDirname, "v0.2.4", ManifestFilename)); err != nil {
					t.Fatal(err)
				}
				seed := seedAt(t, seedSpec{version: "v0.2.5", minBase: "v0.2.4"})
				return root, rootState(t, root), func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrCurrentCorrupt,
		},
		{
			name: "a dangling current is refused with a recovery hint",
			setup: func(t *testing.T) (string, string, func(Options) Options) {
				root, _ := setup(t)
				if err := os.RemoveAll(filepath.Join(root, ReleasesDirname, "v0.2.4")); err != nil {
					t.Fatal(err)
				}
				seed := seedAt(t, seedSpec{version: "v0.2.5", minBase: "v0.2.4"})
				return root, rootState(t, root), func(o Options) Options {
					o.Seed = seed
					return o
				}
			},
			want: ErrCurrentCorrupt,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, before, setSeed := c.setup(t)
			opts := setSeed(Options{Root: root, DataDir: dataDir(t), BaseVersion: "v0.2.5", Platform: "linux/amd64"})
			res, err := Install(opts)
			if err == nil {
				t.Fatalf("Install answered %+v, want a refusal", res)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("Install error = %q, want the %v sentinel", err, c.want)
			}
			if after := rootState(t, root); after != before {
				t.Errorf("a refusal changed the root:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			// a corrupt current's refusal carries a recovery hint, not
			// just the sentinel's words.
			if errors.Is(err, ErrCurrentCorrupt) {
				msg := err.Error()
				if !strings.Contains(msg, CurrentLinkname) || !strings.Contains(msg, ManifestFilename) {
					t.Errorf("the corrupt-current refusal must tell how to recover (current and its manifest named):\n%s", msg)
				}
			}
		})
	}
}

// rewriteManifestVersion rewrites the seed's manifest with an illegal
// version string, the way a broken build would ship it.
func rewriteManifestVersion(t *testing.T, dir, version string) {
	t.Helper()
	path := filepath.Join(dir, ManifestFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(raw), `"v0.2.5"`, `"`+version+`"`, 2)
	if body == string(raw) {
		t.Fatal("the rewrite found no version to replace")
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInstallLegacyLayoutRefused pins AC2's legacy clause where the
// old layout actually lives: the #161 image and the host installer put
// their components in the DATA directory — <data-dir>/release/<ver>
// with a current symlink inside — and the components root is a new
// volume that era did not have. An install on a machine whose data
// volume still carries that shape is refused with a migration note,
// never imported, never bridged — and both the old layout and the
// components volume are left untouched.
func TestInstallLegacyLayoutRefused(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	legacy := filepath.Join(data, LegacyReleaseDirname)
	if err := os.MkdirAll(filepath.Join(legacy, "v0.2.3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("v0.2.3", filepath.Join(legacy, CurrentLinkname)); err != nil {
		t.Fatal(err)
	}

	_, err := Install(Options{Root: root, DataDir: data, Seed: installSeed(t, seedSpec{}), BaseVersion: "v0.2.4", Platform: "linux/amd64"})
	if !errors.Is(err, ErrLegacyLayout) {
		t.Fatalf("Install with a #161-era data volume = %v, want the ErrLegacyLayout refusal", err)
	}
	msg := err.Error()
	for _, want := range []string{
		filepath.Join(LegacyReleaseDirname, CurrentLinkname), // names the old shape where it lives
		ReleasesDirname, // names the new layout
		"migrat",        // tells how to migrate
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the legacy refusal's migration note lacks %q:\n%s", want, msg)
		}
	}
	// the old layout is left untouched, and the components volume got
	// nothing.
	if target, err := os.Readlink(filepath.Join(legacy, CurrentLinkname)); err != nil || target != "v0.2.3" {
		t.Errorf("the refusal touched the legacy layout: %v (current -> %q)", err, target)
	}
	if state := rootState(t, root); strings.Contains(state, "v0.2.4") {
		t.Errorf("the refused install wrote into the components volume:\n%s", state)
	}
}

// TestInstallLockHeld pins the mutex: a second install while the lock
// is held answers ErrLockHeld and writes nothing; once released, the
// same install proceeds.
func TestInstallLockHeld(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, LockFilename), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("taking the lock in the test: %v", err)
	}

	_, err = Install(Options{Root: root, DataDir: dataDir(t), Seed: installSeed(t, seedSpec{}), BaseVersion: "v0.2.4", Platform: "linux/amd64"})
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Install under a held lock = %v, want the ErrLockHeld refusal", err)
	}
	if state := rootState(t, root); strings.Contains(state, "v0.2.4") {
		t.Errorf("the refused install wrote a version directory:\n%s", state)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Options{Root: root, DataDir: dataDir(t), Seed: installSeed(t, seedSpec{}), BaseVersion: "v0.2.4", Platform: "linux/amd64"}); err != nil {
		t.Fatalf("Install after the lock's release: %v", err)
	}
	if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Errorf("current -> %q, want releases/v0.2.4", target)
	}
}
