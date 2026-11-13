package workflow_test

// w520_contract_compat_test.go: the contract version moves
// only on an incompatible change, at most once per release. A compatible
// change (a new optional key, a new value of a key) regenerates the golden
// file in place and keeps ContractVersion; an incompatible one the golden
// shows mechanically (a removed key, a new required key, a removed value)
// is red until contract-pending.md registers it; and the rule reads the
// same in its three places.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// insertAfter inserts line after the line header of dump, failing when
// dump carries no such line.
func insertAfter(t *testing.T, dump, header, line string) string {
	t.Helper()
	if !strings.Contains(dump, "\n"+header+"\n") {
		t.Fatalf("the registry dump carries no %q line:\n%s", header, dump)
	}
	return strings.Replace(dump, "\n"+header+"\n", "\n"+header+"\n"+line+"\n", 1)
}

// dropLine deletes the one line of dump re matches, failing when none does.
func dropLine(t *testing.T, dump, re string) string {
	t.Helper()
	rx := regexp.MustCompile(`(?m)^` + re + `\n`)
	if !rx.MatchString(dump) {
		t.Fatalf("the registry dump carries no line %q:\n%s", re, dump)
	}
	return rx.ReplaceAllString(dump, "")
}

func TestW520CompatibleChangeKeepsTheVersion(t *testing.T) {
	base := workflow.RegistryDump()
	cur := insertAfter(t, base, "[lane keys]", "aaa_optional: bool (default false): a new optional key")
	cur = insertAfter(t, cur, "[values]", "lane.acceptance: maybe")

	if got := workflow.IncompatibleChanges(base, cur); len(got) != 0 {
		t.Errorf("a new optional key and a new value are compatible; got incompatible %v", got)
	}
	err := workflow.JudgeRegistry(cur, base, base, 0)
	if err == nil {
		t.Fatal("the registry changed and the golden file was not regenerated: want an error")
	}
	msg := err.Error()
	for _, want := range []string{"in place", "PF_WRITE_CONTRACT_GOLDEN=1", "go generate", fmt.Sprintf("ContractVersion stays %d", workflow.ContractVersion)} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "bump workflow.ContractVersion") {
		t.Errorf("a compatible change must not ask for a version bump:\n%s", msg)
	}
	if err := workflow.JudgeRegistry(cur, cur, cur, 0); err != nil {
		t.Errorf("golden and baseline regenerated in place, the version unchanged: want the pin green, got %v", err)
	}
}

func TestW520IncompatibleChangeMustBeRegistered(t *testing.T) {
	dump := workflow.RegistryDump()
	for _, want := range []string{"\n[required]\n", "\nlane.contract\n", "\n[values]\n", "\nresources.admission: heavy\n"} {
		if !strings.Contains(dump, want) {
			t.Errorf("the registry dump carries no %q", strings.TrimSpace(want))
		}
	}
	if got := workflow.IncompatibleChanges(dump, dump); len(got) != 0 {
		t.Fatalf("the dump against itself: got incompatible %v", got)
	}

	mutants := []struct {
		name, item, mut string
	}{
		{"a removed key", "step.waivable", dropLine(t, dump, `waivable: .*`)},
		{"a new required key", "lane.mandated", insertAfter(t, insertAfter(t, dump, "[lane keys]", "mandated: string, required"), "[required]", "lane.mandated")},
		{"a removed value", "resources.admission: heavy", dropLine(t, dump, `resources\.admission: heavy`)},
	}
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			if got := workflow.IncompatibleChanges(dump, m.mut); len(got) == 0 {
				t.Errorf("%s is incompatible: got none", m.name)
			}
			err := workflow.JudgeRegistry(m.mut, m.mut, dump, 0)
			if err == nil {
				t.Fatalf("%s with contract-pending.md empty: want an error", m.name)
			}
			for _, want := range []string{"contract-pending.md", "register", m.item} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
			if err := workflow.JudgeRegistry(m.mut, m.mut, dump, 1); err != nil {
				t.Errorf("%s registered in contract-pending.md: want the pin green, got %v", m.name, err)
			}
		})
	}
}

// versionRule is the sentence the rule's three places all carry.
const versionRule = "only an incompatible change bumps it, at most once per release"

// commentAbove answers the // comment block right above the line of src
// that starts with decl.
func commentAbove(t *testing.T, file, decl string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, decl) {
			continue
		}
		j := i
		for j > 0 && strings.HasPrefix(lines[j-1], "//") {
			j--
		}
		return strings.Join(lines[j:i], "\n")
	}
	t.Fatalf("%s has no line starting %q", file, decl)
	return ""
}

func TestW520VersionRuleText(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/reference/workflow-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "\n## Versions and the drain\n")
	if i < 0 {
		t.Fatal("the reference has no `## Versions and the drain` section")
	}
	section := doc[i+1:]
	if j := strings.Index(section[3:], "\n## "); j >= 0 {
		section = section[:j+3]
	}
	places := map[string]string{
		"contract.go (ContractVersion)":                   commentAbove(t, "contract.go", "const ContractVersion"),
		"workflow-contract.md (Versions and the drain)":   section,
		"load_test.go (TestRegistryGoldenPinsTheVersion)": commentAbove(t, "load_test.go", "func TestRegistryGoldenPinsTheVersion("),
	}
	lead := regexp.MustCompile(`(?m)^\s*//`)
	for where, text := range places {
		text = strings.Join(strings.Fields(lead.ReplaceAllString(text, "")), " ")
		for _, want := range []string{versionRule, "contract-pending.md"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q:\n%s", where, want, text)
			}
		}
		for _, gone := range []string{"one exception", "exception to the bump"} {
			if strings.Contains(strings.ToLower(text), gone) {
				t.Errorf("%s still states the rule as an exception (%q):\n%s", where, gone, text)
			}
		}
	}
}
