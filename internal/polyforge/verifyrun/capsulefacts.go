package verifyrun

// capsulefacts.go: what verify-run reads from the capsule — the task
// repos (capsule.json) and the upstream step outputs the verification
// contract keys on (runtime-state/execution.json): the authored
// test_files, the authored scenario titles and their bindings, the
// regression_test paths that pin the change, and the authored-tests
// baseline stamp. Read-only: verify-run never writes execution.json.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// e2eAuthorStep is the conventional id of a lane's authored-acceptance
// step (its output carries scenario_titles and bindings).
const e2eAuthorStep = "e2e-author"

// testFilesCommitKey is the driver-written authored-tests baseline stamp
// on an authoring step's output (task repo -> commit).
const testFilesCommitKey = "test_files_commit"

// Binding ties one authored scenario title to a unit of a command's
// output: the command (run in repos/<Repo>), the unit (a test name, a
// suite title) and Marker, a regular expression one line of that
// command's output must match for the scenario to pass.
type Binding struct {
	Title   string `json:"title"`
	Repo    string `json:"repo"`
	Command string `json:"command"`
	Unit    string `json:"unit"`
	Marker  string `json:"marker"`
}

// TestFile is one declared test_files path and the step that declared it.
type TestFile struct {
	Step string
	Path string
}

// capsuleFacts is the folded read of capsule.json and execution.json.
type capsuleFacts struct {
	Repos []string
	// TestFiles are every done step's declared test_files (e2e-author,
	// repro) in step order.
	TestFiles []TestFile
	// Protected are the repo-relative paths that pin this change: the
	// test_files and the regression_test paths. A unit in one is never
	// pre-existing.
	Protected []string
	// Authored: the workflow has an authored acceptance step.
	Authored       bool
	ScenarioTitles []string
	Bindings       []Binding
	// AuthoredStamp is the authoring step's baseline commit per repo.
	AuthoredStamp map[string]string
}

// readCapsuleFacts reads capsule.json (required: the command runs at a
// capsule root) and execution.json (absent = no upstream facts).
func readCapsuleFacts(dir string) (*capsuleFacts, error) {
	raw, err := os.ReadFile(filepath.Join(dir, capsule.CapsuleJSON))
	if err != nil {
		return nil, fmt.Errorf("not a capsule root (%s unreadable: %v) — run pf verify-run in the capsule root", capsule.CapsuleJSON, err)
	}
	var id capsule.Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", capsule.CapsuleJSON, err)
	}
	f := &capsuleFacts{AuthoredStamp: map[string]string{}}
	for name := range id.Repos {
		if st, err := os.Stat(filepath.Join(dir, capsule.DirRepos, name)); err == nil && st.IsDir() {
			f.Repos = append(f.Repos, name)
		}
	}
	sort.Strings(f.Repos)
	if len(f.Repos) == 0 {
		return nil, fmt.Errorf("%s names no task repo present under %s/", capsule.CapsuleJSON, capsule.DirRepos)
	}
	raw, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(capsule.ExecutionJSON)))
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, fmt.Errorf("%s unreadable: %v", capsule.ExecutionJSON, err)
	}
	var ex capsule.Execution
	if err := json.Unmarshal(raw, &ex); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", capsule.ExecutionJSON, err)
	}
	f.fold(&ex)
	return f, nil
}

// fold reads the upstream facts out of the done steps' outputs, in a
// stable order (step ids sorted; the workflow order is not recorded in
// execution.json and nothing here depends on it).
func (f *capsuleFacts) fold(ex *capsule.Execution) {
	seen := map[string]bool{}
	protect := func(p string) {
		if c := cleanRel(p); c != "" && !seen[c] {
			seen[c] = true
			f.Protected = append(f.Protected, c)
		}
	}
	if st, ok := ex.Steps[e2eAuthorStep]; ok && st != nil {
		f.Authored = true
		if st.Status == capsule.StepDone {
			f.ScenarioTitles = stringsOf(st.Output["scenario_titles"])
			f.Bindings = bindingsOf(st.Output["bindings"])
		}
	}
	for _, id := range sortedKeys(ex.Steps) {
		st := ex.Steps[id]
		if st == nil || st.Status != capsule.StepDone {
			continue
		}
		for _, p := range stringsOf(st.Output["test_files"]) {
			if strings.TrimSpace(p) == "" {
				continue
			}
			f.TestFiles = append(f.TestFiles, TestFile{Step: id, Path: strings.TrimSpace(p)})
			protect(p)
		}
		if rt, ok := st.Output["regression_test"].(string); ok {
			for _, p := range regressionPaths(rt) {
				protect(p)
			}
		}
		if id == e2eAuthorStep {
			if stamp, ok := st.Output[testFilesCommitKey].(map[string]any); ok {
				for repo, v := range stamp {
					if s, ok := v.(string); ok && s != "" {
						f.AuthoredStamp[repo] = s
					}
				}
			}
		}
	}
	if f.ScenarioTitles == nil && f.Authored {
		f.ScenarioTitles = []string{}
	}
}

// stringsOf coerces a decoded JSON list to its string elements.
func stringsOf(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// bindingsOf decodes e2e-author's optional bindings list (the driver
// validated it at acceptance; an unreadable entry is dropped here and its
// title then reads as document-only).
func bindingsOf(v any) []Binding {
	raw, err := json.Marshal(v)
	if err != nil || v == nil {
		return nil
	}
	var out []Binding
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// cleanRel normalizes one repo-relative path ("" when it is not one).
func cleanRel(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	c := path.Clean(p)
	if path.IsAbs(c) || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	return c
}

// regressionPaths parses a fix step's regression_test: comma-separated
// entries, each reduced to its path token (a trailing "(TestFoo)" or a
// test name after whitespace is dropped); a `waived: ...` value names
// none. The same reading the driver's pre-existing boundary applies.
func regressionPaths(rt string) []string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rt)), "waived") {
		return nil
	}
	var out []string
	for _, item := range strings.Split(rt, ",") {
		fields := strings.Fields(item)
		if len(fields) == 0 {
			continue
		}
		tok := fields[0]
		if i := strings.IndexAny(tok, "(:"); i >= 0 {
			tok = tok[:i]
		}
		if c := cleanRel(tok); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// protectedPath answers whether a repo-relative path is a protected test
// (the exact path, or under a protected directory).
func (f *capsuleFacts) protectedPath(p string) bool {
	c := cleanRel(p)
	if c == "" {
		return false
	}
	for _, pr := range f.Protected {
		if c == pr || strings.HasPrefix(c, pr+"/") {
			return true
		}
	}
	return false
}
