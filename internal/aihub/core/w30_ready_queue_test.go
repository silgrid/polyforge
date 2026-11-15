package core

// w30_ready_queue_test.go — AC8 (polyforge#30): the ready queue's
// executable segment is the daemon auto-claim's source, and a
// deactivated project contributes NOTHING to it — the server-side
// refusal comes first (the claim door answers PROJECT_DEACTIVATED in any
// case, but the wave never even proposes the item). The running/paused
// visibility segments and every other read are untouched, and restoring
// the project puts the very same queued item back into the executable
// segment at once.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestW30ReadyQueueExcludesDeactivated: a queued, unblocked, bundled
// work item of a deactivated project is not executable (absent from
// items), and the same item is executable again the moment the project
// is restored.
func TestW30ReadyQueueExcludesDeactivated(t *testing.T) {
	env := w30StatusEnv(t)
	w30Terminal(t, env, "w30 wrapped history", api.StatusWrapped)
	if _, err := env.Admin.DeactivateProject("w30"); err != nil {
		t.Fatalf("deactivate w30: %v", err)
	}
	// The queued target the API can no longer create (AC3's 409 keeps it
	// out); seeded directly so the executable segment has a candidate.
	projID := projectIDOfTest(t, env, "w30")
	queued := "wi_w30_rqueue"
	if _, err := env.DB.Exec(`INSERT INTO work_items(id,seq,project_id,goal,reporter,bundle_name,created_at,updated_at)
		VALUES(?1,2,?2,'w30 ready-queue candidate',?3,'demo-bundle',?4,?4)`,
		queued, projID, env.UserID, Now()); err != nil {
		t.Fatalf("seed queued wi: %v", err)
	}

	q, err := env.WIs.ReadyQueue("w30")
	if err != nil {
		t.Fatalf("ready queue of a deactivated project: %v", err)
	}
	for _, it := range q.Items {
		if it.ID == queued {
			t.Fatalf("the ready queue proposes %s of the deactivated project (items %v): the auto-claim source must exclude it", queued, q.Items)
		}
	}
	if len(q.Running) != 0 || len(q.Paused) != 0 {
		t.Fatalf("the visibility segments moved: running %v, paused %v", q.Running, q.Paused)
	}

	// Restore: the very same item is executable again at once.
	if _, err := env.Admin.RestoreProject("w30"); err != nil {
		t.Fatalf("restore w30: %v", err)
	}
	q, err = env.WIs.ReadyQueue("w30")
	if err != nil {
		t.Fatalf("ready queue after restore: %v", err)
	}
	if len(q.Items) != 1 || q.Items[0].ID != queued {
		t.Fatalf("after restore the ready queue must propose exactly the seeded item, got %v", q.Items)
	}
}
