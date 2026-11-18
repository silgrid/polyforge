package capsulegc

// capsulegc_test.go: the declaration parser's pins. The declaration is
// the only thing that widens what the rebuildable-artifacts cleanup may
// remove, so the parser is fail-closed everywhere: one unknown key or
// one unsafe entry rejects the whole declaration and nothing of it is
// honored (polyforge#127, u1 — AC3: a repository that declares nothing
// keeps today's behavior; AC5: the safety rules live here, project
// names never do). Expected values are handwritten from the ticket's
// Q2 ruling — literal directories only, no glob, no shell, no
// variables, unknown keys and illegal entries rejected whole — never
// taken from the implementation's own output.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// parseCases is the parser's table: what one declaration line is and
// what may never be one. Each case is the ruling's own words turned
// into bytes; the parser's job is to reject everything the ruling does
// not spell out, never to normalize a near-miss into a directory.
var parseCases = []struct {
	name    string
	raw     string
	want    []string
	wantErr string // "" = the declaration must parse
}{
	{
		name: "one line per directory, comments and blanks ignored",
		raw:  "# the only rebuildable products of this repository\n\nrebuild_dir=build/dist\nrebuild_dir=vendor/deps\n",
		want: []string{"build/dist", "vendor/deps"},
	},
	{
		name: "a hidden directory is a literal like any other",
		raw:  "rebuild_dir=.cache\n",
		want: []string{".cache"},
	},
	{
		name: "a declaration with no entry enables nothing and is no error",
		raw:  "# nothing declared\n\n",
	},
	{
		name:    "an unknown key rejects the whole declaration",
		raw:     "rebuild_dir=build\nkeep_dir=secrets\n",
		wantErr: "keep_dir",
	},
	{
		name:    "a line with no key-value shape is unknown, not a directory",
		raw:     "rebuild_dir\n",
		wantErr: "rebuild_dir",
	},
	{
		name:    "an empty value is an illegal entry",
		raw:     "rebuild_dir=\n",
		wantErr: "rebuild_dir=",
	},
	{
		name:    "an absolute path never names a repo-root-relative directory",
		raw:     "rebuild_dir=/etc\n",
		wantErr: "/etc",
	},
	{
		name:    "a parent-relative path escapes the repository root",
		raw:     "rebuild_dir=../outside\n",
		wantErr: "../outside",
	},
	{
		name:    "the repository root itself is not a declared directory",
		raw:     "rebuild_dir=.\n",
		wantErr: ".",
	},
	{
		name:    "a trailing slash is not the literal the declaration names",
		raw:     "rebuild_dir=build/\n",
		wantErr: "build/",
	},
	{
		name:    "a dot-segmented path is not the literal the declaration names",
		raw:     "rebuild_dir=./build\n",
		wantErr: "./build",
	},
	{
		name:    "whitespace inside a value is not a directory literal",
		raw:     "rebuild_dir=build dist\n",
		wantErr: "build dist",
	},
	{
		name:    "the same directory twice is one entry too many",
		raw:     "rebuild_dir=build\nrebuild_dir=build\n",
		wantErr: "build",
	},
	{
		name:    "one declared directory inside another is an overlap",
		raw:     "rebuild_dir=build\nrebuild_dir=build/dist\n",
		wantErr: "build/dist",
	},
}

// TestParseGCDeclaration: the declaration parser is fail-closed — the
// literal directories of a well-formed declaration come out in file
// order, and anything the Q2 ruling does not spell out (unknown keys,
// empty values, absolute or parent-relative paths, the root itself,
// non-literal spellings, duplicates, overlaps) rejects the whole
// declaration, naming the offending line, never a silent partial.
func TestParseGCDeclaration(t *testing.T) {
	for _, tc := range parseCases {
		t.Run(tc.name, func(t *testing.T) {
			dirs, err := ParseDeclaration([]byte(tc.raw))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseDeclaration(%q) = %v, nil; want the whole declaration rejected", tc.raw, dirs)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseDeclaration(%q) error %q does not name the offending line %q", tc.raw, err, tc.wantErr)
				}
				if dirs != nil {
					t.Fatalf("a rejected declaration must parse to no directory, got %v", dirs)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDeclaration(%q) = %v, %v; want %v, nil", tc.raw, dirs, err, tc.want)
			}
			if len(dirs) != len(tc.want) {
				t.Fatalf("ParseDeclaration(%q) = %v, want %v", tc.raw, dirs, tc.want)
			}
			for i := range tc.want {
				if dirs[i] != tc.want[i] {
					t.Fatalf("ParseDeclaration(%q)[%d] = %q, want %q (file order)", tc.raw, i, dirs[i], tc.want[i])
				}
			}
		})
	}
}

