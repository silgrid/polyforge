package driver

// w241_facts_proof_test.go (polyforge#241): develop's build declares the
// mechanical facts summary (`facts_proof`) and the driver carries the
// declaration through to the execution graph — the result file's path
// template and the one command that writes it, both carrying {repo}. The
// daemon holds no repository's command: the declared line names only the
// lane's facts.py.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestW241FactsProofDeclaration(t *testing.T) {
	wf, err := LoadWorkflowGraph(filepath.Join(shippedWorkflowsDir, developBundle))
	if err != nil {
		t.Fatalf("LoadWorkflowGraph: %v", err)
	}
	var build StepSpec
	found := false
	for _, s := range wf.Steps {
		if s.ID == "build" {
			build, found = s, true
		}
	}
	if !found {
		t.Fatal("develop carries no build step")
	}
	if build.FactsProof == nil {
		t.Fatal("develop's build declares no facts_proof: the done gate reads nothing")
	}
	if build.FactsProof.Result != "artifacts/build/handoff-facts/{repo}.json" {
		t.Errorf("facts_proof.result = %q, want the handoff result path template", build.FactsProof.Result)
	}
	cmd := build.FactsProof.Command
	if strings.ContainsAny(cmd, "\r\n") {
		t.Errorf("the command is not one line: %q", cmd)
	}
	if !strings.Contains(cmd, "facts.py\" handoff") {
		t.Errorf("the command does not run the lane's facts.py handoff: %q", cmd)
	}
	if !strings.Contains(cmd, "--out artifacts/build/handoff-facts/{repo}.json") {
		t.Errorf("the command does not write the declared result: %q", cmd)
	}
	for _, tool := range []string{"go ", "npm ", "make "} {
		if strings.Contains(cmd, tool) {
			t.Errorf("the command names %q, a repository tool the daemon must not know", tool)
		}
	}
	// the fast-check gate is a different proof, kept as it was
	if build.FastCheckProof == nil {
		t.Error("develop's build no longer declares fast_check_proof")
	}
}
