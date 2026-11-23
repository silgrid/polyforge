// carries: pi-web/app/api/polyforge/polyforge-override-route.test.mjs

package test

// polyforge84_override_route_flake_contract_test.go (polyforge#84 fix
// build, close red): the params-view test's ended sub-case starts two
// composed reads (the override and the params view) that the watch route
// drops unawaited once the ticket reads ended. The next reset() emptied the
// mock's argv log while a dropped child could still append, so under load
// "no opt-in, no params read" saw the previous request's params spawn. The
// fix is a bounded wait for the dropped reads' own argv before the reset
// (epic/85 b2f4129f's pattern, "polyforge#151's flaky spawn-set find").
//
// This guard pins the seam: the ended sub-case must wait, bounded, for the
// dropped reads' own argv before it resets, and the no-opt-in assertion
// must stay (the wait never weakens the judgment). The route's production
// behaviour is untouched — only the test's sequencing is.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverrideRouteTestWaitsForItsDroppedReads(t *testing.T) {
	root := v1Root(t)
	path := filepath.Join(root, "pi-web", "app", "api", "polyforge", "polyforge-override-route.test.mjs")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the override route test must ship: %v", err)
	}
	body := string(raw)

	// The one test whose ended sub-case drops two composed reads and then
	// resets the argv log before its no-opt-in assertion.
	const opening = `test("the watch route composes the params view beside the override view`
	from := strings.Index(body, opening)
	if from < 0 {
		t.Fatal("the params-view test must ship: it is the one the close red names")
	}
	to := strings.Index(body[from:], "\ntest(")
	if to < 0 {
		to = len(body) - from
	}
	block := body[from : from+to]

	if !strings.Contains(block, `"no opt-in, no params read"`) {
		t.Error("the params-view test must keep its no-opt-in assertion: the wait must never weaken the judgment")
	}

	ended := strings.Index(block, `reset("ended")`)
	if ended < 0 {
		t.Fatal("the params-view test must keep its ended sub-case")
	}
	next := strings.Index(block[ended:], `reset("view")`)
	if next < 0 {
		t.Fatal("the ended sub-case must reset back to view before its no-opt-in request")
	}
	between := block[ended : ended+next]
	for _, want := range []string{"while (Date.now() <", "argv()", `"params"`, "await new Promise"} {
		if !strings.Contains(between, want) {
			t.Errorf("the ended sub-case must wait, bounded, for the dropped reads' own argv before resetting (lacks %q): under load the previous request's params spawn appends after the reset", want)
		}
	}
}
