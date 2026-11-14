// Package workflow is the workflow contract: the one lane loader (Load)
// every reader of a lane definition calls, its registry and typed WFC-*
// errors, and the definition, placeholder and fragment rules the loader
// and the driver's dispatch share.
//
// Design authority: docs/reference/workflow-contract.md.
package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SharedDir is the sibling of the bundles under a workflows dir that holds
// the step definitions several bundles share (workflows/_shared/steps/<id>.md)
// and the fragments a step definition includes (workflows/_shared/<name>.md,
// rendered by a {{<name>}} placeholder that needs no vars declaration).
// It is not a bundle (no workflow.yaml); materialization copies it next to
// the bundle so the capsule resolves the same files.
const SharedDir = "_shared"

// StepDefinitionPath answers the step definition file for stepID in the
// bundle at bundleDir: the bundle's own steps/<id>.md when it exists (the
// bundle's file always wins), else the shared ../_shared/steps/<id>.md
// when that exists, else the bundle's own path (so the caller's read
// error names the expected location).
func StepDefinitionPath(bundleDir, stepID string) string {
	return stepDefinitionPath(bundleDir, SiblingShared(bundleDir), stepID)
}

// stepDefinitionPath is StepDefinitionPath over an explicit shared tree
// (a user lane's is the built-in one, LoadUser).
func stepDefinitionPath(bundleDir, shared, stepID string) string {
	own := filepath.Join(bundleDir, "steps", stepID+".md")
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		return own
	}
	sharedDef := filepath.Join(shared, "steps", stepID+".md")
	if _, err := os.Stat(sharedDef); err == nil {
		return sharedDef
	}
	return own
}

// SiblingShared answers the shared tree beside the lane at bundleDir
// (../_shared): a built-in lane's, and a capsule snapshot's, which the
// claim lays out beside the lane whatever its source.
func SiblingShared(bundleDir string) string {
	return filepath.Join(filepath.Dir(bundleDir), SharedDir)
}

// SharedFragment is the conventional path of one shared fragment a
// step definition may include: workflows/_shared/<name>.md, rendered
// by a {{<name>}} placeholder that needs no vars declaration (the
// {{principles}} include generalized, a block several
// step files repeat lives once as a fragment and every step file
// includes it). Fragments are lane-neutral: no bundle, project or
// repo name is hardcoded in one.
func SharedFragment(bundleDir, name string) string {
	return sharedFragment(SiblingShared(bundleDir), name)
}

// sharedFragment is the fragment name of the shared tree at shared.
func sharedFragment(shared, name string) string {
	return filepath.Join(shared, name+".md")
}

// placeholder is one step-definition variable reference: {{name}}.
var placeholder = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

// PrinciplesVar is the reserved shared fragment every step definition
// may include without declaring it: {{principles}} renders
// workflows/_shared/principles.md (PrinciplesFile), the engineering
// principles shared by every bundle, so the text lives once and each
// step states around it how it applies them.
const (
	PrinciplesVar  = "principles"
	PrinciplesFile = "principles.md"
)

// RenderStepVars substitutes every {{name}} placeholder of a step
// definition (frontmatter and body alike) with vars[name] — the
// `vars:` map of the workflow.yaml step entry (and its `vars_files`), so one shared step text
// carries each bundle's lane-specific facts (decision 2026-10-16 G1
// follow-up). Values are inserted verbatim (a trailing newline of a
// YAML block scalar dropped) and never re-scanned. Fail-closed both
// ways: a placeholder without a var and a var no placeholder uses are
// errors, so a typo cannot ship a half-rendered prompt.
func RenderStepVars(raw []byte, vars map[string]string) ([]byte, error) {
	used, seen := map[string]bool{}, map[string]bool{}
	var missing []string
	out := placeholder.ReplaceAllFunc(raw, func(m []byte) []byte {
		name := string(m[2 : len(m)-2])
		v, ok := vars[name]
		if !ok {
			if !seen[name] {
				seen[name] = true
				missing = append(missing, name)
			}
			return m
		}
		used[name] = true
		return []byte(strings.TrimRight(v, "\n"))
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("step definition uses undeclared vars %v (declare them in the workflow.yaml step entry's vars or vars_files)", missing)
	}
	var unused []string
	for name := range vars {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return nil, fmt.Errorf("step entry declares vars %v the step definition never uses", unused)
	}
	return out, nil
}

// SplitFrontmatter splits a Markdown document — a step definition, a
// skill — into its YAML frontmatter block and the body that follows the
// closing fence. The file must begin with a `---` line; ok=false when it
// does not.
func SplitFrontmatter(content []byte) (front, body []byte, ok bool) {
	s := string(content)
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return nil, nil, false
	}
	if strings.TrimRight(s[:nl], " \r") != "---" {
		return nil, nil, false
	}
	rest := s[nl+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, nil, false
	}
	block := rest[:end+1]
	// a closing fence may carry trailing spaces / CRLF noise
	tail := rest[end+4:]
	after := ""
	if nl2 := strings.IndexByte(tail, '\n'); nl2 >= 0 {
		tail, after = tail[:nl2], tail[nl2+1:]
	}
	if strings.TrimRight(tail, " \r") != "" {
		return nil, nil, false // not a fence close (e.g. ---- separator in the body)
	}
	return []byte(block), []byte(after), true
}

// RetiredHarnessHint is the rewrite instruction of a refused `harness`
// key (yaml step entries and bundle steps/defaults alike).
const RetiredHarnessHint = "the `harness` key was removed — move it into each model candidate's prefix, e.g. [claude:claude-opus-5-5, pi:example-gateway/model-a]"

// LaneDirs answers the lane directories under a lanes root, in name order:
// every subdirectory except those whose name begins with `_` (`_shared`,
// `_archive` are not lanes).
func LaneDirs(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out, nil
}

// LanePresent reports whether the lanes root carries a lane named name: a
// directory of that name, not `_`-prefixed, holding a workflow.yaml — or a
// contract 4 workflow.md, which the loader then refuses with its rewrite
// (a lane not yet rewritten is named as such, not as missing). A name it
// does not carry is the claim's WFC-123.
func LanePresent(root, name string) bool {
	if name == "" || strings.HasPrefix(name, "_") || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, f := range []string{WorkflowFile, Contract4File} {
		if st, err := os.Stat(filepath.Join(root, name, f)); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}
