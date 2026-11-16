package test

// helpers_test.go: the contract package's own copies of the three
// readers its suites share with the acceptance package's staying files
// (a directory of _test.go files exports nothing, so each package keeps
// its own copy; the names and behaviour mirror test/v1_harness_test.go,
// the former test/w68_build_step_rules_contract_test.go — whose only
// content was this reader and which dissolved into this file — and
// test/w246_skill_execution_intent_guard_test.go, whose users moved).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// v1Root is the module root (the tests run with cwd = the contract
// package, three levels below it).
func v1Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not three levels above the contract package: %v", err)
	}
	return root
}

// w68Flat reads a repo file and collapses every whitespace run to one
// space, so line wrapping cannot make the text assertions brittle.
func w68Flat(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// w246FrontmatterDescription answers the SKILL.md frontmatter's parsed
// description value — the block between the opening and closing `---`,
// unmarshalled as strict YAML the way pi's loader reads it — or the
// parse error.
func w246FrontmatterDescription(body string) (string, error) {
	lines := strings.Split(body, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return "", errW246NoFrontmatter
	}
	end := -1
	for i, ln := range lines[1:] {
		if strings.TrimSpace(ln) == "---" {
			end = i + 1
			break
		}
	}
	if end < 0 {
		return "", errW246NoFrontmatter
	}
	var fm struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return "", err
	}
	return fm.Description, nil
}

var errW246NoFrontmatter = errors.New("no `---` delimited frontmatter block")
