package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// doctor_display_test.go: the harness progress-display checks
// (polyforge#142) — the Claude Code statusLine shapes, the pi package
// shapes, the copyable append snippet, and the unchanged exit code.

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

func TestClaudeDisplayCheck(t *testing.T) {
	scripts := t.TempDir()
	withPf := writeScript(t, scripts, "with-pf.sh", "#!/bin/sh\ninput=$(cat)\necho main\nprintf '%s' \"$input\" | env -u PF_CHAT_ID pf watch --statusline --harness-session - 2>/dev/null\n")
	withoutPf := writeScript(t, scripts, "without-pf.sh", "#!/bin/sh\necho main\n# pf watch --statusline (commented out)\n")
	callsOther := writeScript(t, scripts, "calls-other.sh", "#!/bin/sh\necho main\n. \"$HOME/more.sh\"\n")
	cases := []struct {
		name     string
		settings string
		status   string
		detail   string // a token the detail must carry
	}{
		{"no ~/.claude", "", "skip", "not set up"},
		{"settings.json absent", "-", "warn", "will not show"},
		{"malformed settings.json", "{not json", "warn", "cannot tell"},
		{"statusLine of the wrong shape", `{"statusLine":"pf"}`, "warn", "cannot tell"},
		{"no statusLine", `{"hooks":{}}`, "warn", "will not show"},
		{"statusLine runs pf directly", statusLineSettings("pf watch --statusline --harness-session -"), "pass", "statusLine command"},
		{"statusLine runs pf quoted", statusLineSettings("bash -c 'pf watch --statusline --harness-session -'"), "pass", "statusLine command"},
		{"statusLine script runs pf", statusLineSettings("bash " + withPf), "pass", withPf},
		{"relative script", statusLineSettings("bash statusline.sh"), "warn", "cannot tell"},
		{"missing script", statusLineSettings("bash " + filepath.Join(scripts, "gone.sh")), "warn", "cannot tell"},
		{"script calling another script", statusLineSettings("bash " + callsOther), "warn", "cannot tell"},
		{"statusLine script without pf", statusLineSettings("bash " + withoutPf), "warn", "append"},
		{"inline command without pf", statusLineSettings("echo hi"), "warn", "append"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := claudeDisplayCheck(displayHome(t, tc.settings, ""))
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("got %s %q, want %s carrying %q", c.Status, c.Detail, tc.status, tc.detail)
			}
		})
	}
}

// snippetStatusLine extracts the `"statusLine": {...}` object a warn
// detail ends with.
func snippetStatusLine(t *testing.T, detail string) map[string]any {
	t.Helper()
	i := strings.Index(detail, `"statusLine": `)
	if i < 0 {
		t.Fatalf("no statusLine snippet in %q", detail)
	}
	var sl map[string]any
	if err := json.Unmarshal([]byte(detail[i+len(`"statusLine": `):]), &sl); err != nil {
		t.Fatalf("snippet does not parse: %v (%q)", err, detail)
	}
	return sl
}

// TestClaudeDisplayAppendSnippetRuns pins that the copied snippet works:
// it parses as JSON, keeps the user's statusLine fields, and run as the
// statusLine command prints the user's own non-empty output unchanged
// followed by the progress rows, both fed the same session JSON — also
// for a user command ending in `;`, `&` or a `# comment`.
func TestClaudeDisplayAppendSnippetRuns(t *testing.T) {
	bin := t.TempDir()
	writeScript(t, bin, "pf", "#!/bin/sh\necho \"pf $*: $(cat)\"\n")
	user := writeScript(t, t.TempDir(), "user.sh", "#!/bin/sh\nprintf 'user: %s' \"$(cat)\"\n")
	const in = `{"session_id":"s1"}`
	pfRow := "pf watch --statusline --harness-session -: " + in + "\n"
	cases := []struct{ cmd, want string }{
		{"sh " + user + ";", "user: " + in + "\n" + pfRow},
		{"sh " + user + " # the user's comment", "user: " + in + "\n" + pfRow},
		{"echo hi &", "hi\n" + pfRow},
		{"true", pfRow},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": tc.cmd, "refreshInterval": 5}})
			c := claudeDisplayCheck(displayHome(t, string(b), ""))
			if c.Status != "warn" {
				t.Fatalf("got %s %q, want warn", c.Status, c.Detail)
			}
			sl := snippetStatusLine(t, c.Detail)
			if sl["type"] != "command" || sl["refreshInterval"] != float64(5) {
				t.Fatalf("snippet dropped the user's statusLine fields: %v", sl)
			}
			cmd := exec.Command("sh", "-c", sl["command"].(string))
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			cmd.Stdin = strings.NewReader(in)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("snippet command failed: %v (%s)", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("snippet output %q, want %q", out, tc.want)
			}
			// Re-judged with the snippet in place, the statusLine now shows.
			b, _ = json.Marshal(map[string]any{"statusLine": sl})
			if c := claudeDisplayCheck(displayHome(t, string(b), "")); c.Status != "pass" {
				t.Fatalf("snippet re-judged: %s %q, want pass", c.Status, c.Detail)
			}
		})
	}
}

func TestPiDisplayCheck(t *testing.T) {
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
			c := piDisplayCheck(filepath.Join(home, ".pi", "agent"), home)
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("got %s %q, want %s carrying %q", c.Status, c.Detail, tc.status, tc.detail)
			}
		})
	}
}

// TestDoctorDisplayGapKeepsExitCode pins that unwired harnesses warn
// without moving the exit code: --harness exits 0, and an otherwise
// healthy machine still passes the full doctor with exit 0.
func TestDoctorDisplayGapKeepsExitCode(t *testing.T) {
	home := displayHome(t, "{not json", `{"packages":[]}`)
	env := map[string]string{"HOME": home, "PI_CODING_AGENT_DIR": ""}
	code, stdout, doc := doctorStatus(t, env, "--json", "doctor", "--harness")
	if code != 0 || !doc.OK || len(doc.Checks) != 5 {
		t.Fatalf("--harness: exit %d ok %v checks %+v (output %s)", code, doc.OK, doc.Checks, stdout)
	}
	for _, name := range []string{docChClaudeDisplay, docChPiDisplay} {
		if st, _ := doctorFind(t, doc, name); st != "warn" {
			t.Fatalf("%s = %s, want warn", name, st)
		}
	}

	// PI_CODING_AGENT_DIR overrides ~/.pi/agent.
	env["PI_CODING_AGENT_DIR"] = filepath.Join(t.TempDir(), "absent")
	_, _, doc = doctorStatus(t, env, "--json", "doctor", "--harness")
	if st, _ := doctorFind(t, doc, docChPiDisplay); st != "skip" {
		t.Fatalf("PI_CODING_AGENT_DIR not honoured: pi check %s", st)
	}

	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	env = doctorServerConfig(t, srv.URL, "k_ok", liveDaemonSocket(t, "m-h"), "polyforge")
	env["HOME"], env["PI_CODING_AGENT_DIR"] = home, ""
	code, stdout, doc = doctorStatus(t, env, "--json", "doctor")
	if code != 0 || !doc.OK {
		t.Fatalf("healthy machine with unwired harnesses: exit %d ok %v (output %s)", code, doc.OK, stdout)
	}
	if st, _ := doctorFind(t, doc, docChClaudeDisplay); st != "warn" {
		t.Fatalf("claude display = %s, want warn", st)
	}
}
