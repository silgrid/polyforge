package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctor_display_test.go: the harness checks — the retired Claude Code
// statusLine face, the pi package shapes, the retired
// chat state row, and the unchanged exit code.

// displayHome builds a scratch HOME: claudeSettings / piSettings are
// written when non-empty; "-" creates the harness dir without settings.
func displayHome(t *testing.T, claudeSettings, piSettings string) string {
	t.Helper()
	home := t.TempDir()
	for _, h := range []struct{ dir, body string }{
		{filepath.Join(home, ".claude"), claudeSettings},
		{filepath.Join(home, ".pi", "agent"), piSettings},
	} {
		if h.body == "" {
			continue
		}
		if err := os.MkdirAll(h.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if h.body != "-" {
			if err := os.WriteFile(filepath.Join(h.dir, "settings.json"), []byte(h.body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return home
}

// writeScript writes an executable script under dir.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func statusLineSettings(cmd string) string {
	b, _ := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": cmd}})
	return string(b)
}

// TestClaudeStatuslineCheck: a statusLine still running
// the retired `pf watch --statusline` — directly or through a script —
// warns with the removal; anything else passes.
func TestClaudeStatuslineCheck(t *testing.T) {
	scripts := t.TempDir()
	withPf := writeScript(t, scripts, "with-pf.sh", "#!/bin/sh\ninput=$(cat)\necho main\nprintf '%s' \"$input\" | pf watch --statusline --harness-session - 2>/dev/null\n")
	withoutPf := writeScript(t, scripts, "without-pf.sh", "#!/bin/sh\necho main\n# pf watch --statusline (commented out)\n")
	cases := []struct {
		name     string
		settings string
		status   string
		detail   string
	}{
		{"no ~/.claude", "", "skip", "not set up"},
		{"settings.json absent", "-", "pass", "no "},
		{"malformed settings.json", "{not json", "warn", "cannot tell"},
		{"no statusLine", `{"hooks":{}}`, "pass", "does not run"},
		{"statusLine runs pf directly", statusLineSettings("pf watch --statusline --harness-session -"), "warn", "remove that invocation"},
		{"statusLine runs pf quoted", statusLineSettings("bash -c 'pf watch --statusline --harness-session -'"), "warn", "retired"},
		{"statusLine script runs pf", statusLineSettings("bash " + withPf), "warn", withPf},
		{"statusLine script without pf", statusLineSettings("bash " + withoutPf), "pass", "does not run"},
		{"inline command without pf", statusLineSettings("echo hi"), "pass", "does not run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := claudeStatuslineCheck(displayHome(t, tc.settings, ""))
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("got %s %q, want %s carrying %q", c.Status, c.Detail, tc.status, tc.detail)
			}
		})
	}
}

func TestPiPackageCheck(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		status   string
		detail   string
	}{
		{"no pi", "", "skip", "not set up"},
		{"settings.json absent", "-", "warn", "re-run tools/install.sh"},
		{"malformed settings.json", "[1,", "warn", "cannot tell"},
		{"package not installed", `{"packages":["git:git@github.com:other/repo.git"]}`, "warn", "release/current"},
		{"package installed", `{"packages":["git:git@github.com:other/repo.git","git:git@github.com:silgrid/polyforge.git@v2.2.1"]}`, "pass", "silgrid/polyforge"},
		{"package installed (object entry)", `{"packages":[{"source":"https://github.com/silgrid/polyforge"}]}`, "pass", "silgrid/polyforge"},
		{"extension listed", `{"extensions":[{"x":1},"/src/polyforge/skills/polyforge/extensions/pf-chat.ts"]}`, "pass", "pf-chat.ts"},
		// polyforge#174: the local release checkout install.sh references
		{"local release package", `{"packages":["~/.local/share/polyforge/release/current"]}`, "pass", "release/current"},
		{"local release package missing", `{"packages":["/nonexistent/release/current"]}`, "warn", "re-run tools/install.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := displayHome(t, "", tc.settings)
			t.Setenv("POLYFORGE_DATA_DIR", "")
			layoutRelease(t, home)
			c := piPackageCheck(filepath.Join(home, ".pi", "agent"), home)
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("got %s %q, want %s carrying %q", c.Status, c.Detail, tc.status, tc.detail)
			}
		})
	}
}

