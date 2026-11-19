// carries: .polyforge/verify-scope.conf, workflows/_shared/scripts/components.py

package test

// pf89_repo_components_test.go (polyforge#89, AC5): this repository
// declares its own components in .polyforge/verify-scope.conf — the
// calibration data the epic-upgrade rules need: a generic division
// would fold the hub, the daemon and the CLI into one "internal"
// component, and the tickets those rules were calibrated on
// (polyforge#25, #30) crossed them. The tool reports the declared
// components with their ranges (source declared), and the engine —
// which reads none of the keys — reports no diagnostic for the lines
// (the not_runnable_rule reader split, AC1).
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestPF89RepoDeclaresItsComponents$' -count=1

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pf89Declared is the component set the repository declares: the
// ticket's hub / daemon / CLI / Pi Web / console / lane / skill, with
// internal/common its own component (the owner's ruling of
// 2026-11-17: it is where the cross-component contracts live, so a
// change there counts as cross-component) and test/ and deploy/ left to
// the generic division. The CLI's range is the longer prefix that
// carves it out of the daemon's. dist/ is its own component too
// (polyforge#161, the owner's settled Q8: the workstation image lives
// in a new top-level dist/ and is registered in the component
// declaration, not folded into the generic division).
var pf89Declared = map[string][]string{
	"hub":       {"internal/aihub"},
	"daemon":    {"internal/polyforge"},
	"cli":       {"internal/polyforge/cli", "cmd"},
	"common":    {"internal/common"},
	"pi-web":    {"pi-web"},
	"webui":     {"webui"},
	"workflows": {"workflows"},
	"skills":    {"skills"},
	"dist":      {"dist"},
}

func TestPF89RepoDeclaresItsComponents(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed: the component tool needs it")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := w25e2eRoot(t)
	// The tool reads the HEAD commit tree (--rev HEAD: this change's
	// branch is not what origin/HEAD names yet).
	cmd := exec.Command("python3",
		filepath.Join(root, "workflows", "_shared", "scripts", "components.py"),
		"--repo", root, "--rev", "HEAD", "--json")
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("components.py --repo <this repository> --rev HEAD: %v\n%s", err, errb.String())
	}
	var doc struct {
		Repos []struct {
			Components []struct {
				Name   string   `json:"name"`
				Paths  []string `json:"paths"`
				Source string   `json:"source"`
			} `json:"components"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
		t.Fatalf("components.py's --json report does not decode: %v\n%s", err, out.String())
	}
	if len(doc.Repos) != 1 {
		t.Fatalf("one --repo answers one repository entry: %s", out.String())
	}
	declared := map[string][]string{}
	for _, c := range doc.Repos[0].Components {
		if c.Source == "declared" {
			declared[c.Name] = c.Paths
		}
	}
	if len(declared) != len(pf89Declared) {
		t.Fatalf("the repository declares %d components, want the %d of the ticket's set:\n%s",
			len(declared), len(pf89Declared), out.String())
	}
	for name, paths := range pf89Declared {
		got, ok := declared[name]
		if !ok {
			t.Errorf("the repository must declare the %s component (source declared):\n%s", name, out.String())
			continue
		}
		if strings.Join(got, ",") != strings.Join(paths, ",") {
			t.Errorf("the %s component's range is %v, want %v", name, got, paths)
		}
	}

	// The engine reports no diagnostic for the declaration's lines: this
	// repository's own conf, read by the engine on a fixture that changes
	// nothing else (the W50 reader-split shape).
	conf, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repository conf: %v", err)
	}
	dir, base := w25e2eFixture(t, map[string]string{
		".polyforge/verify-scope.conf": string(conf),
		"a.txt":                        "a\n",
	})
	_, stderr, code := w25e2eRun(t, dir, nil, "--json", "--base", base)
	if code != 0 {
		t.Fatalf("the engine exited %d: %s", code, stderr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "component") || strings.Contains(line, "epic_") {
			t.Fatalf("the engine must not diagnose the component tool's keys: %s", line)
		}
	}
}
