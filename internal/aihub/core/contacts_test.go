package core

import (
	"reflect"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestListProjectMemberContactsScope: the member face returns exactly the
// project's human members other than the caller, ordered by display name,
// as id + display name; machine members, the caller and non-members never
// appear, and an empty display name is kept for the client's placeholder.
func TestListProjectMemberContactsScope(t *testing.T) {
	env := newEnv(t)
	zed, err := env.Admin.CreateUser("Zed", "zed@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	amy, err := env.Admin.CreateUser("Amy", "amy@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	blank, err := env.Admin.CreateUser("Blank", "blank@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`UPDATE users SET display_name='' WHERE id=?1`, blank.ID); err != nil {
		t.Fatal(err)
	}
	bot, err := env.Admin.CreateUser("Bot", "", "writer", "machine")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{env.UserID, zed.ID, amy.ID, blank.ID, bot.ID} {
		if err := env.Admin.AddMember(env.Project, id, "writer"); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	// UserIDB ("second") is not a member of alpha and must not appear.

	got, err := env.Admin.ListProjectMemberContacts(env.Project, env.UserID, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []api.ProjectMemberContact{
		{ID: blank.ID, DisplayName: ""},
		{ID: amy.ID, DisplayName: "Amy"},
		{ID: zed.ID, DisplayName: "Zed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contacts = %+v, want %+v", got, want)
	}

	// A global admin that is not a member sees every human member.
	got, err = env.Admin.ListProjectMemberContacts(env.Project, env.UserIDB, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("non-member caller contacts = %+v, want the 4 human members", got)
	}
	for _, c := range got {
		if c.ID == bot.ID || c.ID == env.UserIDB {
			t.Fatalf("contacts carry a machine or the caller: %+v", got)
		}
	}

	// The explicit include-caller read keeps the caller's own row; the
	// machine member is still absent.
	got, err = env.Admin.ListProjectMemberContacts(env.Project, env.UserID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("include-caller contacts = %+v, want the 4 human members", got)
	}
	foundCaller := false
	for _, c := range got {
		if c.ID == bot.ID {
			t.Fatalf("include-caller contacts carry the machine member: %+v", got)
		}
		foundCaller = foundCaller || c.ID == env.UserID
	}
	if !foundCaller {
		t.Fatalf("include-caller contacts lack the caller %s: %+v", env.UserID, got)
	}
}

// TestListProjectMemberContactsEmpty: a project whose only member is the
// caller answers an empty, non-nil list.
func TestListProjectMemberContactsEmpty(t *testing.T) {
	env := newEnv(t)
	if err := env.Admin.AddMember(env.Project, env.UserID, "writer"); err != nil {
		t.Fatal(err)
	}
	got, err := env.Admin.ListProjectMemberContacts(env.Project, env.UserID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("contacts = %#v, want an empty list", got)
	}
}
