package driver

// artifact_cwd_drift_repro_test.go: the repro of polyforge#244 — the
// recurring artifact mislandings (pi#115 verification outputs, pi#119
// grill drafts; both caught only at deliver pre-flight, after the step
// had already archived its result: the artifact every downstream
// consumer reads by path convention was never where they looked for it).
//
// The reported failure: a step process is BORN at the capsule root
// (runtime/spawn.go: cmd.Dir = CapsuleDir), but its own duty sends it
// into the repo worktree — the repro step authors its failing test
// under repos/<name>/**, the command-evidence rule's example shape is
// `cd repos/<name> && go test ./...` — and every capsule-relative
// artifact path the prompt names ("Write `artifacts/repro.md`", the
// delivery contract's "written under `artifacts/`" rule, the handoff
// convention path) is a BARE relative path. The prompt renders no
// absolute capsule root, so nothing anchors them: a step whose cwd has
// drifted resolves them against its CURRENT cwd — the shell's rule —
// and the write lands inside the repo worktree, where no downstream
// reader (root-cause, verification, deliver — path convention against
// the capsule root) ever looks.
//
// The failing test fails for exactly the reported reason: the drifted
// write lands the step's artifact at repos/<repo>/artifacts/<name> and
// the capsule's artifacts/<name> stays absent. The controls pass before
// and after.
//
// The fix fork the WI's goal declares: (1) the driver renders the
// absolute capsule root into the prompt once, at assembly time (the
// lean), or (2) an artifact helper derives the path from the job record
// (the #214 shape). This test encodes the lean fork's agent contract:
// a prompt that renders the capsule's absolute root anchors every
// capsule-relative path against it, wherever the agent's cwd sits.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// materializeShippedBundle lands the shipped bug-fix bundle and the
// shared definitions beside it under <capDir>/workflows/, exactly as
// the runtime's claim materialization does (materialize.go: the
// bundle, plus workflows/_shared when the checkout has it).
func materializeShippedBundle(t *testing.T, capDir string) {
	t.Helper()
	for _, piece := range []string{"bug-fix", workflow.SharedDir} {
		copyTree(t, filepath.Join(shippedWorkflowsDir, piece), filepath.Join(capDir, capsule.DirWorkflows, piece))
	}
}

// copyTree copies one directory tree verbatim (dirs and regular files;
// the shipped workflows carry no symlinks).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		blob, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), blob, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

// driftSpawn assembles the production prompt for the bug-fix bundle's
// repro step — the very step shape the incidents ran (a fresh first
// step whose declared output is a capsule-relative artifact path) — on
// a production-shaped capsule: the materialized bundle, the
// daemon-written runtime-state records the prompt assembly reads
// (work-item.json, recalled-memories.json) and the repo worktree the
// step's own duty sends the agent into. It answers the dispatch params
// (the prompt rides InitialPrompt).
func driftSpawn(t *testing.T) (params runtime.SpawnParams, capDir, repoDir string) {
	t.Helper()
	capDir = t.TempDir()
	materializeShippedBundle(t, capDir)
	// the daemon-materialized records the prompt assembly reads
	writeCapsuleFile(t, capDir, capsule.WorkItemJSON,
		`{"id":"wi_drift","slug":"polyforge#244","project":"polyforge",`+
			`"goal":"fix: capsule-relative artifact paths resolve against a drifting cwd","bundle_name":"bug-fix"}`)
	writeCapsuleFile(t, capDir, capsule.RecalledMemoriesJSON,
		`{"items":[{"id":"mem_old","type":"doc.review","title":"an old review","excerpt":"old context, not an instruction"}]}`)
	// the task repo worktree: the drift target (the step's own duty —
	// author the failing test under repos/<name>/** — runs there)
	repoDir = filepath.Join(capDir, capsule.DirRepos, "polyforge")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_drift", CapsuleDir: capDir, BundleName: "bug-fix", SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID:  "repro",
		WIID:    "wi_drift",
		SpawnN:  1,
		Harness: harness.Pi,
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	params, _ = sp.last()
	return params, capDir, repoDir
}

