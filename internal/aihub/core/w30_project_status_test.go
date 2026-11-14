package core

// w30_project_status_test.go — polyforge#30's project deactivation
// core: AdminManager.DeactivateProject / RestoreProject. The refusal
// (CORE-409 NON_TERMINAL_WORK_ITEMS listing EVERY blocking slug, the
// same rule the hard delete keeps), the state machine (deactivate →
// deactivated, restore → active, the idempotence conflicts
// ALREADY_DEACTIVATED / NOT_DEACTIVATED), and the no-data-loss contract:
// no work item, no project row and no membership is deleted or rewritten
// at any step (AC3, AC5).

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w30StatusEnv is one deactivation fixture: the admin user, project
// alpha (the keeper sibling of every fixture's) and the target "w30".
func w30StatusEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newEnv(t)
	if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: "w30"}); err != nil {
		t.Fatalf("create w30: %v", err)
	}
	return env
}

// w30Reason decodes a typed error's extra into its reason and, when
// present, its work_items list.
func w30Reason(t *testing.T, err error) (string, []string) {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("not a typed error: %v", err)
	}
	var extra struct {
		Reason    string   `json:"reason"`
		WorkItems []string `json:"work_items"`
	}
	if len(ae.Extra) > 0 {
		if jerr := json.Unmarshal(ae.Extra, &extra); jerr != nil {
			t.Fatalf("decode extra %s: %v", ae.Extra, jerr)
		}
	}
	return extra.Reason, extra.WorkItems
}

// w30Terminal drives one bundled execution ticket of project w30
// through the real doors to the given TERMINAL status (claim, then
// session-end with that verdict).
func w30Terminal(t *testing.T, env *testEnv, goal, status string) {
	t.Helper()
	id := execWI(t, env, "w30", goal)
	sess := "w30-" + id
	if _, err := NewSessionManager(env.DB).Claim(id, api.SessionClaimRequest{
		SessionID: sess, MachineID: "m-w30",
	}, env.UserID); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	if err := NewSessionManager(env.DB).End(id, api.SessionEndRequest{
		SessionID: sess, Status: status,
	}, env.UserID); err != nil {
		t.Fatalf("end %s as %s: %v", id, status, err)
	}
}

// TestW30ProjectDeactivateUnknown: an unknown id or name answers
// CORE-404 from both doors.
func TestW30ProjectDeactivateUnknown(t *testing.T) {
	env := w30StatusEnv(t)
	for _, ref := range []string{"prj_nope", "never-existed"} {
		if _, err := env.Admin.DeactivateProject(ref); !isCode(err, api.ErrCoreNotFound) {
			t.Fatalf("deactivate %q: %v, want CORE-404", ref, err)
		}
		if _, err := env.Admin.RestoreProject(ref); !isCode(err, api.ErrCoreNotFound) {
			t.Fatalf("restore %q: %v, want CORE-404", ref, err)
		}
	}
}

// TestW30ProjectDeactivateNonTerminal: queued, running, paused and
// blocked work items — bundled and bundle-less alike — each block the
// deactivation with CORE-409 listing EVERY offending slug (detail and
// the typed extra's work_items), and nothing is deleted or rewritten:
// every work item row stays, the project stays active.
func TestW30ProjectDeactivateNonTerminal(t *testing.T) {
	env := w30StatusEnv(t)
	execWI(t, env, "w30", "queued bundled ticket")
	driveTo(t, env, "w30", "running bundled ticket", api.StatusRunning)
	driveTo(t, env, "w30", "paused bundled ticket", api.StatusPaused)
	driveTo(t, env, "w30", "blocked bundled ticket", api.StatusBlocked)
	if _, err := env.WIs.Create(CreateRequest{Project: "w30", Goal: "queued registry ticket", Reporter: env.UserID}); err != nil {
		t.Fatalf("bundle-less create: %v", err)
	}
	projID := projectIDOfTest(t, env, "w30")
	before := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, projID)
	p, err := env.Admin.DeactivateProject("w30")
	if !isCode(err, api.ErrCoreConflict) {
		t.Fatalf("deactivate w30: %v, want CORE-409", err)
	}
	if p != nil {
		t.Fatalf("deactivate w30 answered a project on refusal: %+v", p)
	}
	var ae *api.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Detail, "deactivated") {
		t.Fatalf("CORE-409 detail %q does not explain the deactivation refusal", ae.Detail)
	}
	reason, wis := w30Reason(t, err)
	if reason != "NON_TERMINAL_WORK_ITEMS" {
		t.Fatalf("typed reason %q, want NON_TERMINAL_WORK_ITEMS", reason)
	}
	if len(wis) != 5 {
		t.Fatalf("typed work_items lists %d slugs (%v), want all 5", len(wis), wis)
	}
	for _, seq := range []int{1, 2, 3, 4, 5} {
		want := "w30#" + strconv.Itoa(seq)
		found := false
		for _, s := range wis {
			if s == want {
				found = true
				break
			}
		}
		if !found || !strings.Contains(ae.Detail, want) {
			t.Fatalf("the refusal does not list %s (work_items %v, detail %q)", want, wis, ae.Detail)
		}
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, projID); n != before {
		t.Fatalf("the refused deactivation changed the work item count: %d, want %d", n, before)
	}
	if p, err := env.Admin.GetProject("w30"); err != nil || p.Status != api.ProjectStatusActive {
		t.Fatalf("the refused deactivation left status %q (%v), want active", p.Status, err)
	}
}

