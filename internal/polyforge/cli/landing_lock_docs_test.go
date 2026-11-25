package cli

// landing_lock_docs_test.go: AC3's document parity (polyforge#249). The
// queue/watch reference and the command's verb skills must describe the
// same read-only projection the code serves: the 等待合入锁 wording, the
// 1-based position, the stale/unavailable states, the privacy boundary and
// the `landing_lock` field name. This is a text contract because AC3 is the
// documents themselves.

import (
	"os"
	"strings"
	"testing"
)

func TestLandingLockDocsParity(t *testing.T) {
	files := map[string][]string{
		"../../../docs/reference/commands.md": {
			"等待合入锁", "landing_lock", "position", "stale", "unavailable", "持有者"},
		"../../../skills/polyforge/verbs/queue.md": {
			"等待合入锁", "LANDING", "position", "stale", "unavailable"},
		"../../../skills/polyforge/verbs/watch.md": {
			"等待合入锁", "landing_lock", "position", "stale", "unavailable"},
		"../../../docs/design/polyforge-vnext/reference.md": {
			"landing_lock", "等待合入锁", "position", "stale", "unavailable", "holder"},
	}
	for path, terms := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, term := range terms {
			if !strings.Contains(string(raw), term) {
				t.Errorf("%s lacks %q", path, term)
			}
		}
	}
}
