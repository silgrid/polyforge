package cli

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// TestW478ContainerKeysAreUnknown: the [daemon] verify_container_* keys
// left with the Go step commands (the scripts take their container limits
// as flags, design r2 D7): each is an unknown key — a cli.toml that still
// sets one is refused naming it — with no schema, no row in the key model
// and none in the Cell config schema.
func TestW478ContainerKeysAreUnknown(t *testing.T) {
	for _, kv := range []daemonKV{
		{key: "verify_container_cpus", val: "8", line: 1},
		{key: "verify_container_memory", val: `"8g"`, line: 1},
		{key: "verify_container_pids", val: "4096", line: 1},
	} {
		_, err := parseDaemonTable("cli.toml", []daemonKV{kv})
		if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), kv.key) {
			t.Errorf("%s must be refused as an unknown key naming it: %v", kv.key, err)
		}
		if ConfigSchema(kv.key) != nil {
			t.Errorf("%s has no schema any more", kv.key)
		}
		for _, d := range configKeyDefs {
			if d.name == kv.key {
				t.Errorf("%s is still in the key model", kv.key)
			}
		}
		if _, ok := cellcfg.Lookup("daemon", kv.key); ok {
			t.Errorf("%s is still in the Cell config schema", kv.key)
		}
	}
}
