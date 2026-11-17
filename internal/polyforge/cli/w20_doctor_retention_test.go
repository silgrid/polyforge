package cli

// carries: internal/polyforge/cli/doctor_gitdirs.go, internal/polyforge/cli/doctor_gitdirs_test.go, internal/polyforge/cli/verbs_doctor.go, internal/polyforge/cli/w20_doctor_*_test.go

// w20_doctor_retention_test.go (polyforge#20): the `capsule retention`
// row and --prune-capsules read each Cell's own git root
// (<data-dir>/cells/<profile>/git, Q6=a) and that Cell's workspaces'
// capsules, with the work-item states read through that Cell's AIHub —
// the retired pre-migration root <data-dir>/git is never reported nor
// pruned — and --prune-scratch covers every Cell's workspaces.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w20RetentionFixture builds the dual-Cell machine of
// w20MappingFixture with both Cells mapped, and answers the runPf
// env, the data dir and the two Cells' fake AIHubs.
func w20RetentionFixture(t *testing.T) (map[string]string, string, *w17Hub, *w17Hub) {
	t.Helper()
	env, _, def, alpha := w20MappingFixture(t, t.TempDir(), t.TempDir())
	return env, env["POLYFORGE_DATA_DIR"], def, alpha
}

// w20OldEntry creates a git-dir-shaped entry and backdates it past the
// 14-day retention.
func w20OldEntry(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	w20Backdate(t, path, time.Now().Add(-30*24*time.Hour))
}

// w20Backdate sets an entry's (and everything under it) times to when.
func w20Backdate(t *testing.T, root string, when time.Time) {
	t.Helper()
	_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
		_ = os.Chtimes(p, when, when)
		return nil
	})
	_ = os.Chtimes(root, when, when)
}

// TestW20DoctorCellRetention (AC4): each Cell's retention row scans
// its own cells/<profile>/git and counts its own reclaimable entries,
// the WI states come from that Cell's AIHub (each hub sees only its own
// Cell's reads), and the retired pre-migration root <data-dir>/git is
// neither reported nor pruned.
func TestW20DoctorCellRetention(t *testing.T) {
	env, data, def, alpha := w20RetentionFixture(t)
	defGit := filepath.Join(data, "cells", "default", "git")
	alphaGit := filepath.Join(data, "cells", "alpha", "git")
	legacyGit := filepath.Join(data, "git")
	for _, d := range []string{filepath.Join(defGit, "wi_d.git"), filepath.Join(alphaGit, "wi_a.git"), filepath.Join(legacyGit, "wi_old.git")} {
		w20OldEntry(t, d)
	}
	// the default Cell's fake daemon names no job; the w20 hub answers
	// every work-item read as wrapped (the per-Cell reads are judged by
	// the recorders below)
	code, stdout, _ := runPf(t, env, "--json", "doctor")
	if code != 0 {
		t.Fatalf("doctor: exit %d (%s)", code, stdout)
	}
	var doc w20Doc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	dSt, dDetail := w20Find(t, doc, "default", docChCapsuleRetention)
	aSt, aDetail := w20Find(t, doc, "alpha", docChCapsuleRetention)
	if dSt != "warn" || aSt != "warn" {
		t.Fatalf("retention rows = %s / %s, want warn (each has one reclaimable, nothing pruned yet)", dSt, aSt)
	}
	if !strings.Contains(dDetail, defGit) || !strings.Contains(dDetail, "reclaimable 1 (") {
		t.Fatalf("default retention row does not scan %s:\n%s", defGit, dDetail)
	}
	if !strings.Contains(aDetail, alphaGit) || !strings.Contains(aDetail, "reclaimable 1 (") {
		t.Fatalf("alpha retention row does not scan %s:\n%s", alphaGit, aDetail)
	}
	if strings.Contains(dDetail, legacyGit) || strings.Contains(aDetail, legacyGit) {
		t.Fatalf("the retired root %s is still reported:\n%s\n%s", legacyGit, dDetail, aDetail)
	}
	// the WI states came through each Cell's own AIHub: the default hub
	// read wi_d only, the alpha hub wi_a only
	defSaw, alphaSaw := map[string]bool{}, map[string]bool{}
	for _, r := range def.rec.all() {
		if strings.Contains(r.Path, "work-items/wi_") {
			defSaw[strings.TrimPrefix(r.Path, "/api/v1/work-items/")] = true
		}
	}
	for _, r := range alpha.rec.all() {
		if strings.Contains(r.Path, "work-items/wi_") {
			alphaSaw[strings.TrimPrefix(r.Path, "/api/v1/work-items/")] = true
		}
	}
	if !defSaw["wi_d"] || len(defSaw) != 1 || !alphaSaw["wi_a"] || len(alphaSaw) != 1 {
		t.Fatalf("per-Cell WI reads wrong (default %v, alpha %v)", defSaw, alphaSaw)
	}
	// a prune removes exactly each Cell's own entry; the legacy root copy
	// stays (nothing reads it any more)
	code, stdout, _ = runPf(t, env, "--json", "doctor", "--prune-capsules")
	if code != 0 {
		t.Fatalf("doctor --prune-capsules: exit %d (%s)", code, stdout)
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	_, dDetail = w20Find(t, doc, "default", docChCapsuleRetention)
	_, aDetail = w20Find(t, doc, "alpha", docChCapsuleRetention)
	for _, want := range []string{"pruned 1", filepath.Join(defGit, "wi_d.git")} {
		if !strings.Contains(dDetail, want) {
			t.Fatalf("default prune detail lacks %q:\n%s", want, dDetail)
		}
	}
	for _, want := range []string{"pruned 1", filepath.Join(alphaGit, "wi_a.git")} {
		if !strings.Contains(aDetail, want) {
			t.Fatalf("alpha prune detail lacks %q:\n%s", want, aDetail)
		}
	}
	for _, gone := range []string{filepath.Join(defGit, "wi_d.git"), filepath.Join(alphaGit, "wi_a.git")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived the prune", gone)
		}
	}
	if _, err := os.Stat(legacyGit); err != nil {
		t.Fatalf("the legacy root copy %s must stay untouched (its owner was never read): %v", legacyGit, err)
	}
}

