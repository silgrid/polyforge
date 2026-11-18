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
