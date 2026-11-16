package daemon

// w80_target_branch_claim_test.go (polyforge#80 u4, AC3): two sub-tickets
// of the one parent, claimed concurrently through the daemon against one
// fake remote, end up on the ONE branch the parent's lane names — the
// origin carries exactly epic/<seq>, both capsules' task repos are
// checked out on it, and both capsules carry the same fixed
// runtime-state/target-branch.json.
//
// Expected values are the ticket's own: "两张子票同时首次领取不会建出
// 两条不同的分支（用假远端的测试）".

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// w80Git runs one git command in dir and answers its combined output.
func w80Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %v: %v: %s", dir, args, err, out)
	}
	return string(out)
}

func TestW80TargetBranchDaemonTwoClaimsOneBranch(t *testing.T) {
	var ws string
	td := newTestDaemon(t, func(c *Config) {
		lanes := t.TempDir()
		workflowtest.WriteLane(t, lanes, "demo-bundle", "steps:\n  - { id: a }\n")
		workflowtest.WriteLane(t, lanes, "epic-lane", "children_branch: \"epic/{seq}\"\nsteps:\n  - { id: a }\n")
		c.WorkflowsDir = lanes
		ws = c.Mapping.Workspaces()[0]
	})

	// the shared fake remote: one commit on main
	src := t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", src).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(src, "readme.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w80Git(t, src, "add", ".")
	w80Git(t, src, "commit", "-m", "init")
	tip := strings.TrimSpace(w80Git(t, src, "rev-parse", "HEAD"))

	// the parent epic and its two sub-tickets
	const parent = "wi_w80epic10"
	const c1, c2 = "wi_w80child10", "wi_w80child11"
	td.hub.mu.Lock()
	td.hub.items = map[string]*api.WorkItem{
		parent: {ID: parent, Slug: "polyforge#10", Seq: 10, ProjectName: "proj", BundleName: strp("epic-lane")},
		c1:     {ID: c1, ProjectName: "proj", BundleName: strp("demo-bundle"), ParentID: &parent},
		c2:     {ID: c2, ProjectName: "proj", BundleName: strp("demo-bundle"), ParentID: &parent},
	}
	td.hub.claimRepos = map[string][]api.ProjectRepo{
		c1: {{Name: "app", URL: "file://" + src}},
		c2: {{Name: "app", URL: "file://" + src}},
	}
	td.hub.mu.Unlock()

	for _, wi := range []string{c1, c2} {
		mkBare(t, td.cfg.GitRemoteBase, wi)
		if _, err := td.client.ClaimWithModels(wi, "normal", nil); err != nil {
			t.Fatalf("claim %s: %v", wi, err)
		}
	}
	for _, wi := range []string{c1, c2} {
		deadline := time.Now().Add(30 * time.Second)
		for {
			if ji, ok := td.rt.Info(wi); ok && ji.State == runtime.StateRunning {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never reached RUNNING (30s)", wi)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// both capsules: the fixed file and the task repo checked out on the
	// one branch
	for _, wi := range []string{c1, c2} {
		capDir := filepath.Join(ws, "capsules", wi)
		raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(capsule.TargetBranchJSON)))
		if err != nil || !strings.Contains(string(raw), `"target":"epic/10"`) {
			t.Fatalf("%s: the fixed target branch is %q, %v (want {\"target\":\"epic/10\",…})", wi, raw, err)
		}
		repoDir := filepath.Join(capDir, capsule.DirRepos, "app")
		if b := strings.TrimSpace(w80Git(t, repoDir, "symbolic-ref", "--short", "HEAD")); b != "epic/10" {
			t.Fatalf("%s: the task repo names %q, want epic/10", wi, b)
		}
		if head := strings.TrimSpace(w80Git(t, repoDir, "rev-parse", "HEAD")); head != tip {
			t.Fatalf("%s: the task repo stands on %q, want the default branch tip %q", wi, head, tip)
		}
	}
	// the remote: exactly main and the one epic branch, at the default tip
	if out := strings.TrimSpace(w80Git(t, src, "ls-remote", "--heads", ".")); out != tip+"\trefs/heads/main\n"+tip+"\trefs/heads/epic/10" {
		t.Fatalf("the origin must carry exactly main and the one epic/10 at %s, got %q", tip, out)
	}
}