// TestW20DoctorPruneScratchEveryCell (AC4): --prune-scratch removes the
// stale entries of every Cell's workspaces, not only the default's.
func TestW20DoctorPruneScratchEveryCell(t *testing.T) {
	env, _, _, _ := w20RetentionFixture(t)
	// ctime cannot be backdated from a test (the download-arrival case it
	// exists for): judge by mtime alone, as the workspace package's own
	// tests do
	orig := workspace.ChangeTime
	workspace.ChangeTime = func(os.FileInfo) time.Time { return time.Time{} }
	t.Cleanup(func() { workspace.ChangeTime = orig })
	cfgDir := filepath.Join(env["XDG_CONFIG_HOME"], "polyforge")
	raw, err := os.ReadFile(filepath.Join(cfgDir, "cli.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wsD := w20MappedWorkspace(t, string(raw))
	rawA, err := os.ReadFile(filepath.Join(cfgDir, "cli-alpha.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wsA := w20MappedWorkspace(t, string(rawA))
	old := time.Now().Add(-30 * 24 * time.Hour)
	staleD := filepath.Join(wsD, "scratch", "old-d")
	staleA := filepath.Join(wsA, "scratch", "old-a")
	freshD := filepath.Join(wsD, "scratch", "fresh")
	for _, d := range []string{staleD, staleA, freshD} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w20Backdate(t, staleD, old)
	w20Backdate(t, staleA, old)
	code, stdout, _ := runPf(t, env, "doctor", "--prune-scratch")
	if code != 0 {
		t.Fatalf("doctor --prune-scratch: exit %d (%s)", code, stdout)
	}
	for _, gone := range []string{staleD, staleA} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived --prune-scratch", gone)
		}
	}
	if _, err := os.Stat(freshD); err != nil {
		t.Fatalf("the fresh entry was pruned: %v", err)
	}
	if !strings.Contains(stdout, "old-d") || !strings.Contains(stdout, "old-a") {
		t.Fatalf("the report must list each Cell's removed entry:\n%s", stdout)
	}
}

// w20MappedWorkspace answers the one [workspace."<path>"] key a
// w20MappingFixture config carries.
func w20MappedWorkspace(t *testing.T, cfg string) string {
	t.Helper()
	for _, line := range strings.Split(cfg, "\n") {
		if rest, ok := strings.CutPrefix(line, "[workspace.\""); ok {
			return strings.TrimSuffix(rest, "\"]")
		}
	}
	t.Fatalf("no [workspace] key in %q", cfg)
	return ""
}
