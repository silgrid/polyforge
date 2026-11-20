// carries: dist/workstation/*, docs/workstation.md, test/w535*
package test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/testenv"
)

func TestWorkstationSmokeResourceLock(t *testing.T) {
	if probe := os.Getenv("W535_LOCK_PROBE"); probe != "" {
		// 独立进程、不同 TMPDIR，仍应观察同一个内核文件锁。
		f, err := os.OpenFile(filepath.Join(testenv.OrigTempDir(), "polyforge-workstation-smoke.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		if probe == "busy" {
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				t.Fatalf("持锁时不可取得同一资源锁，实际错误：%v", err)
			}
		} else if err != nil {
			t.Fatalf("释放后应可取得资源锁：%v", err)
		}
		return
	}
	// 使用私有公共根，避免本测试的探针对并行真机测试造成干扰。
	t.Setenv("TESTENV_ORIG_TMPDIR", t.TempDir())
	probe := func(want string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkstationSmokeResourceLock$", "-test.v")
		cmd.Env = append(os.Environ(), "W535_LOCK_PROBE="+want, "TMPDIR="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("跨进程资源锁探针 %s 失败：%v\n%s", want, err, out)
		}
	}
	unlock := w535LockSmoke(t)
	// 断言失败时也释放锁，不给后续测试留下持锁进程。
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	probe("busy")
	unlock()
	unlock = nil
	probe("free")
}

// w535LockSmoke 的实现由 u17 在红测试之后补上。
func w535LockSmoke(t *testing.T) func() {
	return func() {}
}
