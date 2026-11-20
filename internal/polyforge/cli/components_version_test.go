// carries: internal/polyforge/cli/verbs_components.go
package cli

import (
	"github.com/silgrid/polyforge/internal/polyforge/components"
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentsVerbRejectsSeedVersionMismatch(t *testing.T) {
	root := componentsRoot(t)
	setWorkstationVars(t, root)
	seed := filepath.Join(t.TempDir(), "seed")
	componentsTree(t, seed, "v0.2.5", "v0.2.4")
	daemon, env := daemonForComponents(t)
	code, _, stderr := runPf(t, env, "components", "upgrade", "--version", "v0.2.6", "--seed-dir", seed)
	if code == 0 || !strings.Contains(stderr, "v0.2.6") || !strings.Contains(stderr, "v0.2.5") {
		t.Fatalf("必须拒绝版本不匹配并说明请求与种子版本：exit=%d %s", code, stderr)
	}
	target, err := components.CurrentTarget(root)
	if err != nil || target != "releases/v0.2.4" {
		t.Fatalf("拒绝不得改变 current：%s %v", target, err)
	}
	for _, request := range daemon.rec.all() {
		if request.Path == "/ops/drain" || request.Path == "/ops/restart" {
			t.Fatalf("版本拒绝不得影响 daemon：%+v", request)
		}
	}
}
