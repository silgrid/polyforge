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
		".polyforge/verify-scope.conf": "stack=npm\ne2e_runner=npm run test:e2e --\ne2e_start_threshold=20\n",
		"lib/app.js":                   "export const app = 1;\n",
	}
	for i := 0; i < suites; i++ {
		files[fmt.Sprintf("e2e/suite-%02d.mjs", i)] = "// carries: lib/app.js\nconsole.log('suite');\n"
	}
	return files
}

func TestW407LightE2EServerModeThreshold(t *testing.T) {
	for _, tc := range []struct {
		name   string
		suites int
		start  bool
	}{
		{name: "below threshold", suites: 2, start: false},
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
