// carries: tools/clean-summarize-sessions
package test

// tools/clean-summarize-sessions finds the sessions the
// summarize dispatches left behind by their first user message — the
// summarize prompt's opening — not by the directory they sit in. It lists
// them first and deletes them only with --delete: one-off sessions sharing
// the summarize-cwd directory, a user's own directory whose name ends in
// summarize-cwd, and every other session stay; early summarize sessions
// mixed into other session directories are found. pi's sessions live in
// $HOME/.pi/agent/sessions/<dir>/, the Cells' claude sessions in
// <data-dir>/cells/<profile>/claude-config/projects/<dir>/, and a custom
// harness root is scanned only when passed with --harness-root. The test
// runs the real script over session fixtures in a scratch HOME.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const w523SummarizePrompt = "Summarize in ONE short line what this coding-agent work item is doing right now: concrete current activity, plain text, no markdown. Answer in English. Transcript tail (oldest first):\nstep build started\n"

func w523JSONL(t *testing.T, recs ...any) string {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		if s, ok := r.(string); ok { // a raw (possibly unparsable) line
			b.WriteString(s + "\n")
			continue
		}
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func w523PiSession() map[string]any {
	return map[string]any{"type": "session", "version": 3, "id": "s", "timestamp": "2026-11-01T00:00:00Z", "cwd": "/root"}
}

func w523PiUser(content any) map[string]any {
	return map[string]any{"type": "message", "id": "m", "message": map[string]any{"role": "user", "content": content}}
}

func w523PiAssistant(text string) map[string]any {
	return map[string]any{"type": "message", "id": "a", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}}
}

func w523ClaudeUser(content any) map[string]any {
	return map[string]any{"type": "user", "sessionId": "s", "message": map[string]any{"role": "user", "content": content}}
}

func w523TextArray(text string) []any {
	return []any{map[string]any{"type": "text", "text": text}}
}

func w523RunCleanup(t *testing.T, home string, args ...string) (int, string) {
	t.Helper()
	script := filepath.Join(v1Root(t), "tools", "clean-summarize-sessions")
	cmd := exec.Command("python3", append([]string{"-I", script}, args...)...)
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "POLYFORGE_DATA_DIR=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "HOME="+home, "POLYFORGE_DATA_DIR="+filepath.Join(home, "data"))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

func w523Listed(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "summarize session: "); ok {
			got = append(got, p)
		}
	}
	sort.Strings(got)
	return got
}