// TestW30ProjectDeactivateRestore: with every work item terminal the
// deactivation succeeds and deletes NOTHING (AC3's second half); the
// state machine answers deactivated, the repeated deactivation and the
// restore of an active project are typed conflicts, restore returns to
// active, and the deactivate → restore → deactivate → restore round
// trip keeps the data intact throughout (AC5).
func TestW30ProjectDeactivateRestore(t *testing.T) {
	env := w30StatusEnv(t)
	w30Terminal(t, env, "wrapped ticket", api.StatusWrapped)
	w30Terminal(t, env, "failed ticket", api.StatusFailed)
	less, err := env.WIs.Create(CreateRequest{Project: "w30", Goal: "canceled registry ticket", Reporter: env.UserID})
	if err != nil {
		t.Fatalf("bundle-less create: %v", err)
	}
	if _, err := env.WIs.Cancel(less.ID, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	projID := projectIDOfTest(t, env, "w30")
	beforeWI := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, projID)

	p, err := env.Admin.DeactivateProject("w30")
	if err != nil || p == nil {
		t.Fatalf("deactivate w30: %v", err)
	}
	if p.Status != api.ProjectStatusDeactivated {
		t.Fatalf("deactivate w30 answered status %q, want deactivated", p.Status)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, projID); n != beforeWI {
		t.Fatalf("deactivation deleted work items: %d, want %d", n, beforeWI)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM projects WHERE id=?`, projID); n != 1 {
		t.Fatal("deactivation deleted the project row")
	}
	if got, err := env.Admin.GetProject(projID); err != nil || got.Status != api.ProjectStatusDeactivated {
		t.Fatalf("GetProject after deactivate: status %q (%v), want deactivated", got.Status, err)
	}

	// The idempotence conflicts, typed on both doors.
	_, err = env.Admin.DeactivateProject("w30")
	if !isCode(err, api.ErrCoreConflict) {
		t.Fatalf("re-deactivate: %v, want CORE-409", err)
	}
	if reason, _ := w30Reason(t, err); reason != "ALREADY_DEACTIVATED" {
		t.Fatalf("re-deactivate reason %q, want ALREADY_DEACTIVATED", reason)
	}
	_, err = env.Admin.RestoreProject("alpha")
	if !isCode(err, api.ErrCoreConflict) {
		t.Fatalf("restore of an active project: %v, want CORE-409", err)
	}
	if reason, _ := w30Reason(t, err); reason != "NOT_DEACTIVATED" {
		t.Fatalf("active restore reason %q, want NOT_DEACTIVATED", reason)
	}

	p, err = env.Admin.RestoreProject("w30")
	if err != nil || p == nil {
		t.Fatalf("restore w30: %v", err)
	}
	if p.Status != api.ProjectStatusActive {
		t.Fatalf("restore w30 answered status %q, want active", p.Status)
	}
	// The full round trip once more, data intact at every step.
	if _, err := env.Admin.DeactivateProject("w30"); err != nil {
		t.Fatalf("re-deactivate after restore: %v", err)
	}
	if _, err := env.Admin.RestoreProject("w30"); err != nil {
		t.Fatalf("re-restore: %v", err)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, projID); n != beforeWI {
		t.Fatalf("the round trip changed the work item count: %d, want %d", n, beforeWI)
	}
	if p, err := env.Admin.GetProject("w30"); err != nil || p.Status != api.ProjectStatusActive {
		t.Fatalf("final state: status %q (%v), want active", p.Status, err)
	}
}
