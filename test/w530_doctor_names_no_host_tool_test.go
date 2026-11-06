// carries: .polyforge/verify-scope.conf, internal/polyforge/cli/*

package test

// w530_doctor_names_no_host_tool_test.go: the doctor names no host
// prerequisite of its own. Every tool a repository needs on the host comes
// from that repository's `host_requires=` declaration in its
// .polyforge/verify-scope.conf; the doctor's code only probes what the
// scope engine lists. So no non-test Go file of the doctor's package
// (internal/polyforge/cli; testdata skipped) mentions the name of any
// prerequisite this repository declares — in any case mix, in an
// identifier, a string literal or a comment alike (a comment naming the
// tool is the first step back to special-casing it). Test files are out
// of scope: a test may probe the repository's own declaration.
//
// The word list is data: the names in `host_requires` of this tree's
// engine's `--json --base HEAD` report run at the repository root, so a
// prerequisite this repository declares later is covered without editing
// this file. An empty list is red — a conf that lost its declarations must
// not let the check pass by scanning for nothing.
//
// A declared name is matched as a plain substring. A name that is also a
// common word in Go code would collide with ordinary identifiers; such a
// name is answered by naming the prerequisite differently in the conf, not
// by an exemption here — the check has no exemption list.
//
// Falsifiable: the scan must read at least one file, and the doctor's
// verb file and its host prerequisites row among them; and the matcher
// must report a mixed-case mention in a synthetic source, or the check is
// red.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// w530DoctorTree is the scanned tree, relative to the repository root.
const w530DoctorTree = "internal/polyforge/cli"

// w530Hits answers "<name>:<line>" for every line of src that mentions
// one of words, case-insensitively (substring match, comments included).
func w530Hits(name, src string, words []string) []string {
	var hits []string
	for n, line := range strings.Split(src, "\n") {
		low := strings.ToLower(line)
		for _, w := range words {
			if strings.Contains(low, strings.ToLower(w)) {
				hits = append(hits, name+":"+strconv.Itoa(n+1))
				break
			}
		}
	}
	return hits
}

// w530DeclaredNames answers the host prerequisite names this repository's
// conf declares, as this tree's engine reports them.
func w530DeclaredNames(t *testing.T) []string {
	t.Helper()
	root := w25e2eRoot(t)
	out, errOut, code := w25e2eRun(t, root, nil, "--json", "--base", "HEAD")
	if code != 0 {
		t.Fatalf("engine --json --base HEAD at the repository root exited %d\n%s", code, errOut)
	}
	var rep struct {
		HostRequires []struct {
			Name string `json:"name"`
		} `json:"host_requires"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("engine report does not decode: %v\n%s", err, out)
	}
	var names []string
	for _, h := range rep.HostRequires {
		if strings.TrimSpace(h.Name) != "" {
			names = append(names, h.Name)
		}
	}
	return names
}

func TestW530DoctorCodeNamesNoDeclaredHostTool(t *testing.T) {
	words := w530DeclaredNames(t)
	if len(words) == 0 {
		t.Fatalf("the engine reports no host_requires for this repository: .polyforge/verify-scope.conf must declare its host prerequisites (`host_requires=<name>|<probe>|<hint>`), and an empty word list would let this check pass by scanning for nothing")
	}
	t.Logf("declared host prerequisites: %q", words)

	// The control: the matcher reports a mixed-case mention.
	var synth []string
	for _, w := range words {
		mixed := []rune(strings.ToLower(w))
		for i := range mixed {
			if i%2 == 0 {
				mixed[i] = []rune(strings.ToUpper(string(mixed[i])))[0]
			}
		}
		synth = append(synth, "package demo", "", "// probes "+string(mixed)+" on the host", "var x = 1")
	}
	if got := w530Hits("synthetic.go", strings.Join(synth, "\n"), words); len(got) != len(words) {
		t.Fatalf("the matcher misses a mixed-case mention in a synthetic source (hits %q for %d words): the scan below proves nothing", got, len(words))
	}

	root := w25e2eRoot(t)
	seen := map[string]bool{}
	scanned := 0
	var bad []string
	err := filepath.WalkDir(filepath.Join(root, w530DoctorTree), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		scanned++
		seen[filepath.Base(path)] = true
		bad = append(bad, w530Hits(rel, string(raw), words)...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", w530DoctorTree, err)
	}
	if scanned == 0 {
		t.Fatalf("scan %s read no Go file", w530DoctorTree)
	}
	for _, must := range []string{"doctor_host_requires.go", "verbs_doctor.go"} {
		if !seen[must] {
			t.Fatalf("scan %s did not read %s (read %d files): the check no longer covers the doctor", w530DoctorTree, must, scanned)
		}
	}
	t.Logf("scanned %d Go files under %s", scanned, w530DoctorTree)
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("the doctor's code names a host prerequisite a repository declares (%q, any case, comments included) — the doctor names none of its own; it probes what the scope engine lists: %s", words, strings.Join(bad, ", "))
	}
}
