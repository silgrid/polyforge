package components

// digest_test.go — TreeDigest's determinism contract: the same content,
// exec bits and symlink targets always digest equal (whatever the
// umask's non-exec bits, whatever the walk order), and any content,
// exec-bit, symlink-target or layout change breaks the digest.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTreeDigestStable pins equality across two independently written
// copies and across a umask-shaped mode difference that keeps the exec
// bit.
func TestTreeDigestStable(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "a")
	dir2 := filepath.Join(t.TempDir(), "b")
	for _, dir := range []string{dir1, dir2} {
		assembleTree(t, dir, seedSpec{})
	}
	d1, err := TreeDigest(dir1, nil)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	d2, err := TreeDigest(dir2, nil)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	if d1 != d2 {
		t.Fatalf("two copies of one tree digested differently: %s vs %s", d1, d2)
	}
	if !IsDigest(d1) {
		t.Fatalf("TreeDigest = %q, not a digest", d1)
	}

	// a mode difference inside the exec bit (0755 vs 0744) is the same
	// content: only the exec bit is a fact of the layout.
	if err := os.Chmod(filepath.Join(dir2, PathPF), 0o744); err != nil {
		t.Fatal(err)
	}
	d2, err = TreeDigest(dir2, nil)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	if d1 != d2 {
		t.Fatalf("a 0755→0744 change (exec bit kept) changed the digest: %s vs %s", d1, d2)
	}
}

// TestTreeDigestCatchesChanges pins that every change the manifest
// promises to cover changes the digest.
func TestTreeDigestCatchesChanges(t *testing.T) {
	pristine := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "t")
		assembleTree(t, dir, seedSpec{})
		return dir
	}
	base := pristine(t)
	want, err := TreeDigest(base, nil)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}

	cases := map[string]func(t *testing.T, dir string){
		"a changed byte": func(t *testing.T, dir string) {
			wfile(t, filepath.Join(dir, "skills/polyforge/SKILL.md"), "# the routing skill, changed\n", 0o644)
		},
		"a lost exec bit": func(t *testing.T, dir string) {
			if err := os.Chmod(filepath.Join(dir, PathPF), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a retargeted symlink": func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, PathPiWeb, "bin/next"))
			wlink(t, filepath.Join(dir, PathPiWeb, "bin/next"), "../app")
		},
		"a symlink becoming a file": func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, PathPiWeb, "bin/next"))
			wfile(t, filepath.Join(dir, PathPiWeb, "bin/next"), "../package.json\n", 0o644)
		},
		"a moved file": func(t *testing.T, dir string) {
			if err := os.Rename(filepath.Join(dir, "workflows/develop/README.md"), filepath.Join(dir, "workflows/develop/ROADMAP.md")); err != nil {
				t.Fatal(err)
			}
		},
		"a removed file": func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "workflows/develop/README.md")); err != nil {
				t.Fatal(err)
			}
		},
		"an added empty directory": func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "workflows/empty"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"an added file": func(t *testing.T, dir string) {
			wfile(t, filepath.Join(dir, "workflows/develop/extra.md"), "x\n", 0o644)
		},
	}
	for name, do := range cases {
		t.Run(name, func(t *testing.T) {
			dir := pristine(t)
			do(t, dir)
			got, err := TreeDigest(dir, nil)
			if err != nil {
				t.Fatalf("TreeDigest: %v", err)
			}
			if got == want {
				t.Fatalf("the digest did not change: %s", name)
			}
		})
	}
}

// TestTreeDigestSkips pins the skip set the release entry uses: the
// entries the other components own are not part of the release
// checkout's digest.
func TestTreeDigestSkips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "t")
	assembleTree(t, dir, seedSpec{})
	skip := []string{"bin", PathPiWeb, "harness", ManifestFilename}
	want, err := TreeDigest(dir, skip)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	// a change inside a skipped entry must not move the release digest.
	wfile(t, filepath.Join(dir, PathPiWeb, "app/page.tsx"), "export default function Page() { /* x */ }\n", 0o644)
	wfile(t, filepath.Join(dir, PathPF), "pf-binary-changed\n", 0o755)
	got, err := TreeDigest(dir, skip)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	if got != want {
		t.Fatalf("a change inside a skipped entry moved the digest: %s vs %s", got, want)
	}
	// a change outside them must.
	wfile(t, filepath.Join(dir, "skills/polyforge/SKILL.md"), "# the routing skill, changed\n", 0o644)
	got, err = TreeDigest(dir, skip)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	if got == want {
		t.Fatal("a change inside the release checkout did not move the release digest")
	}
}
