package cli

// pr8_placeholder_doctor_test.go (carry-over (c) of
// workflow contract 2): `pf doctor`'s workspace yaml row warns when a
// resolved step's first candidate is a placeholder no gateway serves
// (`<harness>:example-…`), naming each step; a project default in the
// workspace yaml silences it, the placeholder-only step list included (it
// is undeclared, so the default governs it). (The daemon step whose action
// started no agent, and so was no finding, went with workflow contract 3.)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

func TestDoctorWarnsOnAPlaceholderFirstCandidate(t *testing.T) {
	wf := t.TempDir()
	writeLane(t, wf, "autopilot", "defaults:\n  model_candidates: [pi:example-claude/claude-opus-5]\nsteps:\n"+
		"  - id: sync\n    model_candidates: [pi:example-gateway/model-a]\n"+
		"  - id: work\n")
	ws := t.TempDir()
	plan, err := mapping.Build([]mapping.Entry{{Workspace: ws, Projects: []string{"p"}}})
	if err != nil {
		t.Fatal(err)
	}
	st, detail, _ := workspaceYamlCheck(plan, wf, true)
	if st != "warn" || !strings.Contains(detail, "placeholder model") || !strings.Contains(detail, "p/autopilot/sync") || !strings.Contains(detail, "p/autopilot/work") {
		t.Fatalf("SYMPTOM: no warning on the placeholder first candidates: %s %s", st, detail)
	}
	// a healthy workspace: the project default governs every agent step
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte("steps:\n  p:\n    default: { models: [pi:prov/model-a] }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, detail, _ = workspaceYamlCheck(plan, wf, true)
	if st != "pass" || strings.Contains(detail, "placeholder") {
		t.Fatalf("SYMPTOM: a project default leaves a placeholder first candidate (the placeholder-only step list must be undeclared): %s %s", st, detail)
	}
}
