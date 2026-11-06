package cli

import (
	"strings"
	"testing"
)

// TestW386CommandPathKey: [daemon] command_path — the PATH
// the daemon's in-process commands start with — parses as a quoted list of
// absolute directories, renders as written, refuses anything else naming
// the key, and is a live [daemon] key with a schema.
func TestW386CommandPathKey(t *testing.T) {
	s, err := parseDaemonTable("cli.toml", []daemonKV{{key: "command_path", val: `"/opt/go/bin:/usr/bin:/bin"`, line: 1}})
	if err != nil || s.CommandPath != "/opt/go/bin:/usr/bin:/bin" {
		t.Fatalf("parsed %+v, %v", s, err)
	}
	if got := daemonValue(s, "command_path"); got != "/opt/go/bin:/usr/bin:/bin" {
		t.Fatalf("rendered %q", got)
	}
	for _, bad := range []string{`""`, `"bin:/usr/bin"`, `"/usr/bin::/bin"`, `"~/go/bin"`, "/usr/bin"} {
		if _, err := parseDaemonTable("cli.toml", []daemonKV{{key: "command_path", val: bad, line: 1}}); err == nil || !strings.Contains(err.Error(), "command_path") {
			t.Errorf("command_path = %s must be refused naming the key: %v", bad, err)
		}
	}
	found := false
	for _, d := range configKeyDefs {
		if d.name == "command_path" {
			found = d.class == ConfigLive && d.kind == keyDaemon
		}
	}
	if !found || ConfigSchema("command_path") == nil {
		t.Fatal("command_path must be a live [daemon] key with a schema")
	}
}
