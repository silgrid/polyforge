package runtime

// polyforge#238 repro: a step process running under a claude-harness
// deliver step inherits the outer adapter's DISABLE_AUTOUPDATER=1 (the
// adapter sets it on every claude step it starts). The claude spawn
// adapter appends its own DISABLE_AUTOUPDATER=1 without stripping the
// inherited one, so the started claude step env carries the variable
// twice — TestSpawnStartedEnvHygiene/claude reports
// "DISABLE_AUTOUPDATER set 2 times" whenever the test process itself
// runs with the variable set (observed on #234's deliver: red under
// claude, green with `env -u`, red on clean main in the same env).
//
// This test encodes the deliver-step environment directly (t.Setenv)
// so the repro is deterministic and independent of the ambient
// environment: the started claude step env must carry the adapter's
// DISABLE_AUTOUPDATER exactly once, whatever the parent inherited.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/harness"
)

func TestSpawnClaudeInheritedAdapterVarNotDuplicated(t *testing.T) {
	// the outer claude adapter's switch, as the deliver-step process
	// that runs `go test` sees it
	t.Setenv("DISABLE_AUTOUPDATER", "1")

	dir := t.TempDir()
	bin := standIn(t, dir, "cat >/dev/null\n")
	cfg := &Config{PiBin: bin, ClaudeBin: bin, MachineID: "m-238", HarnessRoot: t.TempDir()}
	p := SpawnParams{
		WorkItemID: "wi-238", CapsuleDir: dir, InitialPrompt: "p",
		Harness: harness.Claude, HarnessSession: testSession,
	}

	seen := 0
	for _, kv := range spawnedEnv(t, cfg, p) {
		if name, _, _ := strings.Cut(kv, "="); name == "DISABLE_AUTOUPDATER" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the claude step env sets DISABLE_AUTOUPDATER %d times, want exactly 1 (the adapter's own; an inherited one must be stripped)", seen)
	}
}
