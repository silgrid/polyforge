package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// help_extensions_test.go: polyforge#229 — `pf help extensions` is a
// one-line-per-item index of the user extension points, each line naming
// the README "Extending" subsection that owns it; `pf help` lists the
// topic; doctor's config failures point at the same place.

// readmeExtendingSubsections answers the `### ` headings of the README's
// `## Extending` section, in order.
func readmeExtendingSubsections(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	_, after, ok := strings.Cut(string(raw), "\n## Extending\n")
	if !ok {
		t.Fatal("README.md has no `## Extending` section")
	}
	if i := strings.Index(after, "\n## "); i >= 0 {
		after = after[:i]
	}
	var subs []string
	for _, line := range strings.Split(after, "\n") {
		if s, ok := strings.CutPrefix(line, "### "); ok {
			subs = append(subs, s)
		}
	}
	return subs
}

func TestHelpExtensionsIndexesTheReadmeSubsections(t *testing.T) {
	code, stdout, stderr := runPf(t, map[string]string{}, "help", "extensions")
	if code != 0 {
		t.Fatalf("pf help extensions exited %d; stderr=%s stdout=%s", code, stderr, stdout)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], `README "Extending"`) {
		t.Fatalf("want a header naming README \"Extending\" then the items:\n%s", stdout)
	}
	item := regexp.MustCompile(`^  \S.*\S\s+README Extending › (.+)$`)
	guide := regexp.MustCompile(`^Daily-use guide: docs/user-guide\.md$`)
	var got []string
	guideSeen := false
	for _, l := range lines[1:] {
		if m := item.FindStringSubmatch(l); m != nil {
			got = append(got, m[1])
			continue
		}
		if guide.MatchString(l) {
			guideSeen = true
			continue
		}
		t.Fatalf("index line %q is not `<extension point>  README Extending › <subsection>` or the daily-use guide pointer:\n%s", l, stdout)
	}
	if !guideSeen {
		t.Fatalf("the extensions index must name the daily-use guide (docs/user-guide.md, polyforge#241):\n%s", stdout)
	}
	want := readmeExtendingSubsections(t)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("help index subsections %q, want exactly the README Extending subsections %q (one line each, same order)", got, want)
	}
	// polyforge#243/#250: the summarize line names all of its keys
	if !strings.Contains(stdout, ".polyforge.yaml summarize.models, summarize.language, summarize.interval_sec") {
		t.Fatalf("the index does not name summarize.language and summarize.interval_sec:\n%s", stdout)
	}
	if len(want) != 9 {
		t.Fatalf("README Extending has %d subsections %q, want the 9 inventory items", len(want), want)
	}
}

func TestHelpRegistryListsTheExtensionsTopic(t *testing.T) {
	code, stdout, _ := runPf(t, map[string]string{}, "help")
	if code != 0 {
		t.Fatalf("pf help exited %d", code)
	}
	if !regexp.MustCompile(`Topics \(pf help <topic>\):\n  extensions\s+\S`).MatchString(stdout) {
		t.Fatalf("pf help does not list the extensions topic under Topics:\n%s", stdout)
	}
	// verb help and the unknown-name answer are unchanged
	if code, stdout, _ := runPf(t, map[string]string{}, "help", "submit"); code != 0 || !strings.HasPrefix(stdout, "usage: ") {
		t.Fatalf("pf help submit: exit %d %q", code, stdout)
	}
	if code, stdout, _ := runPf(t, map[string]string{}, "help", "nosuch"); code != 2 || !strings.Contains(stdout, `unknown command "nosuch"`) {
		t.Fatalf("pf help nosuch: exit %d %q", code, stdout)
	}
}

func TestDoctorConfigFailuresPointAtExtending(t *testing.T) {
	const hint = `README "Extending" (pf help extensions)`
	t.Run("workspace mapping", func(t *testing.T) {
		srv := aiSever(t, &reqRecorder{}, nil, 0, "")
		env := doctorServerConfig(t, srv.URL, "k", "")
		_, _, doc := doctorStatus(t, env, "--json", "doctor")
		if st, detail := doctorFind(t, doc, "workspace mapping"); st != "fail" || !strings.Contains(detail, hint) {
			t.Fatalf("mapping fail detail lacks the Extending pointer: %s %q", st, detail)
		}
	})
	t.Run("workspace yaml", func(t *testing.T) {
		wf := t.TempDir()
		if err := os.MkdirAll(filepath.Join(wf, "autopilot"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wf, "autopilot", "workflow.md"), []byte("---\nsteps:\n  - { id: fix }\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"machine_id":"m-y","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
		})
		sock := newFakeDaemon(t, mux)
		srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
		env := doctorServerConfig(t, srv.URL, "k", sock, "polyforge")
		yaml := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge", ".polyforge.yaml")
		if err := os.WriteFile(yaml, []byte("steps:\n  polyforge:\n    gate: { models: [pi:c] }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, doc := doctorStatus(t, env, "--json", "doctor")
		if st, detail := doctorFind(t, doc, "workspace yaml"); st != "fail" || !strings.Contains(detail, hint) {
			t.Fatalf("yaml fail detail lacks the Extending pointer: %s %q", st, detail)
		}
		// a passing yaml carries no pointer
		if err := os.WriteFile(yaml, []byte("steps:\n  polyforge:\n    fix: { models: [pi:b] }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, doc = doctorStatus(t, env, "--json", "doctor")
		if st, detail := doctorFind(t, doc, "workspace yaml"); st != "pass" || strings.Contains(detail, hint) {
			t.Fatalf("passing yaml: %s %q", st, detail)
		}
	})
}
