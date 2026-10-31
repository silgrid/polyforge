package cli

// End-to-end acceptance for `pf msg contacts`: the contact
// read spans the union of every [workspace.*] project, deduplicates by user
// id, survives an unreadable project, is an empty success when nothing is
// configured, sees a mapping edit without a daemon restart, and is refused
// inside a step before any request leaves.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contactsConfig(t *testing.T, sections map[string][]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString("server = \"http://contacts.invalid:1\"\n")
	for name, projects := range sections {
		body.WriteString("\n[workspace.\"" + filepath.Join(dir, name) + "\"]\nprojects = [")
		for i, project := range projects {
			if i > 0 {
				body.WriteString(", ")
			}
			body.WriteString("\"" + project + "\"")
		}
		body.WriteString("]\n")
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg}
}

type contactsJSON struct {
	Contacts []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"contacts"`
}

func TestMsgContactsUnionDedupUnreadableAndReload(t *testing.T) {
	rec := &reqRecorder{}
	answer := map[string]string{
		"P1": `{"contacts":[{"id":"u_a","display_name":"Ava"},{"id":"u_shared","display_name":"Shared"}]}`,
		"P3": `{"contacts":[{"id":"u_shared","display_name":"Shared"},{"id":"u_b","display_name":"Ben"}]}`,
		"P4": `{"contacts":[{"id":"u_c","display_name":"Cleo"}]}`,
	}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		project := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/members")
		if project == "P2" { // the configured project the caller cannot read
			w.WriteHeader(http.StatusForbidden)
			ioJSON(w, `{"code":"AUTH-403","detail":"api key is scoped to another project"}`)
			return
		}
		if body, ok := answer[project]; ok {
			ioJSON(w, body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		ioJSON(w, `{"code":"CORE-404","detail":"project is unknown"}`)
	})
	// The current config invariant maps a project to exactly one workspace
	// (the spec's overlapping-section example is refused by the parser), so the
	// union is exercised through two entries and a user shared across projects.
	env := contactsConfig(t, map[string][]string{
		"wsA": {"P1", "P2"},
		"wsB": {"P3"},
	})
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_contacts"

	code, stdout, stderr := runPf(t, env, "--json", "msg", "contacts")
	if code != 0 {
		t.Fatalf("contacts read: exit %d %s %s", code, stdout, stderr)
	}
	var got contactsJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("contacts answer is not JSON: %s", stdout)
	}
	byID := map[string]string{}
	for _, row := range got.Contacts {
		if _, seen := byID[row.ID]; seen {
			t.Fatalf("contact %s appears twice: %s", row.ID, stdout)
		}
		byID[row.ID] = row.DisplayName
	}
	if len(byID) != 3 || byID["u_a"] != "Ava" || byID["u_shared"] != "Shared" || byID["u_b"] != "Ben" {
		t.Fatalf("the union is not P1 ∪ P3 with u_shared once: %s", stdout)
	}
	var requested []string
	for _, req := range rec.all() {
		requested = append(requested, req.Path)
	}
	if len(requested) != 3 {
		t.Fatalf("one request per distinct project expected, got %v", requested)
	}
	for _, project := range []string{"P1", "P2", "P3"} {
		found := false
		for _, path := range requested {
			if path == "/api/v1/projects/"+project+"/members" {
				found = true
			}
		}
		if !found {
			t.Fatalf("project %s was not read: %v", project, requested)
		}
	}

	// A workspace entry gains a project while the daemon keeps running: the
	// per-call file read picks it up on the very next invocation.
	env = contactsConfig(t, map[string][]string{
		"wsA": {"P1", "P2"},
		"wsB": {"P3", "P4"},
	})
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_contacts"
	code, stdout, stderr = runPf(t, env, "--json", "msg", "contacts")
	if code != 0 {
		t.Fatalf("contacts after mapping edit: exit %d %s %s", code, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("contacts after mapping edit is not JSON: %s", stdout)
	}
	foundCleo := false
	for _, row := range got.Contacts {
		if row.ID == "u_c" && row.DisplayName == "Cleo" {
			foundCleo = true
		}
	}
	if !foundCleo {
		t.Fatalf("the added project's contact did not appear without a daemon restart: %s", stdout)
	}
}

func TestMsgContactsEmptyAndStepRefusal(t *testing.T) {
	rec := &reqRecorder{}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		ioJSON(w, `{"contacts":[]}`)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k_contacts"}

	// Nothing configured: an empty success, not an error.
	code, stdout, stderr := runPf(t, env, "--json", "msg", "contacts")
	if code != 0 {
		t.Fatalf("empty mapping: exit %d %s %s", code, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != `{"contacts":[]}` {
		t.Fatalf("empty mapping answer = %s", stdout)
	}
	if len(rec.all()) != 0 {
		t.Fatalf("empty mapping reached aihub: %+v", rec.all())
	}

	// Inside a step the new read is refused before any request leaves.
	stepEnv := map[string]string{
		"PF_SERVER": srv.URL, "PF_API_KEY": "k_should_never_leave",
		"PF_WI_ID": "wi_st", "PF_DAEMON_ID": "m-test", "PF_DAEMON_SOCKET": "/nonexistent.sock",
	}
	code, _, stderr = runPf(t, stepEnv, "msg", "contacts")
	if code == 0 || stderr == "" {
		t.Fatalf("a step must be refused with a reason, got exit %d %q", code, stderr)
	}
	if len(rec.all()) != 0 {
		t.Fatalf("a step process reached aihub: %+v", rec.all())
	}
}

func ioJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
