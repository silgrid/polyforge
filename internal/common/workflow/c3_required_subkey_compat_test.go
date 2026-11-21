package workflow_test

// c3_required_subkey_compat_test.go: the contract-version rule's mechanical
// half judges a new required key against the lanes valid under base. A key
// set base does not carry at all is new with its parent key: no lane valid
// under base could use it, so its own required subkeys are no incompatible
// change (polyforge#211). A required key a set base already has stays
// incompatible, and so does a new parent key that is itself required — the
// parent of the new set is caught on its own set's rule.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// insertBlockAfter inserts block after the line header of dump.
func insertBlockAfter(t *testing.T, dump, header, block string) string {
	t.Helper()
	if !strings.Contains(dump, "\n"+header+"\n") {
		t.Fatalf("the registry dump carries no %q line", header)
	}
	return strings.Replace(dump, "\n"+header+"\n", "\n"+header+"\n"+block+"\n", 1)
}

// insertKeySet inserts a whole key-set section before the dump's [removed keys].
func insertKeySet(t *testing.T, dump, name, block string) string {
	t.Helper()
	if !strings.Contains(dump, "\n[removed keys]\n") {
		t.Fatalf("the registry dump carries no [removed keys] section")
	}
	return strings.Replace(dump, "\n[removed keys]\n",
		"\n["+name+" keys]\n"+block+"\n\n[removed keys]\n", 1)
}

func TestC3RequiredSubkeyCompat(t *testing.T) {
	base := workflow.RegistryDump()

	t.Run("a required subkey of a brand-new key set is compatible", func(t *testing.T) {
		cur := insertBlockAfter(t, base, "[step keys]", "fast_check_proof: map {result, command}")
		cur = insertKeySet(t, cur, "fast_check_proof", "command: string, required\nresult: string, required")
		cur = insertBlockAfter(t, cur, "[required]", "fast_check_proof.command\nfast_check_proof.result")
		if got := workflow.IncompatibleChanges(base, cur); len(got) != 0 {
			t.Fatalf("a required subkey of a brand-new key set is no incompatible change; got %v", got)
		}
		if err := workflow.JudgeRegistry(cur, cur, cur, 0); err != nil {
			t.Fatalf("golden and baseline regenerated in place, the version unchanged: want the pin green, got %v", err)
		}
	})

	t.Run("a new required key of an existing key set stays incompatible", func(t *testing.T) {
		cur := insertBlockAfter(t, base, "[step keys]", "mandated_required: string, required")
		cur = insertBlockAfter(t, cur, "[required]", "step.mandated_required")
		got := workflow.IncompatibleChanges(base, cur)
		if !strings.Contains(strings.Join(got, "; "), "new required key step.mandated_required") {
			t.Fatalf("a new required key of an existing set must be reported; got %v", got)
		}
	})

	t.Run("a new parent key that is itself required stays incompatible", func(t *testing.T) {
		cur := insertBlockAfter(t, base, "[step keys]", "fast_check_proof: map {result, command}, required")
		cur = insertKeySet(t, cur, "fast_check_proof", "command: string, required\nresult: string, required")
		cur = insertBlockAfter(t, cur, "[required]", "step.fast_check_proof\nfast_check_proof.command\nfast_check_proof.result")
		got := strings.Join(workflow.IncompatibleChanges(base, cur), "; ")
		if !strings.Contains(got, "new required key step.fast_check_proof") {
			t.Fatalf("a new parent key that is itself required must be reported; got %v", got)
		}
		if strings.Contains(got, "new required key fast_check_proof.") {
			t.Fatalf("the parent's required subkeys are no second finding; got %v", got)
		}
	})
}