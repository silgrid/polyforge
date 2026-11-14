package workspace

// w479_integrate_removed_test.go: the root `integrate:` section of
// `.polyforge.yaml` is removed with the daemon's automatic integrate
// trigger (a person files a batch with `pf file create --bundle integrate`
// and `pf submit`). A file still
// carrying it — whatever its entries, alone or beside `steps:` and
// `tiers:` — is refused whole, naming the file and the rewrite hint
// (IntegrateRemovedHint), never ignored; the hint is the one
// docs/reference/config.md quotes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func w479Load(t *testing.T, body string, projects ...string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir, projects)
}

func TestW479IntegrateSectionRefused(t *testing.T) {
	steps := "steps:\n  p:\n    default: { models: [\"pi:m\"] }\n"
	for name, body := range map[string]string{
		"auto false":          "integrate:\n  p:\n    auto: false\n",
		"auto true":           "integrate:\n  p:\n    auto: true\n",
		"quiet":               "integrate:\n  p:\n    quiet: 30m\n",
		"both, two projects":  "integrate:\n  p:\n    auto: false\n    quiet: 1h\n  q:\n    auto: false\n",
		"beside steps":        steps + "integrate:\n  p:\n    auto: false\n",
		"before steps":        "integrate:\n  p:\n    auto: false\n" + steps,
		"beside tiers":        "tiers:\n  p:\n    cheap: { models: [\"pi:c\"] }\nintegrate:\n  q:\n    quiet: 0s\n",
		"empty section":       "integrate:\n",
		"an unmapped project": "integrate:\n  zz:\n    auto: false\n",
		"a key it never took": "integrate:\n  p:\n    colour: red\n",
		"not even a mapping":  "integrate: [p]\n",
	} {
		cfg, err := w479Load(t, body, "p", "q")
		if err == nil || cfg != nil {
			t.Fatalf("%s: a leftover integrate: section is refused, never ignored: cfg %+v, err %v", name, cfg, err)
		}
		if !strings.Contains(err.Error(), FileName) || !strings.Contains(err.Error(), "`integrate:` "+IntegrateRemovedHint) {
			t.Fatalf("%s: the refusal names the file and the rewrite hint: %v", name, err)
		}
	}
	for _, want := range []string{"pf file create --bundle integrate", "pf submit", "delete the whole `integrate:` section"} {
		if !strings.Contains(IntegrateRemovedHint, want) {
			t.Fatalf("the hint says %q: %s", want, IntegrateRemovedHint)
		}
	}
	// the hint is the one the workspace yaml reference quotes
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "reference", "config.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(doc)), " "), IntegrateRemovedHint) {
		t.Fatalf("docs/reference/config.md quotes the refusal's hint verbatim: %s", IntegrateRemovedHint)
	}

	// without the section the file loads as before
	cfg, err := w479Load(t, steps, "p")
	if err != nil || cfg == nil || len(cfg.Models["p"]["default"]) != 1 {
		t.Fatalf("a file without integrate: loads: %+v %v", cfg, err)
	}
	_, err = w479Load(t, "workflow_defaults: {}\n", "p")
	if err == nil || !strings.Contains(err.Error(), "only `steps` and `tiers`") {
		t.Fatalf("the top-level refusal names the two sections there are: %v", err)
	}
}
