// Package workflowtest writes contract-valid lanes for tests: a
// workflow.yaml carrying the contract version, the lane's name and a
// description ahead of the caller's lane keys, and a minimal definition
// for every step the lane needs that the caller did not write itself. The lane is loaded through the one lane loader before it
// is handed back, so a fixture the contract refuses fails the test that
// wrote it.
package workflowtest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// Header answers the lines every lane's workflow.yaml starts with: the
// contract version, name and a description.
func Header(name string) string {
	return fmt.Sprintf("contract: %d\nname: %s\ndescription: test lane %s\n", workflow.ContractVersion, name, name)
}

// Definition answers a minimal definition file for id: name, description
// and a body that uses each of vars.
func Definition(id string, vars ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: test definition %s\n---\n\nRun %s.\n", id, id, id)
	for _, v := range vars {
		fmt.Fprintf(&b, "{{%s}}\n", v)
	}
	return b.String()
}

// WriteLane writes the lane name under root — its workflow.yaml,
// Header(name) followed by front — fills in every definition the loader finds
// missing (Definition, using the step's declared vars) and answers the
// lane directory. Any other contract defect fails the test.
func WriteLane(t testing.TB, root, name, front string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	write(t, filepath.Join(dir, workflow.WorkflowFile), Header(name)+front)
	FillDefinitions(t, dir)
	return dir
}

// WriteLaneUnchecked is WriteLane for a lane the test expects the loader
// to refuse: the missing definitions are filled in, nothing is judged.
func WriteLaneUnchecked(t testing.TB, root, name, front string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	write(t, filepath.Join(dir, workflow.WorkflowFile), Header(name)+front)
	fill(t, dir)
	return dir
}

// FillDefinitions writes a minimal definition for every definition the
// lane at dir needs and lacks, then fails the test on any remaining
// contract defect.
func FillDefinitions(t testing.TB, dir string) {
	t.Helper()
	fill(t, dir)
	if _, errs := workflow.Load(dir); len(errs) > 0 {
		t.Fatalf("workflowtest: lane %s is not contract-valid: %v", dir, workflow.AsLoadError(dir, errs))
	}
}

// FillMissing writes a minimal definition for every definition the lane
// at dir needs and lacks, judging nothing else (a test of a refused lane
// keeps its own defect).
func FillMissing(t testing.TB, dir string) {
	t.Helper()
	fill(t, dir)
}

// fill writes a minimal definition for every definition the lane at dir
// needs and lacks.
func fill(t testing.TB, dir string) {
	t.Helper()
	lane, errs := workflow.Load(dir)
	for _, e := range errs {
		if e.Code != workflow.CodeDefinitionMissing {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(e.File), ".md")
		var vars []string
		s := lane.Step(id)
		if s != nil {
			for v := range s.Vars {
				vars = append(vars, v)
			}
			sort.Strings(vars)
		}
		write(t, e.File, Definition(id, vars...))
	}
}

func write(t testing.TB, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// RenamedLaneSet copies the lanes root from (the shipped workflows/, say)
// to a new temporary directory and renames each lane of renames (old name
// → new name): its directory and its `name`. Every lane of the copy must
// load; it answers the copy's root.
func RenamedLaneSet(t testing.TB, from string, renames map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(root, rel), 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), raw, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	for old, name := range renames {
		if err := os.Rename(filepath.Join(root, old), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := workflow.LaneDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		p := filepath.Join(d, workflow.WorkflowFile)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := "\n" + string(raw)
		for old, name := range renames {
			if filepath.Base(d) == name {
				s = strings.Replace(s, "\nname: "+old+"\n", "\nname: "+name+"\n", 1)
			}
		}
		write(t, p, s[1:])
	}
	for _, d := range dirs {
		if _, errs := workflow.Load(d); len(errs) > 0 {
			t.Fatalf("workflowtest: the renamed lane set does not load: %v", workflow.AsLoadError(d, errs))
		}
	}
	return root
}