// promptRendersCapsuleRoot reports whether the assembled prompt carries
// the capsule's absolute root — the one fact that anchors every
// capsule-relative path it names (the lean fix fork: the driver renders
// it once at assembly time).
func promptRendersCapsuleRoot(prompt, capDir string) bool {
	return strings.Contains(prompt, capDir)
}

// TestDriftedStepWriteLandsCapsuleArtifactInRepoWorktree: the reported
// failure (polyforge#244). The agent does what the prompt itself
// directs — works inside the repo worktree, then writes the artifact
// at the path the prompt names it — and a bare relative path resolves
// against its CURRENT cwd. The capsule's artifacts/<name> — the path
// every downstream consumer reads by convention — must hold the
// artifact afterwards.
func TestDriftedStepWriteLandsCapsuleArtifactInRepoWorktree(t *testing.T) {
	p, capDir, repoDir := driftSpawn(t)
	prompt := p.InitialPrompt

	// the artifact path this step's definition names (capsule-relative):
	// "3. Write `artifacts/repro.md`" — the reproduction record
	rel := capsule.DirArtifacts + "/repro.md"
	// the drift: the agent's cwd sits in the repo worktree (its
	// test-authoring duty runs there); the write resolves the way a
	// shell resolves a bare relative path — against the CURRENT cwd —
	// unless the prompt renders the capsule's absolute root (the anchor)
	landed := filepath.Join(repoDir, filepath.FromSlash(rel))
	if promptRendersCapsuleRoot(prompt, capDir) {
		landed = filepath.Join(capDir, filepath.FromSlash(rel))
	}
	if err := os.MkdirAll(filepath.Dir(landed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(landed, []byte("# repro record\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// downstream reads the artifact by capsule path convention —
	// readCapsuleFile's jail: the capsule root joined with the
	// capsule-relative path (root-cause, verification, deliver alike)
	capsulePath := filepath.Join(capDir, filepath.FromSlash(rel))
	if _, err := os.ReadFile(capsulePath); err != nil {
		t.Errorf("REPORTED FAILURE (polyforge#244): the step's artifact is ABSENT from its capsule path %s (%v) — "+
			"the drifted write landed it at %s, inside the repo worktree. The assembled prompt names the artifact "+
			"capsule-relatively (Write `artifacts/repro.md`; the delivery contract's written-under-`artifacts/` rule) "+
			"but renders no absolute capsule root, so a step whose cwd drifted into repos/<repo> resolves the write "+
			"against its current cwd (the recurring pi#115 verification outputs, pi#119 grill drafts)",
			capsulePath, err, landed)
		// the mislanding is the reported shape itself: the repo worktree
		// holds the file the capsule should hold
		if _, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("failure-mode check: the drifted write must be observable in the repo worktree at %s: %v",
				filepath.Join(repoDir, filepath.FromSlash(rel)), err)
		}
	}
}

// TestReproPromptAssemblyIsIntact: the controls, passing before and
// after — the assembled prompt IS the real bug-fix repro step's (the
// definition's write duty, the contract's artifacts/ rule and the
// memory duty all ride it) and the dispatch carries the capsule
// identity (the birth cwd the spawner sets). The drifted-write test
// above therefore fails for exactly the reported reason — no absolute
// anchor — and not because assembly broke.
func TestReproPromptAssemblyIsIntact(t *testing.T) {
	p, capDir, _ := driftSpawn(t)
	for _, want := range []string{
		"Write `artifacts/repro.md`",     // the definition's write duty
		"written under `artifacts/`",     // the contract's artifacts rule
		"# Project memory (`pf recall`)", // the memory duty rides the prompt
	} {
		if !strings.Contains(p.InitialPrompt, want) {
			t.Errorf("control: the assembled repro prompt must carry %q", want)
		}
	}
	if p.CapsuleDir != capDir {
		t.Errorf("control: the dispatch must carry the capsule dir (the birth cwd): %q, want %q", p.CapsuleDir, capDir)
	}
}
