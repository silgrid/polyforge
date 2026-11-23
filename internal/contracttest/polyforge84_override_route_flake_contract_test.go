// carries: pi-web/app/api/polyforge/polyforge-override-route.test.mjs

package test

// polyforge84_override_route_flake_contract_test.go (polyforge#84 fix
// build, close red; polyforge#238 round 2): the ended sub-cases start two
// composed reads (the override and the params view) that the watch route
// drops unawaited once the ticket reads ended. The next reset() emptied the
// mock's argv log while a dropped child could still append, so under load
// "no opt-in, no params read" saw the previous request's params spawn; the
// reset also only compared exit records with the argv lines already written,
// so a child that had not reached its argv append yet was invisible.
//
// This guard pins the seam: every ended sub-case waits, bounded, for the
// dropped reads' own argv BEFORE it resets (the shared awaitDroppedReads
// helper, or the inline loop the params test keeps), and the no-opt-in
// assertion must stay (the wait never weakens the judgment). The route's
// production behaviour is untouched — only the test's sequencing is.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overrideTestBlock answers one test's source block: from its opening to the
// next test declaration (or the end of the file).
func overrideTestBlock(t *testing.T, body, opening string) string {
	t.Helper()
	from := strings.Index(body, opening)
	if from < 0 {
		t.Fatalf("the test must ship: %q", opening)
	}
	to := strings.Index(body[from:], "\ntest(")
	if to < 0 {
		to = len(body) - from
	}
	return body[from : from+to]
}

func TestOverrideRouteTestWaitsForItsDroppedReads(t *testing.T) {
	root := v1Root(t)
	path := filepath.Join(root, "pi-web", "app", "api", "polyforge", "polyforge-override-route.test.mjs")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the override route test must ship: %v", err)
	}
	body := string(raw)

	// The shared wait the ended sub-cases call: bounded, re-reads the argv
	// log every iteration, and fails (never returns quietly) at the deadline.
	helperStart := strings.Index(body, "const awaitDroppedReads = async () => {")
	if helperStart < 0 {
		t.Fatal("the override test must ship the shared awaitDroppedReads wait")
	}
	helperEnd := strings.Index(body[helperStart:], "\n};")
	if helperEnd < 0 {
		t.Fatal("awaitDroppedReads must be a complete function")
	}
	helper := body[helperStart : helperStart+helperEnd]
	for _, want := range []string{"while (Date.now() <", "argv()", "await new Promise", "assert.fail"} {
		if !strings.Contains(helper, want) {
			t.Errorf("awaitDroppedReads must wait, bounded, for the dropped reads' own argv (lacks %q): under load a child that has not reached its argv append lands after the reset", want)
		}
	}

	// Both ended sub-cases must wait for the dropped reads' own argv before
	// their next reset. The params test also keeps its no-opt-in assertion,
	// so the wait can never stand in for the judgment.
	const paramsOpening = `test("the watch route composes the params view beside the override view`
	params := overrideTestBlock(t, body, paramsOpening)
	if !strings.Contains(params, `"no opt-in, no params read"`) {
		t.Error("the params-view test must keep its no-opt-in assertion: the wait must never weaken the judgment")
	}
	blocks := []string{
		overrideTestBlock(t, body, `test("polyforge#48 AC3: an ended ticket's answer never carries the view"`),
		params,
	}
	for i, block := range blocks {
		ended := strings.Index(block, `reset("ended")`)
		if ended < 0 {
			t.Fatalf("sub-case %d must keep its ended sub-case", i+1)
		}
		tail := block[ended:]
		inline := strings.Contains(tail, `"params"`) && strings.Contains(tail, "while (Date.now() <")
		if !strings.Contains(tail, "awaitDroppedReads()") && !inline {
			t.Errorf("ended sub-case %d must wait, bounded, for the dropped reads' own argv before resetting: under load the previous request's override/params spawn appends after the reset", i+1)
		}
	}
}
