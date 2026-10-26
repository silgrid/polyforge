// Package workflow is the minimal workflow-bundle frontmatter read surface
// shared by pf: the model-candidate fields that drive model (and, through
// each candidate's `<harness>:` prefix, harness) selection.
//
// Design authority: docs/design/polyforge-vnext/01-workflow-skills.md.
package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"gopkg.in/yaml.v3"
)

// SharedDir is the sibling of the bundles under a workflows dir that holds
// the step definitions several bundles share: workflows/_shared/steps/<id>.md.
// It is not a bundle (no workflow.md); materialization copies it next to
// the bundle so the capsule resolves the same files.
const SharedDir = "_shared"

// StepDefinitionPath answers the step definition file for stepID in the
// bundle at bundleDir: the bundle's own steps/<id>.md when it exists (the
// bundle's file always wins), else the shared ../_shared/steps/<id>.md
// when that exists, else the bundle's own path (so the caller's read
// error names the expected location).
func StepDefinitionPath(bundleDir, stepID string) string {
	own := filepath.Join(bundleDir, "steps", stepID+".md")
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		return own
	}
	shared := filepath.Join(filepath.Dir(bundleDir), SharedDir, "steps", stepID+".md")
	if _, err := os.Stat(shared); err == nil {
		return shared
	}
	return own
}

// placeholder is one step-definition variable reference: {{name}}.
var placeholder = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

// PrinciplesVar is the one placeholder a step definition may use without
// declaring it: {{principles}} renders workflows/_shared/principles.md
// (PrinciplesFile), the engineering principles shared by every bundle, so
// the text lives once and each step states around it how it applies them.
const (
	PrinciplesVar  = "principles"
	PrinciplesFile = "principles.md"
)

