package test

// w433_user_lane_e2e_test.go (AC5, workflow contract "User
// lanes"): a user lane written from the reference document alone runs a
// work item to the end. The lane is the reference's own worked example —
// the fenced files of docs/reference/workflow-contract.md's "A user lane,
// end to end" section, copied out the way a reader would, with no Go code
// read — planted in the e2e daemon's workspace under
// .polyforge/workflows/<lane>/. A real aihub, a real `pf daemon` and
// `pf submit`, the fakepi agent: the work item is claimed on the
// workspace's lane set, its capsule snapshots the user lane (source
// workspace) beside the built-in shared tree, and it reaches wrapped.
//
// carries: internal/common/workflow/laneset.go, docs/reference/workflow-contract.md

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// referenceUserLane answers the reference document's worked user lane:
// its name and its files (workflow.yaml, steps/<id>.md), from the fenced
// blocks of the "A user lane, end to end" section: the one block with no
// frontmatter declaring contract: and steps: is the workflow.yaml.
func referenceUserLane(t *testing.T) (string, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "docs", "reference", "workflow-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "### A user lane, end to end")
	if i < 0 {
		t.Fatal("docs/reference/workflow-contract.md has no \"A user lane, end to end\" section")
	}
	section := doc[i:]
	if j := strings.Index(section[1:], "\n## "); j >= 0 {
		section = section[:j+1]
	}
	var fences []string
	var cur []string
	in := false
	for _, l := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if in {
				fences = append(fences, strings.Join(cur, "\n")+"\n")
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, l)
		}
	}
	files := map[string]string{}
	name := ""
	steps, contract := regexp.MustCompile(`(?m)^steps:`), regexp.MustCompile(`(?m)^contract:`)
	nameRe := regexp.MustCompile(`(?m)^name:\s*([a-z][a-z0-9-]*)\s*$`)
	for _, f := range fences {
		front, _, ok := workflow.SplitFrontmatter([]byte(f))
		if !ok {
			if m := nameRe.FindStringSubmatch(f); m != nil && steps.MatchString(f) && contract.MatchString(f) {
				name = m[1]
				files[workflow.WorkflowFile] = f
			}
			continue
		}
		m := nameRe.FindSubmatch(front)
		if m == nil {
			t.Fatalf("a frontmatter block of the section declares no name:\n%s", f)
		}
		files["steps/"+string(m[1])+".md"] = f
	}
	if name == "" || len(files) < 2 {
		t.Fatalf("the section's worked lane is incomplete: %v", files)
	}
	return name, files
}

func TestW433UserLaneFromTheReferenceRunsToTheEnd(t *testing.T) {
	lane, files := referenceUserLane(t)
	inst, _ := newV1InstallTaskRepo(t)
	d := newV1Daemon(t, inst)
	laneDir := filepath.Join(workflow.UserLanesRoot(d.workspace), lane)
	for rel, body := range files {
		p := filepath.Join(laneDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the agent's envelopes, one per step of the documented lane
	wi := newWIWithBundle(t, inst, "w433: a note on a user lane", lane, []map[string]any{
		{"do": "envelope", "id": "draft", "outcome": "completed", "output": map[string]any{"summary": "a short note"}},
		{"do": "envelope", "id": "check", "outcome": "completed", "output": map[string]any{"notes": "the note matches"}},
	})
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	capDir := d.capsuleDir(wi)
	if x := readExecution(t, capDir); x.WorkflowStatus != "completed" {
		t.Fatalf("the user lane's workflow did not complete: %q (%s)", x.WorkflowStatus, x.BlockedReason)
	}
	for _, step := range []string{"draft", "check"} {
		if len(w222Steps(t, capDir, step)) == 0 {
			t.Fatalf("step %s of the user lane was never dispatched", step)
		}
	}
	raw, err := os.ReadFile(filepath.Join(capDir, "sync", "materialized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		BundleName  string   `json:"bundle_name"`
		Source      string   `json:"source"`
		ContentHash string   `json:"content_hash"`
		Targets     []string `json:"targets"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.BundleName != lane || m.Source != workflow.SourceWorkspace || !strings.HasPrefix(m.ContentHash, "sha256:") {
		t.Fatalf("the snapshot marker = %+v, want lane %s, source workspace and a content hash", m, lane)
	}
	want := map[string]bool{"workflows/" + lane + "/" + workflow.WorkflowFile: false, "workflows/_shared/principles.md": false}
	for _, rel := range m.Targets {
		if _, ok := want[rel]; ok {
			want[rel] = true
		}
	}
	for rel, seen := range want {
		if !seen {
			t.Fatalf("the snapshot lacks %s (targets %v)", rel, m.Targets)
		}
	}
}
