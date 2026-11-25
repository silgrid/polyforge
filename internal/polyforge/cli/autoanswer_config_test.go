package cli

// autoanswer_config_test.go: the two Cell/live configuration keys of the
// automatic reply (polyforge#239): ask_auto_reply_enabled (unquoted bool,
// unset = on, explicit false stays false) and ask_auto_reply_timeout (a
// quoted positive Go duration). The shared parser is the one acceptance
// path; a bad value refuses the whole file.

import (
	"testing"
	"time"
)

func TestAutoAnswerConfigKeys(t *testing.T) {
	// Unset: both fields absent (the daemon's documented default applies).
	cfg, err := ParseConfigBytes("cli.toml", []byte("[daemon]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.AskAutoReplyEnabled != nil || cfg.Daemon.AskAutoReplyTimeout != 0 {
		t.Fatalf("unset = %v / %v, want nil / 0", cfg.Daemon.AskAutoReplyEnabled, cfg.Daemon.AskAutoReplyTimeout)
	}

	// Explicit false is not unset; the timeout round-trips.
	cfg, err = ParseConfigBytes("cli.toml", []byte("[daemon]\nask_auto_reply_enabled = false\nask_auto_reply_timeout = \"45m\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.AskAutoReplyEnabled == nil || *cfg.Daemon.AskAutoReplyEnabled {
		t.Fatalf("explicit false = %v, want a false pointer", cfg.Daemon.AskAutoReplyEnabled)
	}
	if cfg.Daemon.AskAutoReplyTimeout != 45*time.Minute {
		t.Fatalf("timeout = %v, want 45m", cfg.Daemon.AskAutoReplyTimeout)
	}

	// The writer renders one unquoted bool and one quoted duration; both
	// re-parse through the same shared path.
	for _, tc := range []struct {
		key, val string
		check    func(Config) bool
	}{
		{"ask_auto_reply_enabled", "true", func(c Config) bool { return c.Daemon.AskAutoReplyEnabled != nil && *c.Daemon.AskAutoReplyEnabled }},
		{"ask_auto_reply_timeout", "30m", func(c Config) bool { return c.Daemon.AskAutoReplyTimeout == 30*time.Minute }},
	} {
		k, err := parseConfigKey(tc.key)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := configSetValue(&Context{}, k, []string{tc.val})
		if err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		c := newConfigLines("[daemon]\n")
		c.setDaemon(k.name, raw)
		got, err := ParseConfigBytes("cli.toml", []byte(c.String()))
		if err != nil {
			t.Fatalf("%s: written %q does not re-parse: %v", tc.key, c.String(), err)
		}
		if !tc.check(got) {
			t.Fatalf("%s: written %q did not read back", tc.key, c.String())
		}
	}

	// A bad value refuses the whole file (no half-applied config).
	for _, bad := range []string{
		"[daemon]\nask_auto_reply_enabled = \"true\"\n",
		"[daemon]\nask_auto_reply_enabled = yes\n",
		"[daemon]\nask_auto_reply_timeout = \"0s\"\n",
		"[daemon]\nask_auto_reply_timeout = \"-5m\"\n",
		"[daemon]\nask_auto_reply_timeout = \"soon\"\n",
	} {
		if _, err := ParseConfigBytes("cli.toml", []byte(bad)); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}

	for _, key := range []string{"ask_auto_reply_enabled", "ask_auto_reply_timeout"} {
		class, ok := ConfigClassOf(key)
		if !ok || class != ConfigLive {
			t.Fatalf("%s class = %v, %v; want live", key, class, ok)
		}
	}
}
