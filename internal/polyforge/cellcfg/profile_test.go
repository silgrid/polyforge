package cellcfg

import "testing"

func TestValidName(t *testing.T) {
	for _, ok := range []string{"default", "a", "alpha", "a-1", "0abc", "abcdefghijklmnopqrstuvwxyz012345"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "Alpha", "a_b", "a.b", "abcdefghijklmnopqrstuvwxyz0123456", "a b"} {
		if ValidName(bad) == nil {
			t.Errorf("ValidName(%q) = nil, want a refusal", bad)
		}
	}
	if ValidNamedProfile("default") == nil {
		t.Fatal("default must be reserved for a named Cell")
	}
}

func TestFileNames(t *testing.T) {
	if CellFileName("default") != "cli.toml" || CellFileName("alpha") != "cli-alpha.toml" {
		t.Fatalf("CellFileName: %q %q", CellFileName("default"), CellFileName("alpha"))
	}
	for base, want := range map[string]string{"cli.toml": "default", "cli-alpha.toml": "alpha", "cli-default.toml": "default"} {
		if got, ok := ProfileOfFile(base); !ok || got != want {
			t.Errorf("ProfileOfFile(%q) = %q,%v want %q", base, got, ok, want)
		}
	}
	for _, base := range []string{"cli-.toml", "other.toml", "cli-a.toml.bak"} {
		if _, ok := ProfileOfFile(base); ok {
			t.Errorf("ProfileOfFile(%q) answered a profile", base)
		}
	}
}

func TestKeyScopes(t *testing.T) {
	for _, k := range []struct{ section, name string }{{"", "daemon_socket"}, {"daemon", "max_concurrent_jobs"}, {"daemon", "harness_root"}, {"daemon", "operator_agent_dir"}, {"daemon", "max_concurrent_bars"}} {
		if !HostOnly(k.section, k.name) {
			t.Errorf("%v must be Host-only", k)
		}
	}
	for _, k := range []struct{ section, name string }{{"", "server"}, {"", "api_key"}, {"daemon", "cell_max_concurrent_jobs"}, {"model", "source"}, {"workspace", "/x"}} {
		if HostOnly(k.section, k.name) {
			t.Errorf("%v must be a Cell key", k)
		}
	}
}
