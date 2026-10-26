package runtime

// w174_step_boundary_test.go (polyforge#174): step agents can never run
// `pf daemon ensure`. The ensure callers are the user-session adapters —
// pi's pf-chat extension (loaded from the user's pi packages or
// ~/.pi/agent/extensions) and the Claude Code SessionStart hook. Pinned
// here, so a later change cannot quietly open the path:
//
//   - a pi step runs against the dedicated step agent dir: even when the
//     operator's agent dir lists the polyforge package (the local release
//     install.sh writes) and carries pf-chat / the v1 bridge in its
//     auto-loaded extensions/, nothing of it reaches the step dir (no
//     settings.json, no extension but subagent);
//   - a claude step runs with --bare or --safe-mode (no hooks, so no
//     SessionStart);
//   - both carry PF_WI_ID, the marker pf-chat and the hook check as their
//     second fence.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestW174StepAgentsCannotReachTheEnsureAdapters(t *testing.T) {
	src := operatorDirFixture(t)
	release := t.TempDir()
	for rel, body := range map[string]string{
		"package.json":                                   `{"pi":{"extensions":["./skills/polyforge/extensions/pf-chat.ts"]}}`,
		"skills/polyforge/extensions/pf-chat.ts":         "// pf-chat: pf daemon ensure\n",
		"skills/polyforge/hooks/claude-session-start.sh": "#!/bin/sh\npf daemon ensure\n",
	} {
		p := filepath.Join(release, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the operator's own pi: the install.sh package entry plus pf-chat
	// dropped straight into the auto-loaded extensions dir
	if err := os.WriteFile(filepath.Join(src, "settings.json"), []byte(`{"packages":["`+release+`","git:git@github.com:silgrid/polyforge.git@v2.7.0"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extensions", "pf-chat.ts"), []byte("// pf daemon ensure\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{PiBin: "/nonexistent-pi", ClaudeBin: "/nonexistent-claude", MachineID: "m-1", DataDir: t.TempDir(), OperatorAgentDir: src}
	cfg.FillDefaults()
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	err := filepath.WalkDir(cfg.StepAgentDir(), func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch de.Name() {
		case "settings.json", "pf-chat.ts", "polyforge":
			t.Fatalf("the step agent dir carries %s — a pi step could load the user-session adapter", p)
		}
		if de.Type().IsRegular() {
			raw, _ := os.ReadFile(p)
			if strings.Contains(string(raw), "pf daemon ensure") || strings.Contains(string(raw), release) {
				t.Fatalf("%s reaches the ensure adapter", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exts := listDirNames(t, filepath.Join(cfg.StepAgentDir(), "extensions")); !slices.Equal(exts, []string{"subagent"}) {
		t.Fatalf("step agent extensions = %v, want only subagent", exts)
	}

	// pi step: the dedicated dir and the step marker
	piEnv := strings.Join(newPiSpawner(&cfg).buildCmd(SpawnParams{WorkItemID: "wi_s", CapsuleDir: "/caps", SpawnN: 1}).Env, "\n") + "\n"
	for _, kv := range []string{"PI_CODING_AGENT_DIR=" + cfg.StepAgentDir() + "\n", "PF_WI_ID=wi_s\n"} {
		if !strings.Contains(piEnv, kv) {
			t.Fatalf("pi step env lacks %q:\n%s", strings.TrimSpace(kv), piEnv)
		}
	}

	// claude step: --bare or --safe-mode (either skips hooks) and the step marker
	sp := &claudeSpawner{cfg: &cfg}
	cmd, err := sp.buildCmd(SpawnParams{WorkItemID: "wi_c", CapsuleDir: "/caps", Model: "claude-opus-5-5", HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Args, "--bare") && !slices.Contains(cmd.Args, "--safe-mode") {
		t.Fatalf("claude step argv lacks --bare/--safe-mode (its SessionStart hook would run pf daemon ensure): %q", cmd.Args)
	}
	if !slices.Contains(cmd.Env, "PF_WI_ID=wi_c") {
		t.Fatal("claude step env lacks PF_WI_ID")
	}
}
