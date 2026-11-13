package driver

// candidate_anchor_regression_test.go: B1 — the candidate
// pointer anchors on the persisted Step.Candidate, never on a walk of the
// archived attempts. Every case drives the Driver surface (Run /
// prepareDispatch over the dispatch-time Compose hook), so the proof is an
// assertion on the dispatched model, not on a helper signature.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// anchorEnv builds a Driver over a scripted spawner whose Compose hook
// reads *list at every dispatch (an owner edit between dispatches changes
// the list).
func anchorEnv(t *testing.T, list *[]string) (*Driver, *fakeSpawner, *safeLog, string) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	sp := &fakeSpawner{}
	log := &safeLog{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(10, []StepSpec{spec("s")}...),
		Spawner:  sp,
		Log:      log,
		Link:     supervise.NewLink("wi_run"),
		Compose: func() (map[string][]string, error) {
			return map[string][]string{"s": *list}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, log, execPath
}

func anchorModels(sp *fakeSpawner) []string {
	var out []string
	for i := range sp.dispatched() {
		out = append(out, sp.spawnAt(i).Model)
	}
	return out
}

// Case 2: a model_error(a) whose switch is decided must not double when
// the owner edits the list while the dying dispatch is in flight.
func TestAnchorModelErrorNoDoubleAdvanceAcrossListEdit(t *testing.T) {
	for _, v := range []struct {
		name   string
		edited []string
	}{
		{"list [b,a,c]", []string{"pi:b", "pi:a", "pi:c"}},
		{"list [a,x,b]", []string{"pi:a", "pi:x", "pi:b"}},
	} {
		list := []string{"pi:a", "pi:b"}
		d, sp, _, _ := anchorEnv(t, &list)
		sp.script = []fakeEntry{
			{err: &ExitedError{Step: "s", ExitCode: 1, LastError: "model not found"}, act: func() { list = v.edited }},
			done(`{}`),
		}
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("%s: Run: %v", v.name, err)
		}
		if models := anchorModels(sp); len(models) != 2 || models[1] != "b" {
			t.Errorf("%s: dispatch models %v, want [a b]", v.name, models)
		}
	}
}

// Case 3: Step.Candidate is still the dead model a with its model_error(a)
// archived (trailing neutral attempts included) — the owed-advance shape a
// chain-end or no-chain death leaves once the owner's list edit adds a later
// candidate: the advance is kept.
func TestAnchorCrashWindowKeepsOwedAdvance(t *testing.T) {
	for _, c := range []struct {
		name     string
		attempts []capsule.Attempt
		list     []string
		want     string
	}{
		{"model_error(a)", []capsule.Attempt{att(FailExitModel, "pi:a")}, []string{"pi:a", "pi:b"}, "b"},
		{"model_error(a) + daemon_restart(a) + driver_relaunch(a)",
			[]capsule.Attempt{att(FailExitModel, "pi:a"), att(FailDaemonRestart, "pi:a"), att(FailDriverRelaunch, "pi:a")},
			[]string{"pi:a", "pi:b"}, "b"},
		{"model_error(a), list [x,a,b]", []capsule.Attempt{att(FailExitModel, "pi:a")}, []string{"pi:x", "pi:a", "pi:b"}, "b"},
	} {
		d := harnessDriver(t, c.list, c.attempts)
		d.exec.Steps["review"].Candidate = "pi:a"
		sp, _, err := d.prepareDispatch("review")
		if err != nil {
			t.Fatal(err)
		}
		if sp.Model != c.want {
			t.Errorf("%s: dispatch model %q, want %q", c.name, sp.Model, c.want)
		}
	}
}
