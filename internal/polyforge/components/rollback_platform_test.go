// carries: internal/polyforge/components/rollback.go
package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 回退目标必须能在当前平台运行；拒绝时不设排空、不改变软链。
func TestComponentsRollbackRefusesPlatformMismatch(t *testing.T) {
	root := rollbackFixture(t)
	previous := filepath.Join(root, ReleasesDirname, "v0.2.4")
	manifest, err := ReadManifest(previous)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Platform = "linux/arm64"
	if err := WriteManifest(previous, manifest); err != nil {
		t.Fatal(err)
	}
	beforeCurrent, _ := os.Readlink(filepath.Join(root, CurrentLinkname))
	beforePrevious, _ := os.Readlink(filepath.Join(root, PreviousLinkname))
	face := &fakeFace{}
	err = runRollback(t, root, face, nil)
	if err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("必须拒绝平台不兼容的回退并说明 platform：%v", err)
	}
	afterCurrent, _ := os.Readlink(filepath.Join(root, CurrentLinkname))
	afterPrevious, _ := os.Readlink(filepath.Join(root, PreviousLinkname))
	if beforeCurrent != afterCurrent || beforePrevious != afterPrevious || len(face.calls) != 0 {
		t.Fatal("拒绝回退不得改变软链或调用 daemon")
	}
	status, err := ReadUpgradeStatus(root)
	if err != nil || status == nil || status.State != UpgradeStateFailed || !strings.Contains(status.Error, "platform") {
		t.Fatalf("状态必须记录拒绝原因：%+v %v", status, err)
	}
}