func w523LastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestW523SummarizeSessionCleanup(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is an install prerequisite and runs this tool: %v", err)
	}
	home := t.TempDir()
	pi := filepath.Join(home, ".pi", "agent", "sessions")
	cells := filepath.Join(home, "data", "cells")
	hr := filepath.Join(home, "hr")
	other := "Analyze this user message and answer with one word."

	files := map[string]string{}   // path → content
	summarize := map[string]bool{} // the paths that must be found
	add := func(path string, match bool, content string) {
		files[path] = content
		if match {
			summarize[path] = true
		}
	}

	// pi: the summarize-cwd directory holds two summarize sessions (string
	// and text-array content) and a one-off session that is not one.
	scwd := filepath.Join(pi, "--root-.local-share-polyforge-summarize-cwd--")
	add(filepath.Join(scwd, "2026-11-01T00-00-00-000Z_a.jsonl"), true,
		w523JSONL(t, w523PiSession(), w523PiUser(w523SummarizePrompt), w523PiAssistant("Building the unit.")))
	add(filepath.Join(scwd, "2026-11-01T00-00-01-000Z_b.jsonl"), true,
		w523JSONL(t, w523PiSession(), "{not json", w523PiUser(w523TextArray(w523SummarizePrompt))))
	add(filepath.Join(scwd, "2026-11-01T00-00-02-000Z_c.jsonl"), false,
		w523JSONL(t, w523PiSession(), w523PiUser(other), w523PiAssistant("ok")))

	// pi: early summarize sessions mixed into other session directories.
	rootDir := filepath.Join(pi, "--root--")
	add(filepath.Join(rootDir, "2026-10-01T00-00-00-000Z_early.jsonl"), true,
		w523JSONL(t, w523PiSession(), w523PiUser(w523SummarizePrompt)))
	add(filepath.Join(rootDir, "2026-10-01T00-00-01-000Z_work.jsonl"), false,
		w523JSONL(t, w523PiSession(), w523PiUser("fix the failing test"), w523PiAssistant("done")))
	proj := filepath.Join(pi, "--home-u-proj--")
	add(filepath.Join(proj, "2026-10-02T00-00-00-000Z_early.jsonl"), true,
		w523JSONL(t, w523PiSession(), w523PiAssistant("hello"), w523PiUser(w523SummarizePrompt)))
	// the prefix in a later user message, or inside (not at the start of)
	// the first one: not a summarize session.
	add(filepath.Join(proj, "2026-10-02T00-00-01-000Z_later.jsonl"), false,
		w523JSONL(t, w523PiSession(), w523PiUser("look at this"), w523PiAssistant("ok"), w523PiUser(w523SummarizePrompt)))
	add(filepath.Join(proj, "2026-10-02T00-00-02-000Z_inside.jsonl"), false,
		w523JSONL(t, w523PiSession(), w523PiUser(w523TextArray("Quoting the daemon: "+w523SummarizePrompt))))
	add(filepath.Join(proj, "notes.txt"), false, w523SummarizePrompt)

	// pi: the user's own directory whose name ends in summarize-cwd.
	add(filepath.Join(pi, "--home-u-my-summarize-cwd--", "2026-10-03T00-00-00-000Z_mine.jsonl"), false,
		w523JSONL(t, w523PiSession(), w523PiUser("plan the summarize-cwd cleanup")))

	// claude: the default Cell's summarize-cwd project and another Cell's
	// workspace project.
	cdef := filepath.Join(cells, "default", "claude-config", "projects", "-root-.local-share-polyforge-cells-default-summarize-cwd")
	add(filepath.Join(cdef, "s1.jsonl"), true,
		w523JSONL(t, w523ClaudeUser(w523SummarizePrompt), map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": w523TextArray("ok")}}))
	add(filepath.Join(cdef, "n1.jsonl"), false,
		w523JSONL(t, w523ClaudeUser("review the plan")))
	cwork := filepath.Join(cells, "work", "claude-config", "projects", "-home-u-proj")
	meta := w523ClaudeUser("<local-command-caveat>Caveat</local-command-caveat>")
	meta["isMeta"] = true
	add(filepath.Join(cwork, "s2.jsonl"), true,
		w523JSONL(t, map[string]any{"type": "queue-operation", "operation": "dequeue"}, meta, w523ClaudeUser(w523TextArray(w523SummarizePrompt))))
	add(filepath.Join(cwork, "n2.jsonl"), false,
		w523JSONL(t, map[string]any{"type": "summary", "summary": w523SummarizePrompt}, w523ClaudeUser(w523TextArray("explain this function"))))

	// a custom harness root: scanned only when passed.
	hrSession := filepath.Join(hr, "cells", "default", "claude-config", "projects", "x", "s3.jsonl")
	add(hrSession, false, w523JSONL(t, w523ClaudeUser(w523SummarizePrompt)))

	for p, c := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var dirs []string
	for p := range files {
		dirs = append(dirs, filepath.Dir(p))
	}

	want := func(withHR bool) []string {
		var w []string
		for p := range summarize {
			w = append(w, p)
		}
		if withHR {
			w = append(w, hrSession)
		}
		sort.Strings(w)
		return w
	}
	assertAllPresent := func(stage string) {
		t.Helper()
		for p, c := range files {
			got, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(got, []byte(c)) {
				t.Fatalf("%s: %s changed or removed (err %v)", stage, p, err)
			}
		}
	}

	// 1. the default run lists and deletes nothing.
	code, out := w523RunCleanup(t, home)
	if code != 0 {
		t.Fatalf("default run: exit %d, want 0\n%s", code, out)
	}
	if got, w := w523Listed(out), want(false); strings.Join(got, "\n") != strings.Join(w, "\n") {
		t.Fatalf("default run listed\n%s\nwant\n%s\noutput:\n%s", strings.Join(got, "\n"), strings.Join(w, "\n"), out)
	}
	if last := w523LastLine(out); last != "6 summarize session(s) found; nothing deleted (re-run with --delete to delete them)" {
		t.Fatalf("default run last line %q", last)
	}
	assertAllPresent("default run")

	// 2. a custom harness root is scanned when passed.
	code, out = w523RunCleanup(t, home, "--harness-root", hr)
	if code != 0 {
		t.Fatalf("--harness-root run: exit %d\n%s", code, out)
	}
	if got, w := w523Listed(out), want(true); strings.Join(got, "\n") != strings.Join(w, "\n") {
		t.Fatalf("--harness-root run listed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(w, "\n"))
	}
	assertAllPresent("--harness-root run")

	// 3. a bad argument: exit 2, nothing deleted.
	if code, out = w523RunCleanup(t, home, "--delete", "--no-such-flag"); code != 2 {
		t.Fatalf("bad argument: exit %d, want 2\n%s", code, out)
	}
	assertAllPresent("bad argument")

	// 4. --delete lists, then deletes exactly the summarize sessions.
	code, out = w523RunCleanup(t, home, "--harness-root", hr, "--delete")
	if code != 0 {
		t.Fatalf("--delete run: exit %d\n%s", code, out)
	}
	if got, w := w523Listed(out), want(true); strings.Join(got, "\n") != strings.Join(w, "\n") {
		t.Fatalf("--delete run listed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(w, "\n"))
	}
	if last := w523LastLine(out); last != "deleted 7 summarize session(s)" {
		t.Fatalf("--delete run last line %q", last)
	}
	for p, c := range files {
		got, err := os.ReadFile(p)
		if summarize[p] || p == hrSession {
			if !os.IsNotExist(err) {
				t.Fatalf("summarize session %s still present (err %v)", p, err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, []byte(c)) {
			t.Fatalf("%s changed or removed by --delete (err %v)", p, err)
		}
	}
	for _, d := range dirs {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Fatalf("directory %s removed by --delete (err %v)", d, err)
		}
	}
}