// gcGit runs one git command in dir and fails the test on its error.
// The fixture git is the same hardened shape the doctor's own probes
// use elsewhere: system config ignored, an isolated HOME, no prompt.
func gcGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "HOME="+t.TempDir())
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, errb.String())
	}
	return out.String()
}

// newGCClone answers a local clone of a fresh repository whose default
// branch (main, the fixture's own choice) carries one commit; conf is
// the capsule-gc declaration that commit carries ("" = none, the
// repository declares nothing). A clone — not a plain repository — so
// the default branch is the metadata a clone records, never a guess.
func newGCClone(t *testing.T, conf string) string {
	t.Helper()
	src := t.TempDir()
	gcGit(t, src, "-c", "init.defaultBranch=main", "init", "-q")
	if conf != "" {
		os.MkdirAll(filepath.Join(src, ".polyforge"), 0o755)
		if err := os.WriteFile(filepath.Join(src, ConfPath), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		gcGit(t, src, "add", ConfPath)
	}
	gcGit(t, src, "-c", "user.email=gc@example.com", "-c", "user.name=gc", "commit", "-q", "-m", "the declaration", "--allow-empty")
	parent := t.TempDir()
	gcGit(t, parent, "clone", "--quiet", src, filepath.Join(parent, "clone"))
	return filepath.Join(parent, "clone")
}

// TestReadGCDeclarationFromCommittedRef: only committed bytes
// authorize a removal. The declaration a repository's default branch
// carries comes out with the ref it was read at and the commit that
// ref resolved to (the retroactive cleanup's audit trail); a working
// tree edited, or even emptied, after the fact changes nothing; no
// declaration at the ref is no error and enables nothing; a committed
// declaration that does not parse is an error — the whole thing, never
// a silent partial; an explicit ref reads that ref; the default branch
// is refs/remotes/origin/HEAD, the metadata a clone records, and a
// repository without it answers an error, never a guessed branch
// name. Expected values come from the fixture's own git (rev-parse),
// never from the implementation (polyforge#127, u2).
func TestReadGCDeclarationFromCommittedRef(t *testing.T) {
	t.Run("the committed declaration, its ref and its commit", func(t *testing.T) {
		clone := newGCClone(t, "# the rebuildable products\nrebuild_dir=build/dist\nrebuild_dir=vendor/deps\n")
		sha := strings.TrimSpace(gcGit(t, clone, "rev-parse", "origin/main"))
		if sha == "" {
			t.Fatal("the fixture's own git answers no commit for origin/main")
		}
		d, err := ReadCommitted(clone, "")
		if err != nil {
			t.Fatalf("ReadCommitted(clone, \"\") = %v, %v; want the committed declaration", d, err)
		}
		if d == nil {
			t.Fatal("ReadCommitted(clone, \"\") = nil, nil; want the committed declaration")
		}
		want := []string{"build/dist", "vendor/deps"}
		if len(d.Dirs) != len(want) || d.Dirs[0] != want[0] || d.Dirs[1] != want[1] {
			t.Fatalf("Dirs = %v, want %v in file order", d.Dirs, want)
		}
		if d.Ref != "origin/main" {
			t.Fatalf("Ref = %q, want origin/main (the clone's recorded default branch)", d.Ref)
		}
		if d.SHA != sha {
			t.Fatalf("SHA = %q, want the commit the fixture's git names: %q", d.SHA, sha)
		}
	})

	t.Run("a working tree never authorizes", func(t *testing.T) {
		clone := newGCClone(t, "rebuild_dir=build/dist\n")
		committed := strings.TrimSpace(gcGit(t, clone, "rev-parse", "origin/main"))
		// An uncommitted edit that would widen what may be removed, and
		// then the file gone from the working tree altogether: neither
		// may move what the committed ref answers.
		if err := os.WriteFile(filepath.Join(clone, ConfPath), []byte("rebuild_dir=hacked\nrebuild_dir=secrets\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := ReadCommitted(clone, "")
		if err != nil {
			t.Fatalf("ReadCommitted after an uncommitted edit: %v", err)
		}
		if d == nil || len(d.Dirs) != 1 || d.Dirs[0] != "build/dist" {
			t.Fatalf("Dirs = %v, want the committed [build/dist] — a working tree never authorizes", d)
		}
		if err := os.Remove(filepath.Join(clone, ConfPath)); err != nil {
			t.Fatal(err)
		}
		if d, err = ReadCommitted(clone, ""); err != nil || d == nil || d.Dirs[0] != "build/dist" || d.SHA != committed {
			t.Fatalf("after the file left the working tree: %v, %v; want the committed declaration unchanged", d, err)
		}
	})

	t.Run("no declaration at the ref is no error and enables nothing", func(t *testing.T) {
		clone := newGCClone(t, "")
		d, err := ReadCommitted(clone, "")
		if d != nil || err != nil {
			t.Fatalf("ReadCommitted on a repository without the declaration = %v, %v; want nil, nil", d, err)
		}
	})

	t.Run("a committed declaration that does not parse is an error", func(t *testing.T) {
		clone := newGCClone(t, "rebuild_dir=build\nkeep_dir=secrets\n")
		d, err := ReadCommitted(clone, "")
		if err == nil {
			t.Fatalf("ReadCommitted = %v, nil; want the committed declaration rejected whole", d)
		}
		if d != nil {
			t.Fatalf("a rejected declaration must read to nothing, got %v", d)
		}
	})

	t.Run("an explicit ref reads that ref", func(t *testing.T) {
		clone := newGCClone(t, "rebuild_dir=build/dist\n")
		head := strings.TrimSpace(gcGit(t, clone, "rev-parse", "HEAD"))
		d, err := ReadCommitted(clone, "HEAD")
		if err != nil || d == nil {
			t.Fatalf("ReadCommitted(clone, \"HEAD\") = %v, %v; want the declaration at HEAD", d, err)
		}
		if d.Ref != "HEAD" || d.SHA != head {
			t.Fatalf("Ref/SHA = %q/%q, want HEAD/%q", d.Ref, d.SHA, head)
		}
	})

	t.Run("the default branch is recorded metadata, never a guess", func(t *testing.T) {
		clone := newGCClone(t, "rebuild_dir=build/dist\n")
		ref, err := DefaultBranch(clone)
		if err != nil || ref != "origin/main" {
			t.Fatalf("DefaultBranch(clone) = %q, %v; want origin/main, nil", ref, err)
		}
		// The bare source repository has no remote-tracking metadata: its
		// default branch is unknown, and unknown is an error, never a
		// guessed branch name.
		parent := t.TempDir()
		gcGit(t, parent, "clone", "--quiet", "--bare", strings.TrimSpace(gcGit(t, clone, "config", "--get", "remote.origin.url")), filepath.Join(parent, "src.git"))
		if _, err := DefaultBranch(filepath.Join(parent, "src.git")); err == nil {
			t.Fatal("DefaultBranch on a repository without remote-tracking metadata = nil error; want an error")
		}
	})

	t.Run("an unreadable repository is an error", func(t *testing.T) {
		if d, err := ReadCommitted(t.TempDir(), ""); d != nil || err == nil {
			t.Fatalf("ReadCommitted on a directory without a repository = %v, %v; want nil, an error", d, err)
		}
	})
}
