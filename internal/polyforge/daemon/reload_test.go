package daemon

// reload_test.go: the reload classes of the two cli.toml table families
// a [summarize] change is applied live to the lane's
// snapshot, the [memory_policy] family is swapped whole and reported
// next-claim, and a hand edit changes nothing until the reload runs.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

func TestReloadAppliesSummarizeLiveAndPolicyNextClaim(t *testing.T) {
	td := newTestDaemon(t, nil)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	base := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n"
	write := func(extra string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(base+extra), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if models, _, _ := td.rt.SummarizeLane(); models != nil {
		t.Fatalf("premise: no table, lane off; got %v", models)
	}
	write("\n[summarize]\nmodels = [\"pi:m-a\"]\nlanguage = \"Japanese\"\n\n[memory_policy.\"" + ws + "\".default]\nrecord = [\"r\"]\n")
	// a hand edit is not served until the reload runs
	if models, _, _ := td.rt.SummarizeLane(); models != nil {
		t.Fatalf("a hand edit was served before any reload: %v", models)
	}
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, o := range resp.Outcomes {
		status[o.Key] = o.Status + "/" + o.Class
	}
	if status["summarize.models"] != "applied/live" || status["summarize.language"] != "applied/live" || status[cli.MemoryPolicyLabel] != "next-claim/next-claim" {
		t.Fatalf("outcomes = %v", status)
	}
	if _, ok := status["summarize.interval_sec"]; ok {
		t.Fatalf("an unchanged key must not be reported: %v", status)
	}
	models, lang, iv := td.rt.SummarizeLane()
	if !reflect.DeepEqual(models, []string{"pi:m-a"}) || lang != "Japanese" || iv != 60 {
		t.Fatalf("lane after reload = %v %q %d", models, lang, iv)
	}
	if got := td.live.MemoryPolicyOf(ws)["default"].Record; !reflect.DeepEqual(got, []string{"r"}) {
		t.Fatalf("policy after reload = %v", got)
	}
	// an unrelated reload reports neither family again
	resp, err = td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range resp.Outcomes {
		if o.Key == cli.MemoryPolicyLabel || strings.HasPrefix(o.Key, "summarize.") {
			t.Fatalf("an unchanged reload reported %v", o)
		}
	}
	// removing the table turns the lane off live
	write("")
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if models, _, _ := td.rt.SummarizeLane(); models != nil {
		t.Fatalf("a removed table left the lane on: %v", models)
	}
	if td.live.MemoryPolicyOf(ws) != nil {
		t.Fatal("a removed family left the policy in place")
	}
}
