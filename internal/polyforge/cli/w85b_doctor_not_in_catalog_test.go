package cli

// w85b_doctor_not_in_catalog_test.go: doctor's `step tiers catalog` row
// (polyforge#143, 85b, AC2) — the user-written candidates (the workspace
// yaml's steps/tiers lists and cli.toml's [summarize] models) are checked
// against the Host's [step_tiers] catalog: a candidate outside it warns,
// never fails; an empty catalog skips the row; a placeholder candidate
// does not count; a catalog entry without an effort covers the model at
// any effort, one with an effort requires the same effort; and a catalog
// tier no installed lane declares is information only.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w85bCatalogEnv answers the doctor env of a machine whose Host cli.toml
// carries catalog (appended after doctorServerConfig's own rows) and
// whose workspace yaml is body, with the daemon reporting lanes as its
// workflows dir.
func w85bCatalogEnv(t *testing.T, catalog, yaml, lanes string) map[string]string {
	t.Helper()
	wf := t.TempDir()
	if lanes != "" {
		writeLane(t, wf, "autopilot", lanes)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-y","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	sock := newFakeDaemon(t, mux)
	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	env := doctorServerConfig(t, srv.URL, "k", sock, "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if catalog != "" {
		cfgPath := filepath.Join(env["XDG_CONFIG_HOME"], "polyforge", "cli.toml")
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, append(raw, []byte(catalog)...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

// TestW85bNotInCatalogWarns: a candidate outside the catalog warns with
// where it is written; the effort rule holds both ways; a placeholder
// does not count; and a catalog tier no installed lane declares is
// information only. Expected values are the candidates this fixture
// writes — none copied from the implementation's output. r1#1: the
// lane's own candidates (defaults.model_candidates, defaults.tiers via a
// model_tier step) enter resolved chains and are in no catalog tier —
// only the user-written candidates are judged (Q3 A), so the row must
// not name them; a row that judged resolved chains (Q3 B) would.
func TestW85bNotInCatalogWarns(t *testing.T) {
	catalog := "\n[step_tiers]\ndefault = [\"pi:in-catalog\"]\ncheap = [\"pi:effort-model:high\"]\nextra = [\"pi:extra-only\"]\n" +
		"\n[summarize]\nmodels = [\"pi:sum-model\"]\n"
	yaml := "steps:\n  polyforge:\n    fix: { models: [pi:in-catalog:high, pi:effort-model, pi:other, pi:example-placeholder] }\n" +
		"tiers:\n  polyforge:\n    cheap: { models: [pi:tier-cand] }\n"
	lanes := "defaults:\n  model_candidates: [pi:lane-default]\n  tiers:\n    cheap: [pi:lane-cheap]\nsteps:\n  - { id: fix }\n  - { id: probe, model_tier: cheap }\n  - { id: bare }\n"
	_, _, doc := doctorStatus(t, w85bCatalogEnv(t, catalog, yaml, lanes), "--json", "doctor")
	st, detail := doctorFind(t, doc, "step tiers catalog")
	if st != "warn" {
		t.Fatalf("a candidate outside the catalog warns (never fails): %s %q", st, detail)
	}
	for _, want := range []string{"pi:effort-model", "pi:other", "pi:tier-cand", "pi:sum-model"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("the warn must name the uncovered candidate %s: %q", want, detail)
		}
	}
	for _, where := range []string{"steps.polyforge.fix", "tiers.polyforge.cheap", "summarize.models"} {
		if !strings.Contains(detail, where) {
			t.Fatalf("the warn must name where the candidate is written (%s): %q", where, detail)
		}
	}
	// an entry without an effort covers the model at any effort: the
	// covered candidate is not named
	if strings.Contains(detail, "pi:in-catalog") {
		t.Fatalf("pi:in-catalog:high is covered by the effort-less entry pi:in-catalog: %q", detail)
	}
	// an entry with an effort requires the same effort: pi:effort-model
	// (no effort) is NOT covered by pi:effort-model:high — asserted above
	// by its presence in the warn
	// a placeholder does not count
	if strings.Contains(detail, "pi:example-placeholder") {
		t.Fatalf("a placeholder candidate is the workspace yaml row's finding, not this row's: %q", detail)
	}
	// a catalog tier no installed lane declares is information only
	if !strings.Contains(detail, "extra") || !strings.Contains(detail, "no installed lane declares") {
		t.Fatalf("the row must report the undeclared catalog tier as information: %q", detail)
	}
	// r1#1: the lane's own candidates are not the user's writing — probe
	// resolves to the lane's cheap tier (pi:lane-cheap) and bare to the
	// lane's model_candidates (pi:lane-default), neither is in the catalog,
	// and the row must not judge them (Q3 A; a resolved-chain row, Q3 B,
	// would name both)
	for _, laneCand := range []string{"pi:lane-cheap", "pi:lane-default", "steps.polyforge.probe", "steps.polyforge.bare"} {
		if strings.Contains(detail, laneCand) {
			t.Fatalf("the lane's own candidate/step %s is not user-written and must not be judged: %q", laneCand, detail)
		}
	}
}

// TestW85bNotInCatalogSkipsWhenEmpty: an empty catalog skips the row (the
// existing yaml still runs — nothing is judged against nothing), and a
// catalog that covers every user-written candidate passes.
func TestW85bNotInCatalogSkipsWhenEmpty(t *testing.T) {
	yaml := "steps:\n  polyforge:\n    fix: { models: [pi:covered] }\n"
	_, _, doc := doctorStatus(t, w85bCatalogEnv(t, "", yaml, ""), "--json", "doctor")
	st, detail := doctorFind(t, doc, "step tiers catalog")
	if st != "skip" {
		t.Fatalf("an empty catalog skips the row: %s %q", st, detail)
	}
	if !strings.Contains(detail, "empty") {
		t.Fatalf("the skip must say the catalog is empty: %q", detail)
	}
	_, _, doc = doctorStatus(t, w85bCatalogEnv(t, "\n[step_tiers]\ndefault = [\"pi:covered\"]\n", yaml, ""), "--json", "doctor")
	st, detail = doctorFind(t, doc, "step tiers catalog")
	if st != "pass" {
		t.Fatalf("a catalog covering every user-written candidate passes: %s %q", st, detail)
	}
}
