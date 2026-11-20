package core

// workstation_init.go — polyforge#176 (84-components-C): the combined
// first-boot identity transaction. The four facts the workstation's first
// boot needs — the daily user, its API key, the local project and the
// maintainer membership — are one all-or-nothing transaction, because
// the individual AdminManager methods each commit on their own and an
// interruption between them would leave a half-initialized hub (a user
// without a key, a project without its member).
//
// The retry semantics are the ticket's settled Q2: the user is found by
// its fixed business identity (the email), never duplicated; the key
// carries a fixed name, and a retry revokes the same-named unrevoked
// keys before issuing a fresh one — the plaintext only exists once, at
// issuance, so a retry that lost it simply reissues.

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// WorkstationInitRequest is the first-boot identity transaction's input.
// Every field is a fixed business constant of the workstation (the
// first boot never takes them from an operator), so the only callers
// are the local init entry (internal/aihub) and the tests.
type WorkstationInitRequest struct {
	// Email is the daily user's fixed identity: the find-or-create key.
	Email string
	// DisplayName is the created user's display name.
	DisplayName string
	// ProjectName is the local project's name (CreateProject's rules).
	ProjectName string
	// KeyName is the API key's fixed name: the revoke-and-reissue target.
	KeyName string
}

// WorkstationInitResult is what one transaction established.
type WorkstationInitResult struct {
	// UserID is the daily user's id (found or created).
	UserID string
	// UserCreated says the user was created by this call (false: found).
	UserCreated bool
	// ProjectID is the project's id (found or created).
	ProjectID string
	// ProjectCreated says the project was created by this call.
	ProjectCreated bool
	// KeyID is the freshly issued key's id.
	KeyID string
	// PlainKey is the issued key's plaintext — it exists exactly here,
	// once, at issuance; the caller folds it into the machine's config
	// and never logs or persists it anywhere else.
	PlainKey string
}

// WorkstationInit runs the combined first-boot identity transaction:
// the daily user (found by its fixed email, created as a human writer
// when absent), the local project (found by name, created when absent),
// the maintainer membership, and one fresh same-named API key — all
// inside ONE transaction, so an interruption at any point leaves none
// of them (or, on a retry, the user and project it already wrote,
// found again — never duplicated).
//
// The retry discipline is the ticket's settled Q2: the key's plaintext
// exists only at issuance, so a retry that lost it revokes the
// same-named unrevoked keys of the user and issues a fresh one. A
// caller that completed its work (the first boot's done phase) never
// calls this again.
func (m *AdminManager) WorkstationInit(req WorkstationInitRequest) (*WorkstationInitResult, error) {
	if strings.TrimSpace(req.DisplayName) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "display_name required")
	}
	if strings.TrimSpace(req.Email) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "email required (the fixed business identity a retry finds the user by)")
	}
	if strings.TrimSpace(req.KeyName) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "key_name required (the fixed name a retry revokes and reissues)")
	}
	if !projectNameRe.MatchString(req.ProjectName) {
		return nil, Fail(api.ErrCoreBadInput, "", "project name must be 1-40 chars of [a-z0-9-_]")
	}

	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// the daily user: found by the fixed email, created when absent —
	// the same INSERT shape CreateUser uses (role writer, kind human:
	// the ticket's 普通用户, never an admin).
	res := &WorkstationInitResult{}
	if err := tx.QueryRow(`SELECT id FROM users WHERE email=?1`, req.Email).Scan(&res.UserID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		id, err := NewID("usr_")
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO users(id, display_name, email, kind, role, created_at)
			VALUES (?1,?2,?3,'human','writer',?4)`, id, req.DisplayName, req.Email, Now()); err != nil {
			return nil, err
		}
		res.UserID, res.UserCreated = id, true
	}

	// the local project: found by name, created when absent. A same-named
	// project inserted concurrently is adopted by the re-read below (the
	// unique violation is the CREATE-side race, never a duplicate).
	if err := tx.QueryRow(`SELECT id FROM projects WHERE name=?1`, req.ProjectName).Scan(&res.ProjectID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		id, err := NewID("prj_")
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO projects(id, name, description, status, repos, created_at, updated_at)
			VALUES (?1,?2,'','active','[]',?3,?3)`, id, req.ProjectName, Now()); err != nil {
			if isUniqueViolation(err) {
				if err := tx.QueryRow(`SELECT id FROM projects WHERE name=?1`, req.ProjectName).Scan(&res.ProjectID); err != nil {
					return nil, err
				}
			} else {
				return nil, err
			}
		} else {
			res.ProjectID, res.ProjectCreated = id, true
		}
	}

	// the maintainer membership (upsert: a retry keeps exactly one row).
	if err := upsertMember(tx, res.ProjectID, res.UserID, "maintainer"); err != nil {
		return nil, err
	}

	// the key: revoke the same-named unrevoked keys of the user, then
	// issue a fresh one (Q2). The plaintext is returned in memory only.
	if _, err := tx.Exec(`UPDATE api_keys SET revoked_at=?1 WHERE user_id=?2 AND name=?3 AND revoked_at IS NULL`,
		Now(), res.UserID, req.KeyName); err != nil {
		return nil, err
	}
	keyID, err := NewID("key_")
	if err != nil {
		return nil, err
	}
	plain, err := NewSecret(24)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO api_keys(id, user_id, project_id, name, key_hash, created_at)
		VALUES (?1,?2,NULL,?3,?4,?5)`, keyID, res.UserID, req.KeyName, Sha256Hex(plain), Now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	res.KeyID, res.PlainKey = keyID, plain
	return res, nil
}
