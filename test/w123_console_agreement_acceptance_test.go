package test

// w123_console_agreement_acceptance_test.go: acceptance
// criterion "the first CLI browse page and the /web/ console's first
// memory page agree on ids and order" (review finding s10#3). Runs the
// REAL pf CLI and the REAL aihub's /web/api/memories face against the same
// seeded project with default filters on both sides.

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// TestW123RecallBrowseMatchesConsoleFirstPage: the first page of
// `pf --json recall --project P` (no query, default limit and sort) lists
// the same ids, in the same order, as the console's first page.
func TestW123RecallBrowseMatchesConsoleFirstPage(t *testing.T) {
	inst := newV1Install(t)
	ids := w123Seed(t, inst, 25)
	// Move a few memories so updated order differs from insertion order:
	// two old rows become the newest, one new row the oldest.
	db := w103DB(t, inst)
	for i, id := range []string{ids[0], ids[3]} {
		w103Exec(t, db, `UPDATE memories SET updated_at=?1 WHERE id=?2`, fmt.Sprintf("2099-01-0%dT00:00:00Z", i+1), id)
	}
	w103Exec(t, db, `UPDATE memories SET updated_at=?1 WHERE id=?2`, "2000-01-01T00:00:00Z", ids[24])

	cli := w123IDs(w123Browse(t, inst, w123Cfg(t)))
	code, web, raw := w103WebList(t, inst, inst.adminKey, v1Project, "")
	if code != http.StatusOK {
		t.Fatalf("console first page = %d: %s", code, raw)
	}
	console := make([]string, 0, len(web.Items))
	for _, it := range web.Items {
		console = append(console, it.ID)
	}
	if len(cli) != 20 {
		t.Fatalf("CLI first page = %d rows, want 20", len(cli))
	}
	if !slices.Equal(cli, console) {
		t.Fatalf("CLI first page ids/order differ from the console's:\n cli     %v\n console %v", cli, console)
	}
	if cli[0] != ids[3] || cli[1] != ids[0] || slices.Contains(cli, ids[24]) {
		t.Fatalf("first page does not follow the moved updated_at order: %v", cli)
	}
}
