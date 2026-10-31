package cli

// w145_prune_unmerged_repro_test.go: the repro of — a
// failed work item's scene is removed wholesale by the cleanup path, no
// set-aside copy survives, and the commits the scene holds that no
// remote ever received go with it.
//
// The scene below is the #123 shape: the work item is terminal (failed),
// its capsule sits aside as capsules/<name>.quarantined.<ts>, and its
// task-repo clone inside carries a commit no remote carries. The cleanup
// path (`pf doctor --prune-git-dirs`, the only path in this tree that
// removes a capsule directory) is expected to REFUSE it — a scene holding
// unmerged commits is not reclaimable, whatever its work item's state and
// age. On the old code it is deleted like any other retired scene.

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const w145WI = "wi_w145failed"

// w145Git runs git with -c overrides that keep a hostile ambient config
// (hooks, signing) out of the fixture.
func w145Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "commit.gpgsign=false",
		"-c", "user.name=w145",
		"-c", "user.email=w145@example.invalid",
		"-c", "init.defaultBranch=main",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// w145Backdate moves every path under root outside the retention window
// (both mtimes; the row judges by the newest change under the entry).
func w145Backdate(t *testing.T, root string, at time.Time) {
	t.Helper()
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, at, at)
	})
	if err != nil {
		t.Fatalf("backdate %s: %v", root, err)
	}
}

func TestW145PruneKeepsSetAsideCapsuleWithUnmergedCommits(t *testing.T) {
	// aihub: the work item is terminal (failed) — exactly the state the
	// retention row calls reclaimable
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/work-items":
			io.WriteString(w, `{"items":[{"id":"`+w145WI+`","status":"failed"}],"total":1}`)
		case r.URL.Path == "/api/v1/work-items/"+w145WI:
			io.WriteString(w, `{"id":"`+w145WI+`","status":"failed"}`)
		case strings.HasSuffix(r.URL.Path, "/ready-queue"):
			io.WriteString(w, `{"project":"polyforge","items":[],"running":[],"paused":[]}`)
		default:
			io.WriteString(w, `{}`)
		}
	})
	// the daemon answers GET /status and names no job for the WI: the
	// prune's own liveness gate is satisfied
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"machine_id":"m-g","jobs":[],"queue":{}}`)
	})
	env := doctorServerConfig(t, srv.URL, "k_ok", newFakeDaemon(t, mux), "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	data := t.TempDir()
	env["POLYFORGE_DATA_DIR"] = data

	// the scene: capsules/<name>.quarantined.<ts> whose task-repo clone
	// carries a commit no remote ever received
	capsuleDir := filepath.Join(ws, "capsules", "polyforge-123.quarantined.w145")
	repoDir := filepath.Join(capsuleDir, "repos", "demo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w145Git(t, repoDir, "init")
	w145Git(t, repoDir, "commit", "--allow-empty", "-m", "implementation head nobody ever merged")
	if err := os.WriteFile(filepath.Join(capsuleDir, "capsule.json"), []byte(`{"work_item_id":"`+w145WI+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// the scene's external git-dir, set aside under the same convention
	gitDir := filepath.Join(data, "git", w145WI+".git.quarantined.w145")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w145Git(t, gitDir, "init", "--bare")

	old := time.Now().Add(-30 * 24 * time.Hour)
	w145Backdate(t, capsuleDir, old)
	w145Backdate(t, gitDir, old)

	// the cleanup path, run for real
	_, _, doc := doctorStatus(t, env, "--json", "doctor", "--prune-git-dirs")
	st, detail := doctorFind(t, doc, docChGitDirs)
	t.Logf("git dir retention row: %s — %s", st, detail)

	// EXPECTED: the scene holds commits no remote
	// carries, so the cleanup path refuses it and the capsule survives.
	if _, err := os.Stat(capsuleDir); err != nil {
		t.Fatalf("the set-aside capsule of a failed work item holding an unmerged commit was deleted by `pf doctor --prune-git-dirs` (%v): a scene with unmerged commits must be kept; row said %q", err, detail)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		t.Fatalf("the capsule's task-repo clone (with its unmerged commit) is gone (%v)", err)
	}
}
