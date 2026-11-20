// carries: dist/workstation/smoke.sh, dist/workstation/build-seed.sh, dist/workstation/upgrade-smoke.sh
package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 脚本行为通过替身引擎执行；实际镜像与进程版本由 verification 的同一 smoke 证明。
func TestWorkstationUpgradeSmoke(t *testing.T) {
	dir := t.TempDir()
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$UPGRADE_LOG"
case "$1" in
 version) echo 29;;
 build) :;;
 create) echo seed-id;;
 cp) mkdir -p "$3/releases/v0.2.4-dev.1";;
 rm) :;;
 inspect) echo container-stable;;
 compose)
  case "$*" in *"ps -q"*) echo container-stable;; esac;;
 exec)
  case "$*" in
   *"/proc/1/stat"*) echo '1 (catatonit) 0 12345';;
   *"upgrade --version"*) :;;
   *"readlink /components/current"*) echo releases/v0.2.4-dev.2;;
   *"readlink /components/previous"*) echo releases/v0.2.4-dev.1;;
   *"pf version"*) echo v0.2.4-dev.2;;
   *"upgrade-status.json"*) echo done;;
  esac;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte("#!/bin/sh\necho 200\n"), 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.log")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "dist/workstation/smoke.sh", "upgrade")
	cmd.Dir = w535Root(t)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "UPGRADE_LOG="+log)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade smoke 必须完成：%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "upgrade: PASS") {
		t.Fatalf("缺少成功证明：%s", out)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"components upgrade --version v0.2.4-dev.2", "readlink /components/current", "readlink /components/previous", "/proc/1/stat", "pf version", "/status", "aihub", "upgrade-status.json"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("smoke 缺少观测 %q", want)
		}
	}
}
