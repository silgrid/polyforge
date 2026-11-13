package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// TestCellModelSourceDecidesExecution: a named Cell without a declaration
// or with an unusable one keeps its execution plane disabled with the
// reason; a usable absolute directory opens it; shared-default copies only
// the env block of the default Cell's claude settings into the Cell's own
// claude config dir.
func TestCellModelSourceDecidesExecution(t *testing.T) {
	root := t.TempDir()
	build := func(source string) Config {
		t.Helper()
		cfg, err := cellConfig(Config{}, cellBuildInput{profile: "alpha", hostRoot: root,
			file: cli.Config{Server: "http://hub-alpha:1", Mapping: &mapping.Plan{}, ModelSource: source}})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if cfg := build(""); !strings.Contains(cfg.ExecutionDisabled, "no model-credential source") {
		t.Fatalf("no declaration: %q", cfg.ExecutionDisabled)
	}
	if cfg := build("relative/dir"); !strings.Contains(cfg.ExecutionDisabled, "not an absolute") {
		t.Fatalf("a relative source: %q", cfg.ExecutionDisabled)
	}
	src := t.TempDir()
	if cfg := build(src); !strings.Contains(cfg.ExecutionDisabled, "no readable auth.json") || cfg.OperatorAgentDirExplicit {
		t.Fatalf("a source without auth.json: %q", cfg.ExecutionDisabled)
	}
	if err := os.WriteFile(filepath.Join(src, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg := build(src); cfg.ExecutionDisabled != "" || cfg.OperatorAgentDir != src || !cfg.OperatorAgentDirExplicit {
		t.Fatalf("a usable source: %q %q", cfg.ExecutionDisabled, cfg.OperatorAgentDir)
	}

	defClaude := filepath.Join(root, "cells", "default", "claude-config")
	if err := os.MkdirAll(defClaude, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defClaude, "settings.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"x"},"permissions":{"allow":["*"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := build(cli.ModelSourceShared)
	if cfg.ExecutionDisabled != "" {
		t.Fatalf("shared-default: %q", cfg.ExecutionDisabled)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "claude-config", "settings.json"))
	if err != nil || !strings.Contains(string(raw), "ANTHROPIC_BASE_URL") || strings.Contains(string(raw), "permissions") {
		t.Fatalf("only the env block may be shared: %v %s", err, raw)
	}
}
