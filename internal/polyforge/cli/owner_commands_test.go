package cli

// owner_commands_test.go: the owner's commands on a parked work item are
// `pf resume` and `pf cancel` alone (workflow contract 4, design #483 §7,
// owner ruling 2026-11-11): `pf waive` and `pf rewind` were removed
// the owner tells the step, which hands off `waive` or
// `rewind --to` citing the message (--per-owner).

import (
	"strings"
	"testing"
)

func TestOwnerCommandsAreResumeAndCancelOnly(t *testing.T) {
	for _, gone := range []string{"waive", "rewind"} {
		if lookupCommand(gone) != nil {
			t.Fatalf("pf %s is registered; it was removed with workflow contract 4", gone)
		}
		code, _, stderr := runPf(t, nil, gone, "wi_x", "--to", "plan", "--step", "check", "--reason", "x")
		if code == 0 || !strings.Contains(stderr, "unknown command \""+gone+"\"") {
			t.Fatalf("pf %s = exit %d, stderr %q; want the unknown-command refusal", gone, code, stderr)
		}
	}
	for _, kept := range []string{"resume", "cancel"} {
		if lookupCommand(kept) == nil {
			t.Fatalf("pf %s is not registered; it is the owner's command for a work item no agent can start", kept)
		}
	}
}
