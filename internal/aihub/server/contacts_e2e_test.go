package server

// End-to-end acceptance for the read-only project member face.
// The HTTP path and the {contacts:[{id,display_name}]} answer are the shape
// fixed by the plan; the spec only names the face. A pre-implementation tree
// answers 404 from the mux, so these are assertion-level reds, not build errors.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

type contactsAnswer struct {
	Contacts []map[string]any `json:"contacts"`
}

func readContacts(t *testing.T, e *srvEnv, key, project string) (int, []byte, contactsAnswer) {
	t.Helper()
	return readContactsAt(t, e, key, "/api/v1/projects/"+project+"/members")
}

func readContactsAt(t *testing.T, e *srvEnv, key, path string) (int, []byte, contactsAnswer) {
	t.Helper()
	code, raw := e.do(t, "GET", path, key, nil, nil)
	var answer contactsAnswer
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatalf("contacts answer is not JSON: %s", raw)
		}
	}
	return code, raw, answer
}

func TestAihubMemberContactsReadableCallers(t *testing.T) {
	e := newSrvEnv(t)
	project, err := e.admin.GetProject("alpha")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	member, err := e.admin.CreateUser("Member Mia", "mia@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	mate, err := e.admin.CreateUser("Mate Mo", "mo@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("mate: %v", err)
	}
	machine, err := e.admin.CreateUser("Machine Max", "", "writer", "machine")
	if err != nil {
		t.Fatalf("machine: %v", err)
	}
	for _, user := range []string{member.ID, mate.ID, machine.ID} {
		if err := e.admin.AddMember(project.ID, user, "writer"); err != nil {
			t.Fatalf("add member %s: %v", user, err)
		}
	}
	memberKey, err := e.admin.CreateAPIKey(member.ID, "member", "")
	if err != nil {
		t.Fatalf("member key: %v", err)
	}

	// A member reads the human co-members: self and the machine member are
	// absent, and every row carries only id + display_name.
	code, raw, answer := readContacts(t, e, memberKey.Plain, project.ID)
	if code != http.StatusOK {
		t.Fatalf("member read: %d %s", code, raw)
	}
	names := map[string]string{}
	for _, row := range answer.Contacts {
		if len(row) != 2 {
			t.Fatalf("a contact row carries more than id + display_name: %#v", row)
		}
		id, _ := row["id"].(string)
		name, _ := row["display_name"].(string)
		if id == "" || name == "" {
			t.Fatalf("a contact row lacks id or display_name: %#v", row)
		}
		names[id] = name
	}
	if _, ok := names[member.ID]; ok {
		t.Fatalf("the caller's own row is in the answer: %#v", names)
	}
	if _, ok := names[machine.ID]; ok {
		t.Fatalf("a machine member is in the answer: %#v", names)
	}
	if names[mate.ID] != "Mate Mo" {
		t.Fatalf("the human co-member is not listed with its display name: %#v", names)
	}

	// A global admin that is not a member gets the same scope: every human
	// member other than the caller, still no machine row.
	code, raw, answer = readContacts(t, e, e.key, project.ID)
	if code != http.StatusOK {
		t.Fatalf("admin read: %d %s", code, raw)
	}
	names = map[string]string{}
	for _, row := range answer.Contacts {
		if len(row) != 2 {
			t.Fatalf("an admin row carries more than id + display_name: %#v", row)
		}
		names[row["id"].(string)] = row["display_name"].(string)
	}
	for _, id := range []string{member.ID, mate.ID} {
		if names[id] == "" {
			t.Fatalf("the admin answer lacks human member %s: %#v", id, names)
		}
	}
	if _, ok := names[machine.ID]; ok {
		t.Fatalf("the admin answer carries a machine member: %#v", names)
	}
}

// TestAihubMemberContactsIncludeCaller: only the explicit
// include_caller=true keeps the caller's own row; the default and any
// other value still exclude it, and no machine member appears either way.
func TestAihubMemberContactsIncludeCaller(t *testing.T) {
	e := newSrvEnv(t)
	project, err := e.admin.GetProject("alpha")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	member, err := e.admin.CreateUser("Member Mia", "mia@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	mate, err := e.admin.CreateUser("Mate Mo", "mo@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("mate: %v", err)
	}
	machine, err := e.admin.CreateUser("Machine Max", "", "writer", "machine")
	if err != nil {
		t.Fatalf("machine: %v", err)
	}
	for _, user := range []string{member.ID, mate.ID, machine.ID} {
		if err := e.admin.AddMember(project.ID, user, "writer"); err != nil {
			t.Fatalf("add member %s: %v", user, err)
		}
	}
	memberKey, err := e.admin.CreateAPIKey(member.ID, "member", "")
	if err != nil {
		t.Fatalf("member key: %v", err)
	}
	base := "/api/v1/projects/" + project.ID + "/members"
	for _, tc := range []struct {
		query      string
		wantCaller bool
	}{
		{"", false},
		{"?include_caller=false", false},
		{"?include_caller=1", false},
		{"?include_caller=true", true},
	} {
		code, raw, answer := readContactsAt(t, e, memberKey.Plain, base+tc.query)
		if code != http.StatusOK {
			t.Fatalf("%q read: %d %s", tc.query, code, raw)
		}
		ids := map[string]bool{}
		for _, row := range answer.Contacts {
			if len(row) != 2 {
				t.Fatalf("%q row carries more than id + display_name: %#v", tc.query, row)
			}
			ids[row["id"].(string)] = true
		}
		if ids[member.ID] != tc.wantCaller {
			t.Fatalf("%q: caller present = %v, want %v (%s)", tc.query, ids[member.ID], tc.wantCaller, raw)
		}
		if !ids[mate.ID] || ids[machine.ID] {
			t.Fatalf("%q: want the human co-member and no machine member: %s", tc.query, raw)
		}
	}
}

func TestAihubMemberContactsRefusals(t *testing.T) {
	e := newSrvEnv(t)
	project, err := e.admin.GetProject("alpha")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	member, err := e.admin.CreateUser("Member Mia", "mia@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if err := e.admin.AddMember(project.ID, member.ID, "writer"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	outsider, err := e.admin.CreateUser("Outsider Ola", "ola@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("outsider: %v", err)
	}
	outsiderKey, err := e.admin.CreateAPIKey(outsider.ID, "outsider", "")
	if err != nil {
		t.Fatalf("outsider key: %v", err)
	}
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("beta: %v", err)
	}
	scopedKey, err := e.admin.CreateAPIKey(e.adminID, "scoped", "beta")
	if err != nil {
		t.Fatalf("scoped key: %v", err)
	}

	// Neither member nor admin: the existing concealed 404, with no member
	// id, name or count in the refusal.
	code, raw, _ := readContacts(t, e, outsiderKey.Plain, project.ID)
	if code != http.StatusNotFound {
		t.Fatalf("non-member read: %d %s", code, raw)
	}
	for _, leak := range []string{member.ID, "Member Mia", "contacts", "count"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("the non-member refusal leaks %q: %s", leak, raw)
		}
	}

	// An admin key scoped to another project is refused by the existing
	// project-scope rule and learns nothing about alpha's members.
	code, raw, _ = readContacts(t, e, scopedKey.Plain, project.ID)
	if code != http.StatusForbidden {
		t.Fatalf("scoped read: %d %s", code, raw)
	}
	for _, leak := range []string{member.ID, "Member Mia", "contacts"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("the scoped refusal leaks %q: %s", leak, raw)
		}
	}
}
