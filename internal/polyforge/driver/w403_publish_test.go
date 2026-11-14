package driver

// w403_publish_test.go (A2/AC4): the develop lane's publish
// step replaces deliver. (Its recorded-command and result rules left with
// the notary, workflow contract v2 change 18: the publish skill's agent
// checks its upstream itself, P1.)

import (
	"path/filepath"
	"strings"
	"testing"
)

// The shipped develop lane publishes with the publish gate (the shared
// deliver stays for the other lanes) and ends with the merge agent step
// that runs merge.py and registers the landing as the wrap's
// `integration` attr (wrap_attrs; contract 3's `handoff: landing`);
// publish neither merges nor re-runs a test — since workflow contract 4
// (no output schema) its hand-off file, artifacts/publish.json, carries
// no merge or test field.
func TestW403ShippedDevelopEndsWithPublishThenMerge(t *testing.T) {
	w, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, "develop"))
	if err != nil {
		t.Fatal(err)
	}
	pub := w.Steps[len(w.Steps)-2]
	if pub.ID != publishStepID || w.step(deliverStepID) != nil || !pub.Gate || pub.Skill != "publish" || pub.OnFail == nil || pub.OnFail.Target != "build" {
		t.Fatalf("develop publishes with the publish gate on the publish skill (no deliver): %+v", pub)
	}
	last := w.Steps[len(w.Steps)-1]
	if last.ID != "merge" || last.Skill != "merge" || strings.Join(w.WrapAttrs(last.ID), ",") != "integration" || last.OnFail == nil || last.OnFail.Target != "sync" {
		t.Fatalf("develop ends with the merge agent step registering the landing (wrap_attrs: [integration]): %+v", last)
	}
	p := strings.Join(strings.Fields(spawnStepPrompt(t, "develop", publishStepID)), " ")
	for _, want := range []string{
		"**You do not merge, merge nothing into the branch and run no test**",
		"**Write** `artifacts/publish.json` — `{\"repos\": [{name, branch, pr_url, head, skipped_reason}]}`",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("publish's prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "merge_commit") || strings.Contains(p, "tests_passed") {
		t.Fatal("publish's hand-off carries no merge or test re-run fields")
	}
}
