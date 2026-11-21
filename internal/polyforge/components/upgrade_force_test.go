// carries: internal/polyforge/components/upgrade.go, internal/polyforge/components/status.go, internal/polyforge/cli/verbs_components.go, pi-web/components/ComponentsUpdateCard.tsx
package components

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComponentsUpgradeDrainForceContinuesSameTransaction(t *testing.T) {
	root, seed := upgradeFixture(t)
	face := &fakeFace{busy: [][]string{{"default"}}}
	face.onDrain = func(n int, f *fakeFace) {
		if n == 0 {
			if err := os.WriteFile(filepath.Join(root, "upgrade-force"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 定向命令自身必须有界，旧实现一直等待，测试上下文主动结束后断言失败。
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := Upgrade(ctx, UpgradeOptions{Root: root, SeedDir: seed, DataDir: t.TempDir(), BaseVersion: "v0.2.4", Platform: "linux/amd64", Face: face, Poll: time.Millisecond, RestartPiWeb: func() error { return nil }})
	if err != nil {
		t.Fatalf("强制请求必须让同一事务继续：%v", err)
	}
	if face.restartCalls() != 1 {
		t.Fatalf("一次强制只能完成一次切换重启：%d", face.restartCalls())
	}
}
