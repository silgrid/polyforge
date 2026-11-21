// carries: internal/polyforge/components/upgrade.go
package components

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestComponentsAIHubProcessHelper(t *testing.T) {
	if os.Getenv("PF_COMPONENTS_TEST_CHILD") != "1" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	if err := os.WriteFile(os.Getenv("PF_COMPONENTS_TEST_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	select {
	case <-signals:
		os.Exit(0)
	case <-time.After(10 * time.Second):
		os.Exit(3)
	}
}

func TestComponentsUpgradeRestartsAIHub(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, ReleasesDirname, "v0.2.4", "bin", "pf")
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(binary, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		t.Fatal(err)
	}
	output.Close()
	ready := filepath.Join(root, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestComponentsAIHubProcessHelper$", "--", "aihub")
	child.Env = append(os.Environ(), "PF_COMPONENTS_TEST_CHILD=1", "PF_COMPONENTS_TEST_READY="+ready)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	defer func() { _ = child.Process.Kill() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("AIHub 替身未就绪")
		}
		time.Sleep(time.Millisecond)
	}
	if err := termPiWebUnder(root); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AIHub 应优雅退出：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("升级必须重启旧版本 AIHub，不能只重启 daemon/Pi Web")
	}
}
