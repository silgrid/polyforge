package test

// carries: workflows/integrate/**, workflows/_shared/scripts/verify-scope, workflows/_shared/scripts/audit-deps, workflows/_shared/scripts/gate.py, workflows/_shared/scripts/integrate.py, workflows/_shared/scripts/revert.py

// pf113_integrate_lane_neutrality_contract_test.go (polyforge#113 AC3, the
// Q8 ruling): the integrate lane is project-neutral. No file of
// workflows/integrate/, and no script its skills' `commands:` lines invoke
// (plus the shared scope engine and its audit helper, which the gate runs),
// names a project component or package — `pi-web` or `webui`, the shipped
// npm trees (`.polyforge/` and the product's own name are not components).
// A hit is red wherever it appears — code, comment or docstring alike: a
// comment naming a component is the first step back to special-casing the
// lane. The exception register is per file, one pattern per line shape with
// its reason; it is empty today and only shrinks (a pattern no line matches
// any more is red too, naming the entry to delete).
//
// The guard is a guard: on a clean tree nothing needs implementing for it
// to pass. The one hit at filing time — verify-scope's nested-root docstring
// example `cd pi-web` — was rewritten by this change, which is why this test
// was red once on the tree before that rewrite.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestPf113IntegrateLaneNamesNoProjectPackage$' -count=1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pf113Banned are the project components and packages the integrate lane and
// the scripts it calls may never name (the Q8 ruling: component names and
// paths; .polyforge/ is the product's own convention directory and the
// product name itself is not a component).
var pf113Banned = []string{"pi-web", "webui"}

// pf113Allowed is one exempt line shape of a file: a line naming a banned
// term may when it matches Line (a regular expression), for Why.
type pf113Allowed struct {
	Line, Why string
}

// pf113Exceptions is the register of lines that may still name a component,
// per file (see the file comment). It is empty today and only shrinks.
var pf113Exceptions = map[string][]pf113Allowed{}

// pf113SkillCommands answers every script name the lane's skills' commands:
// front-matter lines invoke, in file order.
func pf113SkillCommands(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	files, err := filepath.Glob(filepath.Join(root, "workflows", "integrate", "skills", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("the integrate skills: %v %v", files, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, ln := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(ln, "commands:") {
				continue
			}
			list := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(ln, "commands:")), "["), "]")
			for _, name := range strings.Split(list, ",") {
				if name = strings.TrimSpace(name); name != "" {
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("the integrate skills declare no commands: the scan's script list would be empty")
	}
	return names
}

// TestPf113IntegrateLaneNamesNoProjectPackage scans the integrate lane and
// the scripts it calls (the skills' commands: list, plus the scope engine and
// the audit helper the gate runs) for the banned component names.
func TestPf113IntegrateLaneNamesNoProjectPackage(t *testing.T) {
	root := v1Root(t)

	// The scanned files: every file under workflows/integrate/, the scripts
	// the skills invoke, and the shared engine and audit helper.
	scanned := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "workflows", "integrate"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		scanned[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{"workflows/_shared/scripts/verify-scope", "workflows/_shared/scripts/audit-deps"} {
		scanned[extra] = true
	}
	for _, name := range pf113SkillCommands(t, root) {
		scanned[filepath.ToSlash(filepath.Join("workflows", "_shared", "scripts", name))] = true
	}

	used := map[string]bool{} // file + "\x00" + pattern → matched a line
	var bad []string
	paths := make([]string, 0, len(scanned))
	for rel := range scanned {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
	allowed:
		for no, ln := range strings.Split(string(raw), "\n") {
			hit := ""
			for _, term := range pf113Banned {
				if strings.Contains(ln, term) {
					hit = term
					break
				}
			}
			if hit == "" {
				continue
			}
			for _, a := range pf113Exceptions[rel] {
				if regexp.MustCompile(a.Line).MatchString(ln) {
					used[rel+"\x00"+a.Line] = true
					continue allowed
				}
			}
			bad = append(bad, fmt.Sprintf("%s:%d names %q: %s", rel, no+1, hit, strings.TrimSpace(ln)))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("the integrate lane and its scripts must not name a project component:\n%s", strings.Join(bad, "\n"))
	}
	// The register only shrinks: an exception pattern no line matches any
	// more is red, naming the entry to delete.
	for rel, rows := range pf113Exceptions {
		for _, a := range rows {
			if !used[rel+"\x00"+a.Line] {
				t.Errorf("the exception %q of %s matches no line any more - delete it", a.Line, rel)
			}
		}
	}
}
