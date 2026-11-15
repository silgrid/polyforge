// carries: workflows/_shared/scripts/verify-scope, .polyforge/verify-scope.conf

package test

import (
	"fmt"
	"strings"
	"testing"
)

func w407Files(suites int) map[string]string {
	files := map[string]string{
		"package.json":                 `{"scripts":{"test:e2e":"node e2e/all.mjs"}}` + "\n",
		".polyforge/verify-scope.conf": "stack=npm\ne2e_runner=npm run test:e2e --\ne2e_start_threshold=20\ne2e_start_prefix=E2E_SERVER_MODE=start\n",
		"lib/app.js":                   "export const app = 1;\n",
	}
	for i := 0; i < suites; i++ {
		files[fmt.Sprintf("e2e/suite-%02d.mjs", i)] = "// carries: lib/app.js\nconsole.log('suite');\n"
	}
	return files
}

// w50PrefixFiles is w407's fixture with the threshold at 2 and an optional
// light-tier prefix declaration (the engine's selection-count prefix is
// the conf's to declare).
func w50PrefixFiles(suites int, prefix string) map[string]string {
	files := map[string]string{
		"package.json":                 `{"scripts":{"test:e2e":"node e2e/all.mjs"}}` + "\n",
		".polyforge/verify-scope.conf": "stack=npm\ne2e_runner=npm run test:e2e --\ne2e_start_threshold=2\n",
		"lib/app.js":                   "export const app = 1;\n",
	}
	if prefix != "" {
		files[".polyforge/verify-scope.conf"] += "e2e_start_prefix=" + prefix + "\n"
	}
	for i := 0; i < suites; i++ {
		files[fmt.Sprintf("e2e/suite-%02d.mjs", i)] = "// carries: lib/app.js\nconsole.log('suite');\n"
	}
	return files
}

// TestW50E2EStartPrefixKey pins the light-tier prefix as a declared key
// (polyforge#50 AC5): the value the engine puts before the e2e_runner=
// command when the selection exceeds e2e_start_threshold is the conf's
// e2e_start_prefix= declaration — a repository declaring no key gets no
// prefix — so no engine text names a repository's own environment
// variable.
func TestW50E2EStartPrefixKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		suites int
		prefix string
		want   string
	}{
		{name: "custom declared prefix is used verbatim", suites: 3, prefix: "W50_MODE=start", want: "W50_MODE=start "},
		{name: "no declared prefix, no prefix", suites: 3, prefix: "", want: ""},
		{name: "at threshold the prefix is not added", suites: 2, prefix: "W50_MODE=start", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := w25e2eFixture(t, w50PrefixFiles(tc.suites, tc.prefix))
			w25e2eWrite(t, dir, "lib/app.js", "export const app = 2;\n")
			w25e2eCommit(t, dir, "change app")
			r := w403Run(t, dir, "--light", "--base", base)
			var selected []string
			for _, c := range r.Commands {
				if c.Phase == "scoped" && strings.Contains(c.Cmd, "npm run test:e2e") {
					selected = append(selected, c.Cmd)
				}
			}
			if len(selected) != 1 {
				t.Fatalf("expected one selected e2e command, got %q", selected)
			}
			if got := selected[0]; !strings.HasPrefix(got, tc.want+"npm run test:e2e") {
				t.Fatalf("selected %d suites: want the command to start %q, got %q", tc.suites, tc.want+"npm run test:e2e", got)
			}
		})
	}
}

func TestW407LightE2EServerModeThreshold(t *testing.T) {
	// r1#6: retain the exact-threshold case so the strict comparison is pinned.
	for _, tc := range []struct {
		name   string
		suites int
		start  bool
	}{
		{name: "below threshold", suites: 2, start: false},
		{name: "at threshold", suites: 20, start: false},
		{name: "above threshold", suites: 21, start: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := w25e2eFixture(t, w407Files(tc.suites))
			w25e2eWrite(t, dir, "lib/app.js", "export const app = 2;\n")
			w25e2eCommit(t, dir, "change app")
			r := w403Run(t, dir, "--light", "--base", base)
			var selected []string
			for _, c := range r.Commands {
				if c.Phase == "scoped" && strings.Contains(c.Cmd, "npm run test:e2e") {
					selected = append(selected, c.Cmd)
				}
			}
			if len(selected) != 1 {
				t.Fatalf("expected one selected e2e command, got %q", selected)
			}
			hasStart := strings.HasPrefix(selected[0], "E2E_SERVER_MODE=start ")
			if hasStart != tc.start {
				t.Fatalf("selected %d suites: start mode=%v, command=%q", tc.suites, hasStart, selected[0])
			}
		})
	}
}
