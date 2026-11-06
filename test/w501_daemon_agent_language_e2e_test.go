package test

// w501_daemon_agent_language_e2e_test.go: the real `pf
// daemon` process hands the configured agent language to its new
// dispatches. The daemon boots on a cli.toml carrying `[daemon]
// agent_language = "zh-CN"`; its first work item's dispatches record
// "zh-CN" in the spawn journal and carry the zh-CN sentence in the
// prompt. After the file says "en" and `pf config reload` runs, the next
// work item's dispatches record "en" and carry the English sentence.
// This pins the daemon's own wiring: the startup seed of the live value
// and the dispatch-time read the driver is handed.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	w501SentenceZhCN = "Language: Simplified Chinese (zh-CN)."
	w501SentenceEN   = "Language: English."
)

// w501Config writes the daemon's cli.toml (the harness mapping plus the
// [daemon] agent_language key), pins it as the daemon's --config before
// start, and runs `pf config reload` on a running daemon.
func w501Config(t *testing.T, d *v1Daemon, lang string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(d.logPath), "w501-cli.toml")
	body := "[workspace.\"" + d.workspace + "\"]\nprojects = [\"" + v1Project + "\"]\n\n[daemon]\nagent_language = \"" + lang + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if d.cmd == nil {
		d.extraArgs = append(d.extraArgs, "--config", path)
		return
	}
	d.pf(t, "config", "reload")
}

// w501RunOne submits one vehicle-lane work item, waits for it to settle
// and asserts every dispatch recorded lang and its prompt carries sentence.
func w501RunOne(t *testing.T, inst *v1Install, d *v1Daemon, goal, lang, sentence string) {
	t.Helper()
	wi := newWIWithBundle(t, inst, goal, vehicleLane, []map[string]any{w222Env("fix"), w222Env("review")})
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	capDir := d.capsuleDir(wi)
	w222WaitSettled(t, capDir)
	entries := w159SpawnJournal(t, capDir)
	if len(entries) == 0 {
		t.Fatalf("%s: no dispatch in the spawn journal", goal)
	}
	for _, e := range entries {
		if e["language"] != lang {
			t.Errorf("%s: dispatch %v of step %v recorded language %v, want %q", goal, e["spawn_n"], e["step_id"], e["language"], lang)
		}
		n, _ := e["spawn_n"].(float64)
		prompt, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-"+strconv.Itoa(int(n))+".txt"))
		if err != nil {
			t.Errorf("%s: dispatch %v prompt: %v", goal, e["spawn_n"], err)
			continue
		}
		if !strings.Contains(string(prompt), sentence) {
			t.Errorf("%s: dispatch %v prompt lacks %q", goal, e["spawn_n"], sentence)
		}
	}
}

func TestW501DaemonHandsConfiguredLanguageToDispatches(t *testing.T) {
	inst, _ := newV1InstallTaskRepo(t)
	d := newV1Daemon(t, inst)
	w501Config(t, d, "zh-CN")
	d.start()
	w501RunOne(t, inst, d, "w501 zh-CN at startup", "zh-CN", w501SentenceZhCN)

	w501Config(t, d, "en")
	w501RunOne(t, inst, d, "w501 en after reload", "en", w501SentenceEN)
}
