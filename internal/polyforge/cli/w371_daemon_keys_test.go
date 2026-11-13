package cli

import (
	"strings"
	"testing"
)

// TestW371DaemonKeys: max_concurrent_bars (int >= 1) and
// bar_load_threshold (a number > 0) parse, render and refuse like their
// siblings, and are live. verify_deadline left with the daemon's verify
// action (workflow contract v2 change 16): it is an unknown key now.
func TestW371DaemonKeys(t *testing.T) {
	s, err := parseDaemonTable("cli.toml", []daemonKV{
		{key: "max_concurrent_bars", val: "3", line: 1},
		{key: "bar_load_threshold", val: "7.5", line: 2},
	})
	if err != nil || s.MaxConcurrentBars != 3 || s.BarLoadThreshold != 7.5 {
		t.Fatalf("parsed %+v, %v", s, err)
	}
	if daemonValue(s, "bar_load_threshold") != "7.5" || daemonValue(s, "max_concurrent_bars") != "3" {
		t.Fatal("rendered values")
	}
	if daemonDefault("max_concurrent_bars") != "2" || daemonDefault("bar_load_threshold") == "" {
		t.Fatal("defaults")
	}
	if _, err := parseDaemonTable("cli.toml", []daemonKV{{key: "verify_deadline", val: `"90m"`, line: 1}}); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("verify_deadline must be refused as an unknown key: %v", err)
	}
	if ConfigSchema("verify_deadline") != nil {
		t.Error("verify_deadline has no schema any more")
	}
	for _, bad := range []daemonKV{
		{key: "max_concurrent_bars", val: "0"}, {key: "bar_load_threshold", val: "0"}, {key: "bar_load_threshold", val: `"8"`},
		{key: "bar_load_threshold", val: "-1"},
	} {
		if _, err := parseDaemonTable("cli.toml", []daemonKV{bad}); err == nil || !strings.Contains(err.Error(), bad.key) {
			t.Errorf("%s = %s must be refused naming the key: %v", bad.key, bad.val, err)
		}
	}
	for _, k := range []string{"max_concurrent_bars", "bar_load_threshold"} {
		found := false
		for _, d := range configKeyDefs {
			if d.name == k {
				found = d.class == ConfigLive && d.kind == keyDaemon
			}
		}
		if !found || ConfigSchema(k) == nil {
			t.Errorf("%s must be a live [daemon] key with a schema", k)
		}
	}
	if DefaultBarLoadThreshold() < 1 {
		t.Fatal("the default threshold is at least 1")
	}
}
