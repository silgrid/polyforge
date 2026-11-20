package core

// workstation_init_test.go — polyforge#176 (84-components-C): the
// combined first-boot identity transaction's behaviour. One function,
// TestWorkstationInit: the four facts land together (user, key, project,
// membership), a retry never duplicates the user or the project, the
// same-named key is revoked and reissued, and the issued plaintext is
// exactly the unrevoked key row of the daily user — never the bootstrap
// admin's (usr_root does not exist in a first-boot-initialized store).

import (
	"testing"
)

// wsInitReq is the request shape every subtest starts from: the
// workstation's fixed business constants (the values the local init
// entry passes; the derivation is the ticket's Q3 — a daily user, a
// writer, the local project, a fixed key name).
var wsInitReq = WorkstationInitRequest{
	Email:       "workstation@polyforge.local",
	DisplayName: "workstation",
	ProjectName: "workstation",
	KeyName:     "workstation-first-boot",
}

// wsInitDB opens one fresh core at dir.
func wsInitDB(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// wsInitRow is one api_keys read.
type wsInitRow struct {
	id      string
	userID  string
	revoked *string
}

// wsInitKeys reads the daily user's key rows, newest first.
func wsInitKeys(t *testing.T, db *DB, userID string) []wsInitRow {
	t.Helper()
	rows, err := db.Query(`SELECT id, user_id, revoked_at FROM api_keys WHERE user_id=?1 ORDER BY created_at DESC`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []wsInitRow
	for rows.Next() {
		var r wsInitRow
		if err := rows.Scan(&r.id, &r.userID, &r.revoked); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorkstationInit(t *testing.T) {
	t.Run("one call establishes the four facts", func(t *testing.T) {
		db := wsInitDB(t, t.TempDir())
		m := NewAdminManager(db)
		res, err := m.WorkstationInit(wsInitReq)
		if err != nil {
			t.Fatalf("WorkstationInit on an empty store must succeed: %v", err)
		}
		// the user: found by the fixed email, a human writer (the
		// ticket's 普通用户 — CreateUser's own rules, applied by the
		// transaction)
		u, err := m.GetUser(res.UserID)
		if err != nil {
			t.Fatalf("the created user must be readable: %v", err)
		}
		if u.Email != wsInitReq.Email || u.Role != "writer" || u.UserType != "human" {
			t.Fatalf("the daily user = %+v, want email %s, role writer, kind human", u, wsInitReq.Email)
		}
		if !res.UserCreated {
			t.Fatal("on an empty store the user must be created (UserCreated)")
		}
		// the project: found by name, active, and the user is its
		// maintainer (the ticket's 维护者)
		p, err := m.GetProject(res.ProjectID)
		if err != nil {
			t.Fatalf("the created project must be readable: %v", err)
		}
		if p.Name != wsInitReq.ProjectName || p.Status != "active" {
			t.Fatalf("the project = %+v, want name %s active", p, wsInitReq.ProjectName)
		}
		if !res.ProjectCreated {
			t.Fatal("on an empty store the project must be created (ProjectCreated)")
		}
		var role string
		if err := db.QueryRow(`SELECT role FROM project_members WHERE project_id=?1 AND user_id=?2`,
			res.ProjectID, res.UserID).Scan(&role); err != nil || role != "maintainer" {
			t.Fatalf("the membership = (%s, %v), want maintainer", role, err)
		}
		// the key: the returned plaintext is exactly one unrevoked key
		// row of the daily user (Sha256Hex is the store's own digest,
		// the same one the HTTP auth path checks)
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=?1 AND key_hash=?2 AND revoked_at IS NULL AND name=?3`,
			res.UserID, Sha256Hex(res.PlainKey), wsInitReq.KeyName).Scan(&n); err != nil || n != 1 {
			t.Fatalf("the plaintext must match exactly one unrevoked %s key of the user (%d, %v)", wsInitReq.KeyName, n, err)
		}
	})

	t.Run("a retry never duplicates the user or the project, and reissues the key", func(t *testing.T) {
		db := wsInitDB(t, t.TempDir())
		m := NewAdminManager(db)
		first, err := m.WorkstationInit(wsInitReq)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		second, err := m.WorkstationInit(wsInitReq)
		if err != nil {
			t.Fatalf("retry (a lost plaintext, Q2): %v", err)
		}
		if second.UserID != first.UserID {
			t.Fatalf("retry must find the same user (got %s, first %s) — the fixed email must never duplicate", second.UserID, first.UserID)
		}
		if second.UserCreated {
			t.Fatal("a retry must not create a second user")
		}
		if second.ProjectID != first.ProjectID || second.ProjectCreated {
			t.Fatalf("retry must find the same project (got %s created=%v, first %s)", second.ProjectID, second.ProjectCreated, first.ProjectID)
		}
		// the fixed-name discipline (Q2): the first key is revoked, the
		// second is fresh, and both belong to the one user
		keys := wsInitKeys(t, db, first.UserID)
		if len(keys) != 2 {
			t.Fatalf("after a retry the user must carry exactly the two same-named keys (want 2 rows, got %d)", len(keys))
		}
		if keys[0].id != second.KeyID || keys[0].revoked != nil {
			t.Fatalf("the retry's key must be the one fresh unrevoked row (id %s revoked %v)", keys[0].id, keys[0].revoked)
		}
		if keys[1].id != first.KeyID || keys[1].revoked == nil {
			t.Fatalf("the first key must be revoked by the retry (id %s revoked %v)", keys[1].id, keys[1].revoked)
		}
		var users, projects int
		if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 1 {
			t.Fatalf("users count = %d (%v), want exactly 1 — a retry must not duplicate the user", users, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil || projects != 1 {
			t.Fatalf("projects count = %d (%v), want exactly 1", projects, err)
		}
	})

	t.Run("the issued key is not the bootstrap admin's and usr_root is never created", func(t *testing.T) {
		db := wsInitDB(t, t.TempDir())
		m := NewAdminManager(db)
		res, err := m.WorkstationInit(wsInitReq)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id='usr_root'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the first-boot transaction must never create usr_root (found %d)", n)
		}
		if res.PlainKey == "" {
			t.Fatal("the result must carry the plaintext exactly once")
		}
		// an empty store's only key belongs to the daily user: the
		// bootstrap admin's key shape (usr_root) cannot exist here
		var foreign int
		if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id<>?1`, res.UserID).Scan(&foreign); err != nil || foreign != 0 {
			t.Fatalf("a fresh store after WorkstationInit must carry keys of the daily user only (found %d foreign: %v)", foreign, err)
		}
	})

	t.Run("bad input is refused before anything is written", func(t *testing.T) {
		db := wsInitDB(t, t.TempDir())
		m := NewAdminManager(db)
		for name, req := range map[string]WorkstationInitRequest{
			"no email":         {DisplayName: "workstation", ProjectName: "workstation", KeyName: "k"},
			"no display name":  {Email: "workstation@polyforge.local", ProjectName: "workstation", KeyName: "k"},
			"no project name":  {Email: "workstation@polyforge.local", DisplayName: "workstation", KeyName: "k"},
			"no key name":      {Email: "workstation@polyforge.local", DisplayName: "workstation", ProjectName: "workstation"},
			"bad project name": {Email: "workstation@polyforge.local", DisplayName: "workstation", ProjectName: "Not A Name", KeyName: "k"},
		} {
			if _, err := m.WorkstationInit(req); err == nil {
				t.Fatalf("%s must be refused", name)
			}
		}
		var users, keys, projects int
		_ = db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
		_ = db.QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&keys)
		_ = db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects)
		if users+keys+projects != 0 {
			t.Fatalf("a refused transaction must write nothing (users=%d keys=%d projects=%d)", users, keys, projects)
		}
	})

	t.Run("an existing project by the same name is adopted, not duplicated", func(t *testing.T) {
		db := wsInitDB(t, t.TempDir())
		m := NewAdminManager(db)
		pre, err := m.CreateProject(CreateProjectRequest{Name: "workstation"})
		if err != nil {
			t.Fatal(err)
		}
		res, err := m.WorkstationInit(wsInitReq)
		if err != nil {
			t.Fatalf("WorkstationInit over an existing project: %v", err)
		}
		if res.ProjectID != pre.ID || res.ProjectCreated {
			t.Fatalf("the existing project must be adopted (got %s created=%v, want %s)", res.ProjectID, res.ProjectCreated, pre.ID)
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("projects = %d (%v), want 1 — the transaction must not duplicate a same-named project", n, err)
		}
	})
}
