// carries: .polyforge/verify-scope.conf, docs/reference/config.md, docs/reference/commands.md, docs/concepts.md, docs/reference/release.md, docs/contributing.md, workflows/integrate/README.md, workflows/integrate/skills/full-gate.md

package test

// gate_groups_contract_test.go (polyforge#244, AC3): the full gate's
// parallel groups are a repository declaration (owner Q2: group=<name>|
// <full-N|command> and full_parallel=<N>, default 2, an invalid value
// diagnosed and serial), this repository declares the owner-confirmed
// Go / pi-web split (Q7: the first 3 full commands, the last 5, the
// dependency audit in neither), and the documents that describe the full
// gate carry the same contract — the config reference's key table, the
// commands reference's --parallel (Q10), the concepts page's progress
// interleaving, the release reference's plan digest rule (Q3: groups are
// recorded only when declared) and the two integrate lane texts.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestGateGroups' -count=1

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gateGroupsConf reads the repository's own verification-bar declarations:
// the full-phase command names in file order (anonymous full_command= lines
// are named full-<i>), the group= bindings in file order and the declared
// full_parallel ("" = undeclared).
func gateGroupsConf(t *testing.T, root string) (full []string, groups map[string][]string, order []string, parallel string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf(".polyforge/verify-scope.conf must exist: %v", err)
	}
	groups = map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "full_command":
			full = append(full, "full-"+strconv.Itoa(len(full)+1))
		case "group":
			name, target, ok := strings.Cut(val, "|")
			if !ok {
				t.Fatalf("group= without the <name>|<full-N|command> shape: %q", line)
			}
			if _, seen := groups[name]; !seen {
				order = append(order, name)
			}
			groups[name] = append(groups[name], strings.TrimSpace(target))
		case "full_parallel":
			parallel = strings.TrimSpace(val)
		}
	}
	return full, groups, order, parallel
}

// TestGateGroupsRepoDeclaration pins this repository's own grouping: the
// owner's Go / pi-web split over the full tail, nothing else.
func TestGateGroupsRepoDeclaration(t *testing.T) {
	root := v1Root(t)
	full, groups, order, parallel := gateGroupsConf(t, root)
	if len(full) != 8 {
		t.Fatalf("the full tail must carry 8 full_command= lines (the Go 3 and the pi-web 5), got %d: %v", len(full), full)
	}
	want := map[string][]string{
		"go":     {"full-1", "full-2", "full-3"},
		"pi-web": {"full-4", "full-5", "full-6", "full-7", "full-8"},
	}
	for name, targets := range want {
		if strings.Join(groups[name], ",") != strings.Join(targets, ",") {
			t.Fatalf("group %q binds %v, want %v", name, groups[name], targets)
		}
	}
	if len(groups) != len(want) {
		t.Fatalf("want exactly the go and pi-web groups, got %v (declared in order %v)", groups, order)
	}
	// The dependency audit is the engine's own appended command: no group
	// line may claim it (it runs serially after the groups).
	for name, targets := range groups {
		for _, target := range targets {
			if target == "audit" || strings.HasPrefix(target, "audit-") {
				t.Fatalf("group %q binds the engine's dependency audit %q: the audit runs serially after the groups", name, target)
			}
		}
	}
	if parallel != "" {
		t.Fatalf("full_parallel must stay undeclared so the default 2 applies, got %q", parallel)
	}
}

// TestGateGroupsDocsContract pins the documents the change reaches, each
// carrying the same contract in the same words.
func TestGateGroupsDocsContract(t *testing.T) {
	root := v1Root(t)
	flat := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
		return strings.Join(strings.Fields(string(raw)), " ")
	}
	for _, c := range []struct {
		rel   string
		wants []string
	}{
		{"docs/reference/config.md", []string{
			"| `group` |", "| `full_parallel` |",
			"default 2", "serial",
			"the gate's plan records the group and the parallelism only when a group is declared",
		}},
		{"docs/reference/commands.md", []string{
			"gate.py", "--parallel", "the parallel comparison's serial baseline",
		}},
		{"docs/concepts.md", []string{
			"parallel groups", "a repository that declares no group runs exactly as before",
		}},
		{"docs/reference/release.md", []string{
			"plan digest", "polyforge#244", "only when a group is declared",
		}},
		{"docs/contributing.md", []string{"group=", "full_parallel="}},
		{"workflows/integrate/README.md", []string{"parallel groups", "interleave"}},
		{"workflows/integrate/skills/full-gate.md", []string{"parallel groups", "interleave"}},
	} {
		text := flat(c.rel)
		for _, want := range c.wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s must carry %q (polyforge#244: the full gate's declared groups)", c.rel, want)
			}
		}
	}
}
