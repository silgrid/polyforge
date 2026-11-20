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

// WorkstationInit runs the combined first-boot identity transaction.
func (m *AdminManager) WorkstationInit(req WorkstationInitRequest) (*WorkstationInitResult, error) {
	return nil, Fail(api.ErrCoreBadInput, "", "workstation init: not implemented yet")
}
