package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestProjectMemberContactsByNameAndBlankName: the member face resolves
// the project by name as well as id, keeps a blank display name as an
// empty string (never the id), answers {"contacts":[]} — not null — when
// the caller has no co-member, and conceals an unknown project as 404.
func TestProjectMemberContactsByNameAndBlankName(t *testing.T) {
	e := newSrvEnv(t)
	project, err := e.admin.GetProject("alpha")
	if err != nil {
		t.Fatal(err)
	}

	code, raw := e.do(t, "GET", "/api/v1/projects/alpha/members", e.key, nil, nil)
	if code != http.StatusOK || string(raw) == "" {
		t.Fatalf("empty project read: %d %s", code, raw)
	}
	var empty map[string]json.RawMessage
	if err := json.Unmarshal(raw, &empty); err != nil || string(empty["contacts"]) != "[]" {
		t.Fatalf("empty answer = %s, want {\"contacts\":[]}", raw)
	}

	blank, err := e.admin.CreateUser("Blank", "blank@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE users SET display_name='' WHERE id=?1`, blank.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.admin.AddMember(project.ID, blank.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Contacts []struct {
			ID          string  `json:"id"`
			DisplayName *string `json:"display_name"`
		} `json:"contacts"`
	}
	code, raw = e.do(t, "GET", "/api/v1/projects/alpha/members", e.key, nil, &answer)
	if code != http.StatusOK {
		t.Fatalf("by-name read: %d %s", code, raw)
	}
	if len(answer.Contacts) != 1 || answer.Contacts[0].ID != blank.ID ||
		answer.Contacts[0].DisplayName == nil || *answer.Contacts[0].DisplayName != "" {
		t.Fatalf("blank-name answer = %s", raw)
	}

	code, raw = e.do(t, "GET", "/api/v1/projects/nope/members", e.key, nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown project: %d %s", code, raw)
	}
}
