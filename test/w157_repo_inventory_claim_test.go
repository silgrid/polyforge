package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w157: a work item that declares a repo inventory clones ONLY the
// declared repos. The project carries two task repos (yingfang and
// skypilot, the production shape that motivated polyforge#157); the
// ticket declares yingfang by name only. A real aihub, a real daemon, a
// real submit: the capsule's repos/ holds yingfang and no skypilot.
func TestW157ClaimClonesOnlyDeclaredRepos(t *testing.T) {
	yingfang, skypilot := newTaskRemote(t), newTaskRemote(t)
	inst := newV1InstallRepos(t, []api.ProjectRepo{
		{Name: "yingfang", URL: yingfang.url},
		{Name: "skypilot", URL: skypilot.url},
	})
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:       v1Project,
		Goal:          "w157: only the declared repo is cloned",
		BundleName:    strPtr("autopilot"),
		RepoInventory: json.RawMessage(`[{"name":"yingfang"}]`),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("create answer: %v (%s)", err, raw)
	}
	wi := created.ID
	inst.trackWI(wi)
	inst.setPlan(t, wi, []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)

	repos := filepath.Join(d.capsuleDir(wi), "repos")
	if _, err := os.Stat(filepath.Join(repos, "yingfang", ".git")); err != nil {
		t.Fatalf("the declared repo was not cloned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repos, "skypilot")); !os.IsNotExist(err) {
		t.Fatalf("an undeclared project repo was cloned (repos/skypilot: %v)", err)
	}
}
