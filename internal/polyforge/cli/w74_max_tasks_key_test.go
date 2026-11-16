package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// TestW74MaxConcurrentTasksKey (polyforge#74): [daemon]
// max_concurrent_tasks parses, renders and refuses like its Host-only
// live siblings, takes task.DefaultLimit as its one default source, is
// written unquoted by `pf config set`, and a named Cell's file refuses it
// as a Host-only key.
func TestW74MaxConcurrentTasksKey(t *testing.T) {
	s, err := parseDaemonTable("cli.toml", []daemonKV{{key: "max_concurrent_tasks", val: "3", line: 1}})
	if err != nil || s.MaxConcurrentTasks != 3 {
		t.Fatalf("parse: %+v %v", s, err)
	}
	if daemonValue(s, "max_concurrent_tasks") != "3" {
		t.Fatal("the set value must render")
	}
	if got, want := daemonDefault("max_concurrent_tasks"), strconv.Itoa(task.DefaultLimit); got != want {
		t.Fatalf("the default must render task.DefaultLimit (%s), got %q", want, got)
	}
	for _, bad := range []daemonKV{
		{key: "max_concurrent_tasks", val: "0"},
		{key: "max_concurrent_tasks", val: "-1"},
		{key: "max_concurrent_tasks", val: `"3"`},
	} {
		if _, err := parseDaemonTable("cli.toml", []daemonKV{bad}); err == nil || !strings.Contains(err.Error(), "max_concurrent_tasks") {
			t.Errorf("%s = %s must be refused naming the key: %v", bad.key, bad.val, err)
		}
	}
	found := false
	for _, d := range configKeyDefs {
		if d.name == "max_concurrent_tasks" {
			found = d.class == ConfigLive && d.kind == keyDaemon
		}
	}
	if !found {
		t.Error("max_concurrent_tasks must be a live [daemon] key of the key model")
	}
	if sch := ConfigSchema("max_concurrent_tasks"); sch == nil || sch["kind"] != "int" || sch["min"] != 1 {
		t.Errorf("schema = %v, want {kind:int,min:1}", sch)
	}
	// the editor writes the value unquoted, like its integer siblings
	k, err := parseConfigKey("max_concurrent_tasks")
	if err != nil {
		t.Fatal(err)
	}
	val, _, err := configSetValue(&Context{}, k, []string{"8"})
	if err != nil || val != "8" {
		t.Fatalf("configSetValue = %q, %v — want the bare integer 8", val, err)
	}
	// the Host/Cell partition: the Host file takes the key, a named
	// Cell's file refuses it as Host-only
	if _, err := ParseConfigBytes("/tmp/cli.toml", []byte("[daemon]\nmax_concurrent_tasks = 3\n")); err != nil {
		t.Fatalf("the Host file must take the key: %v", err)
	}
	_, err = ParseConfigBytes("/tmp/cli-alpha.toml", []byte("[daemon]\nmax_concurrent_tasks = 3\n"))
	if err == nil || !strings.Contains(err.Error(), "Host-only key") {
		t.Fatalf("a named Cell's file must refuse the Host-only key, got %v", err)
	}
}
