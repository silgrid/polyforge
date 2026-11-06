package workflow

// contract_doc_test.go (②): docs/reference/workflow-contract.md and the
// registry say the same thing — every key table, the records, the
// removed keys, the properties and the error codes are held equal both
// ways, and the page names the version the registry implements.

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const contractDoc = "../../../docs/reference/workflow-contract.md"

// docTable answers the rows of the first table under heading: each row's
// cells, trimmed, with `\|` kept as a literal `|`.
func docTable(t *testing.T, doc, heading string) [][]string {
	t.Helper()
	i := strings.Index(doc, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("%s carries no %q section", contractDoc, heading)
	}
	var rows [][]string
	inTable := false
	for _, line := range strings.Split(doc[i+len(heading)+2:], "\n") {
		if strings.HasPrefix(line, "#") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		inTable = true
		cells := strings.Split(strings.ReplaceAll(strings.Trim(line, "|"), `\|`, "\x00"), "|")
		for k := range cells {
			cells[k] = strings.ReplaceAll(strings.TrimSpace(cells[k]), "\x00", "|")
		}
		if strings.HasPrefix(cells[0], "---") || cells[0] == "Key" || cells[0] == "Code" || cells[0] == "Property" || cells[0] == "Form" || cells[0] == "Rule" || cells[0] == "When" || cells[0] == "Record" || cells[0] == "Removed key" {
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: the %q table is empty", contractDoc, heading)
	}
	return rows
}

// unquote strips one pair of backquotes.
func unquote(s string) string { return strings.TrimSuffix(strings.TrimPrefix(s, "`"), "`") }

func sameKeys(t *testing.T, what string, doc []string, reg []string) {
	t.Helper()
	sort.Strings(doc)
	sort.Strings(reg)
	if strings.Join(doc, ",") != strings.Join(reg, ",") {
		t.Errorf("%s: the reference lists %v, the registry %v", what, doc, reg)
	}
}

func TestReferenceDocMatchesTheRegistry(t *testing.T) {
	raw, err := os.ReadFile(contractDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	if !strings.Contains(doc, fmt.Sprintf("This is contract **%d**.", ContractVersion)) || !strings.Contains(doc, fmt.Sprintf("contract.v%d.golden", ContractVersion)) {
		t.Errorf("%s does not name contract %d and its golden file", contractDoc, ContractVersion)
	}
	keyTable := func(heading string, reg map[string]keySpec) {
		var got []string
		for _, r := range docTable(t, doc, heading) {
			got = append(got, unquote(r[0]))
		}
		sameKeys(t, heading, got, sortedKeys(reg))
	}
	keyTable("### Lane keys", laneKeys)
	keyTable("### defaults keys", defaultsKeys)
	keyTable("### Step keys", stepKeys)
	keyTable("### on_fail keys", onFailKeys)
	keyTable("### budget keys", budgetKeys)
	keyTable("### resources keys", resourcesKeys)
	keyTable("### hub keys", hubKeys)
	keyTable("### Skill keys", skillKeys)
	keyTable("### Definition keys", defKeys)

	// removed keys, each with a rewrite
	var removed, reg []string
	for _, r := range docTable(t, doc, "### Removed keys") {
		removed = append(removed, unquote(r[0]))
		if len(r) != 2 || strings.TrimSpace(r[1]) == "" {
			t.Errorf("removed key %s: the reference names no rewrite", r[0])
		}
	}
	for where, keys := range removedKeys {
		for k := range keys {
			reg = append(reg, where+"."+k)
		}
	}
	sameKeys(t, "### Removed keys", removed, reg)

	// properties
	var props []string
	for _, r := range docTable(t, doc, "## Properties") {
		k := unquote(r[0])
		props = append(props, k)
		if r[1] != Properties[k] {
			t.Errorf("property %s: the reference says %q, the registry %q", k, r[1], Properties[k])
		}
	}
	sameKeys(t, "## Properties", props, sortedKeys(Properties))

	// error codes, meaning for meaning
	var codes []string
	for _, r := range docTable(t, doc, "## Error codes") {
		c := unquote(r[0])
		codes = append(codes, c)
		if want, ok := Codes[c]; ok && r[1] != want {
			t.Errorf("code %s: the reference says %q, the registry %q", c, r[1], want)
		}
	}
	sameKeys(t, "## Error codes", codes, sortedKeys(Codes))
	if regexp.MustCompile(`WFC-\d{3}`).FindAllString(doc, -1) == nil {
		t.Fatal("the reference names no WFC code")
	}

	// contract 3 removed the daemon actions, the checks, the facts and
	// the sub-definitions; contract 4 the step I/O tables, the attrs, the
	// records and the effect and hand-off shapes: the reference documents
	// none of them as the contract's (each removed key's rewrite still
	// names what replaced it)
	for _, gone := range []string{"\n## Actions\n", "\n## Checks\n", "\n## Sub-definitions\n", "\n### Facts\n", "\n### sweep keys\n", "\n### evidence keys\n", "\n### merge-pr\n", "\n### run\n",
		"\n### integration keys\n", "\n### outputs keys\n", "\n### inputs[] keys\n", "\n### verifies[] keys\n", "\n### Records\n", "\n### attrs.&lt;name&gt; keys\n"} {
		if strings.Contains(doc, gone) {
			t.Errorf("%s still documents %q, which a contract removed", contractDoc, strings.TrimSpace(gone))
		}
	}
}
