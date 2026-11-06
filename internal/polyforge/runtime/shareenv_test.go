package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShareClaudeEnvCopiesOnlyEnv: the env block replaces the destination's,
// its other keys are kept, the source's other keys never travel, and a
// missing source writes nothing.
func TestShareClaudeEnvCopiesOnlyEnv(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	dst := filepath.Join(dir, "dst")
	if err := ShareClaudeEnv(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("a missing source must write nothing")
	}
	_ = os.MkdirAll(dst, 0o700)
	_ = os.WriteFile(filepath.Join(dst, "settings.json"), []byte(`{"env":{"OLD":"1"},"model":"m"}`), 0o600)
	_ = os.WriteFile(src, []byte(`{"env":{"NEW":"2"},"hooks":{"x":1}}`), 0o600)
	if err := ShareClaudeEnv(src, dst); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dst, "settings.json"))
	s := string(raw)
	if !strings.Contains(s, `"NEW"`) || strings.Contains(s, `"OLD"`) || !strings.Contains(s, `"model"`) || strings.Contains(s, "hooks") {
		t.Fatalf("settings = %s", s)
	}
}