// TestRetiredChatStateIsReportedAndPruned ( AC2): the
// chat-subscription leftovers in the data dir and the old watcher's
// runtime files are reported (warn, exit code unchanged) and
// --prune-chat-state deletes exactly them.
func TestRetiredChatStateIsReportedAndPruned(t *testing.T) {
	home := displayHome(t, "", "")
	data, rt := filepath.Join(home, "data"), filepath.Join(home, "rt", "polyforge")
	for _, p := range []string{
		filepath.Join(data, "chats.json"), filepath.Join(data, "chat-subs.json"),
		filepath.Join(data, "chat-status", "x"), filepath.Join(rt, "chat-chat_1.lock"),
		filepath.Join(rt, "chat-chat_1.status"), filepath.Join(rt, "session-ab.chat"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := []string{filepath.Join(data, "decisions.json"), filepath.Join(rt, "other.lock")}
	for _, p := range keep {
		_ = os.WriteFile(p, []byte("{}"), 0o600)
	}
	env := map[string]string{"HOME": home, "PI_CODING_AGENT_DIR": "", "POLYFORGE_DATA_DIR": data, "XDG_RUNTIME_DIR": filepath.Dir(rt)}
	code, stdout, doc := doctorStatus(t, env, "--json", "doctor", "--harness")
	if code != 0 {
		t.Fatalf("--harness exit %d: %s", code, stdout)
	}
	st, detail := doctorFind(t, doc, docChRetiredChat)
	if st != "warn" || !strings.Contains(detail, "6 file(s)") || !strings.Contains(detail, "--prune-chat-state") {
		t.Fatalf("report = %s %q", st, detail)
	}
	if code, _, _ := runPf(t, env, "doctor", "--harness", "--prune-chat-state"); code == 0 {
		t.Fatal("--prune-chat-state must not combine with the read-only --harness")
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor", "--prune-chat-state")
	if st, detail := doctorFind(t, doc, docChRetiredChat); st != "pass" || !strings.Contains(detail, "deleted 6") {
		t.Fatalf("prune = %s %q", st, detail)
	}
	if found, _ := retiredChatFiles(data, rt); len(found) != 0 {
		t.Fatalf("left behind: %v", found)
	}
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("a live file was deleted: %s", p)
		}
	}
	if st, _ := doctorFind(t, func() doctorDoc { _, _, d := doctorStatus(t, env, "--json", "doctor", "--harness"); return d }(), docChRetiredChat); st != "pass" {
		t.Fatalf("after the prune the row = %s", st)
	}
}

// TestDoctorDisplayGapKeepsExitCode pins that unwired harnesses warn
// without moving the exit code: --harness exits 0, and an otherwise
// healthy machine still passes the full doctor with exit 0.
func TestDoctorDisplayGapKeepsExitCode(t *testing.T) {
	home := displayHome(t, "{not json", `{"packages":[]}`)
	env := map[string]string{"HOME": home, "PI_CODING_AGENT_DIR": ""}
	code, stdout, doc := doctorStatus(t, env, "--json", "doctor", "--harness")
	if code != 0 || !doc.OK || len(doc.Checks) != 6 {
		t.Fatalf("--harness: exit %d ok %v checks %+v (output %s)", code, doc.OK, doc.Checks, stdout)
	}
	for _, name := range []string{docChClaudeStatusline, docChPiPackage} {
		if st, _ := doctorFind(t, doc, name); st != "warn" {
			t.Fatalf("%s = %s, want warn", name, st)
		}
	}

	// PI_CODING_AGENT_DIR overrides ~/.pi/agent.
	env["PI_CODING_AGENT_DIR"] = filepath.Join(t.TempDir(), "absent")
	_, _, doc = doctorStatus(t, env, "--json", "doctor", "--harness")
	if st, _ := doctorFind(t, doc, docChPiPackage); st != "skip" {
		t.Fatalf("PI_CODING_AGENT_DIR not honoured: pi check %s", st)
	}

	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	env = doctorServerConfig(t, srv.URL, "k_ok", liveDaemonSocket(t, "m-h"), "polyforge")
	env["HOME"], env["PI_CODING_AGENT_DIR"] = home, ""
	code, stdout, doc = doctorStatus(t, env, "--json", "doctor")
	if code != 0 || !doc.OK {
		t.Fatalf("healthy machine with unwired harnesses: exit %d ok %v (output %s)", code, doc.OK, stdout)
	}
	if st, _ := doctorFind(t, doc, docChClaudeStatusline); st != "warn" {
		t.Fatalf("claude statusline = %s, want warn", st)
	}
}
