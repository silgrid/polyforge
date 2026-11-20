package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstBootResumeProjectConflict(t *testing.T) {
	for _, mode := range []string{"local", "external"} {
		t.Run(mode, func(t *testing.T) {
			o, _ := fbOpts(t)
			fbSeed(t, o)
			if mode == "external" {
				o.ExtServer, o.ExtKey, o.ExtProject = "https://first.example.invalid", "first-key", "first-project"
			}
			// 让首次启动在配置已落盘之后中断。
			if err := os.MkdirAll(filepath.Dir(o.AgentDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(o.AgentDir, []byte("blocked"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := runFirstBoot(o, &out); err == nil {
				t.Fatal("预置的 harness 故障必须中断首次启动")
			}
			if rec := fbRecord(t, o); rec.Phase != "harness/failed" {
				t.Fatalf("中断阶段 = %s", rec.Phase)
			}
			if err := os.Remove(o.AgentDir); err != nil {
				t.Fatal(err)
			}
			read := func(path string) []byte {
				t.Helper()
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			config, record := read(o.ConfigPath), read(firstBootPath(o.DataDir))
			o.ExtProject = "conflicting-project"
			if err := runFirstBoot(o, &out); err == nil {
				t.Fatal("冲突项目名的续跑必须拒绝")
			}
			if !bytes.Equal(config, read(o.ConfigPath)) || !bytes.Equal(record, read(firstBootPath(o.DataDir))) {
				t.Fatal("拒绝续跑不得改写配置或记录")
			}
			// 项目名可省略，省略时继续采用记录里的首次选择。
			o.ExtProject = ""
			if err := runFirstBoot(o, &out); err != nil {
				t.Fatalf("省略可选项目名应按记录续跑：%v", err)
			}
			cfg, err := LoadFileConfig(o.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			want := "workstation"
			if mode == "external" {
				want = "first-project"
			}
			if got := cfg.Mapping.ProjectsOf(o.WorkspaceDir); len(got) != 1 || got[0] != want {
				t.Fatalf("续跑后项目 = %v，期望 %s", got, want)
			}
		})
	}
}