// ReadStepDefinition reads the step definition StepDefinitionPath
// resolves and renders it with the step entry's vars (RenderStepVars),
// plus {{principles}} when the definition uses it (a step entry may not
// declare that var itself). A read error is returned unwrapped
// (os.IsNotExist holds).
func ReadStepDefinition(bundleDir, stepID string, vars map[string]string) ([]byte, error) {
	path := StepDefinitionPath(bundleDir, stepID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if _, ok := vars[PrinciplesVar]; ok {
		return nil, fmt.Errorf("%s: step entry declares the reserved var %q (it renders %s/%s)", path, PrinciplesVar, SharedDir, PrinciplesFile)
	}
	if bytes.Contains(raw, []byte("{{"+PrinciplesVar+"}}")) {
		text, err := os.ReadFile(filepath.Join(filepath.Dir(bundleDir), SharedDir, PrinciplesFile))
		if err != nil {
			return nil, fmt.Errorf("%s: {{%s}}: %w", path, PrinciplesVar, err)
		}
		withP := make(map[string]string, len(vars)+1)
		for k, v := range vars {
			withP[k] = v
		}
		withP[PrinciplesVar] = strings.TrimSpace(string(text))
		vars = withP
	}
	out, err := RenderStepVars(raw, vars)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// RenderStepVars substitutes every {{name}} placeholder of a step
// definition (frontmatter and body alike) with vars[name] — the
// `vars:` map of the workflow.md step entry, so one shared step text
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
		return nil, fmt.Errorf("step definition uses undeclared vars %v (declare them in the workflow.md step entry's vars)", missing)
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

// ---------------------------------------------------------------------------
// Minimal frontmatter read surface (the F4 lane: candidate-chain
// consumption): the fields that drive model selection — workflow-level
// defaults.model_candidates and the per-step model_candidates override,
// keyed by step id. The one thing judged here is the candidate format
// (polyforge#175, CheckCandidates): every candidate is `<harness>:<model>`
// and the retired `harness` key is refused; the driver's LoadWorkflow is
// the structural check of a bundle. Other unknown keys are ignored (a
// retired `kind:` in an old bundle snapshot included).

// Defaults carries the workflow-level fallback model candidates.
type Defaults struct {
	ModelCandidates []string `yaml:"model_candidates"`
}

// FrontmatterStep is one frontmatter step entry, reduced to the fields
// the read surface needs: the id (the workspace yaml's per-step model
// lists are keyed by it) and the per-step candidate override.
type FrontmatterStep struct {
	ID              string   `yaml:"id"`
	ModelCandidates []string `yaml:"model_candidates"`
}

// Frontmatter is the parsed workflow.md frontmatter (structure reduced to
// the minimal read surface; the Markdown body is never parsed here).
type Frontmatter struct {
	Defaults *Defaults         `yaml:"defaults"`
	Steps    []FrontmatterStep `yaml:"steps"`
}

// SplitFrontmatter splits a Markdown document into its YAML frontmatter
// block and the body that follows the closing fence. The file must begin
// with a `---` line; ok=false when it does not (a frontmatter-less
// workflow.md is legal input to this reader — the candidate chain is then
// simply absent).
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

// ReadFrontmatter reads one workflow.md and parses its frontmatter with
// the minimal surface above. A missing file, a file with no frontmatter
// block, or an empty one answers (nil, nil) — "no candidate chain" is a
// typed absent condition, never an error. A present-but-torn YAML block,
// or one CheckCandidates refuses (an unprefixed candidate, a retired
// `harness` key), answers (nil, err) naming the file: the caller decides
// whether that is fatal. The candidate lists come back canonical.
func ReadFrontmatter(path string) (*Frontmatter, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	fm, front, err := parseFrontmatter(raw)
	if fm == nil || err != nil {
		return nil, err
	}
	if err := CheckCandidates(front, fm); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return fm, nil
}

// parseFrontmatter is the unjudged yaml parse of one workflow.md:
// (nil, nil, nil) when it carries no frontmatter block.
func parseFrontmatter(raw []byte) (*Frontmatter, []byte, error) {
	front, _, ok := SplitFrontmatter(raw)
	if !ok || len(strings.TrimSpace(string(front))) == 0 {
		return nil, nil, nil
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return nil, nil, err
	}
	return &fm, front, nil
}

// RetiredHarnessHint is the rewrite instruction of a refused `harness`
// key (yaml step entries and bundle steps/defaults alike).
const RetiredHarnessHint = "the `harness` key was removed (polyforge#175) — move it into each model candidate's prefix, e.g. [claude:claude-opus-5-5, pi:example-gateway/model-a] (see docs/upgrade.md)"

// CheckCandidates is the polyforge#175 bundle-side format check of one
// parsed frontmatter block: a `harness` key under defaults or a step is
// refused, and every model_candidates entry must parse as
// `<harness>:<model>` (harness.ParseCandidate). fm's lists are rewritten
// in canonical form. The error names the step (or defaults) and index.
func CheckCandidates(front []byte, fm *Frontmatter) error {
	var keys struct {
		Defaults *struct {
			Harness yaml.Node `yaml:"harness"`
		} `yaml:"defaults"`
		Steps []struct {
			ID      string    `yaml:"id"`
			Harness yaml.Node `yaml:"harness"`
		} `yaml:"steps"`
	}
	if err := yaml.Unmarshal(front, &keys); err != nil {
		return err
	}
	if keys.Defaults != nil && keys.Defaults.Harness.Kind != 0 {
		return fmt.Errorf("defaults.harness: %s", RetiredHarnessHint)
	}
	for _, s := range keys.Steps {
		if s.Harness.Kind != 0 {
			return fmt.Errorf("step %s: harness: %s", s.ID, RetiredHarnessHint)
		}
	}
	canon := func(where string, list []string) error {
		for i, c := range list {
			n, err := harness.Normalize(c)
			if err != nil {
				return fmt.Errorf("%s[%d]: %w", where, i, err)
			}
			list[i] = n
		}
		return nil
	}
	if fm.Defaults != nil {
		if err := canon("defaults.model_candidates", fm.Defaults.ModelCandidates); err != nil {
			return err
		}
	}
	for _, s := range fm.Steps {
		if err := canon("step "+s.ID+": model_candidates", s.ModelCandidates); err != nil {
			return err
		}
	}
	return nil
}

// StepIDs answers the set of step ids declared by the installed bundles
// under workflowsDir (every <workflowsDir>/<bundle>/workflow.md) — the
// legal per-step keys of the workspace yaml's model lists. A bundle
// whose frontmatter is absent or torn contributes nothing (its own
// claim surfaces the defect); a bundle whose candidates CheckCandidates
// refuses still contributes its ids (that defect is the bundle's, never
// the yaml's — polyforge#175); an unreadable directory is an error.
func StepIDs(workflowsDir string) (map[string]bool, error) {
	ents, err := os.ReadDir(workflowsDir)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(workflowsDir, e.Name(), "workflow.md"))
		if err != nil {
			continue
		}
		fm, _, _ := parseFrontmatter(raw)
		if fm == nil {
			continue
		}
		for _, s := range fm.Steps {
			if s.ID != "" {
				ids[s.ID] = true
			}
		}
	}
	return ids, nil
}
