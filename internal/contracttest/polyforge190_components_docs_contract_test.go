// carries: docs/upgrade.md, docs/reference/commands.md, docs/workstation.md, skills/polyforge/verbs/components.md, skills/polyforge/verbs/ops.md
package contracttest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentsUpgradeDocs(t *testing.T) {
	root := filepath.Join("..", "..")
	checks := map[string][]string{
		"docs/reference/commands.md":           {"pf components upgrade --version V", "upgrade-status.json", "退出码 75"},
		"skills/polyforge/verbs/components.md": {"check", "status", "upgrade", "rollback", "cancel", "DAEMON-409"},
		"docs/upgrade.md":                      {"polyforge#190", "SHA256SUMS", "harness", "Podman", "previous"},
		"docs/workstation.md":                  {"pf components", "upgrade-status.json"},
	}
	for file, words := range checks {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Errorf("读取 %s: %v", file, err)
			continue
		}
		for _, word := range words {
			if !strings.Contains(string(body), word) {
				t.Errorf("%s 缺少约定 %q", file, word)
			}
		}
	}
}
