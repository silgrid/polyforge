package driver

// c3_fast_check_proof_passthrough_test.go: the daemon's step graph carries
// the lane's `fast_check_proof` declaration unchanged (polyforge#211): the
// hand-off gate reads exactly the templates the lane wrote, so the daemon
// knows no repository's command.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestFastCheckProofPassthrough(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "probe", `steps:
  - id: build
    fast_check_proof:
      result: artifacts/x/{repo}.json
      command: run-fast-check --repo {repo} --out artifacts/x/{repo}.json
  - id: sync
    fast_check_proof:
      result: artifacts/y/{repo}.json
      command: run-fast-check --repo {repo} --out artifacts/y/{repo}.json
  - id: review
`)
	wf, err := LoadWorkflowGraph(dir)
	if err != nil {
		t.Fatalf("LoadWorkflowGraph: %v", err)
	}
	build, sync, review := wf.step("build"), wf.step("sync"), wf.step("review")
	if build == nil || build.FastCheckProof == nil ||
		build.FastCheckProof.Result != "artifacts/x/{repo}.json" ||
		build.FastCheckProof.Command != "run-fast-check --repo {repo} --out artifacts/x/{repo}.json" {
		t.Fatalf("build.FastCheckProof = %+v, want the declared templates", build)
	}
	if sync == nil || sync.FastCheckProof == nil || sync.FastCheckProof.Result != "artifacts/y/{repo}.json" {
		t.Fatalf("sync.FastCheckProof = %+v, want artifacts/y/{repo}.json", sync)
	}
	if review == nil || review.FastCheckProof != nil {
		t.Fatalf("review.FastCheckProof = %+v, want nil", review)
	}
}
