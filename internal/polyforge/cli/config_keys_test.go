package cli

import "testing"

func TestConfigKeyParsing(t *testing.T) {
	cases := []struct {
		arg   string
		kind  configKeyKind
		name  string
		class ConfigClass
	}{
		{"server", keyFlat, "server", ConfigRestartOnly},
		{"claude_bin", keyFlat, "claude_bin", ConfigLive},
		{"user_id", keyFlat, "user_id", ConfigCLIOnly},
		{"max_concurrent_jobs", keyDaemon, "max_concurrent_jobs", ConfigLive},
		{"[daemon] step_timeout", keyDaemon, "step_timeout", ConfigLive},
		{`[workspace."/srv/ws"]`, keyWorkspace, "/srv/ws", ConfigNextClaim},
	}
	for _, c := range cases {
		k, err := parseConfigKey(c.arg)
		if err != nil {
			t.Fatalf("parseConfigKey(%q): %v", c.arg, err)
		}
		if k.kind != c.kind || k.name != c.name || classOfKey(k) != c.class {
			t.Fatalf("parseConfigKey(%q) = %+v, want kind %d name %q class %q", c.arg, k, c.kind, c.name, c.class)
		}
	}
	for _, bad := range []string{"", "nope", "[daemon]", "[other]"} {
		if _, err := parseConfigKey(bad); err == nil {
			t.Fatalf("parseConfigKey(%q) must refuse", bad)
		}
	}
}
