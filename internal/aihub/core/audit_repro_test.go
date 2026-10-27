package core

// Regression tests for the 2026-10-13 audit item B1 (polyforge#146):
// a revoked API key records the revocation time in revoked_at.

import (
	"testing"
	"time"
)

// B1: RevokeAPIKey must write the revocation TIME into revoked_at.
// Before the fix RevokeAPIKey bound keyID to BOTH placeholders of
// `UPDATE api_keys SET revoked_at=? WHERE id=?1`, so revoked_at held
// the key id instead of a timestamp.
func TestReproB1RevokedAtIsTimestamp(t *testing.T) {
	env := newEnv(t)

	issued, err := env.Admin.CreateAPIKey(env.UserIDB, "b1-repro", "")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	// Precondition: revocation succeeds at the API level.
	if err := env.Admin.RevokeAPIKey(issued.Token.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	var revokedAt string
	if err := env.DB.QueryRow(`SELECT revoked_at FROM api_keys WHERE id=?1`,
		issued.Token.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}
	if revokedAt == "" {
		t.Fatal("B1: revoked_at is empty after revocation")
	}
	if revokedAt == issued.Token.ID {
		t.Fatalf("B1: revoked_at stores the KEY ID %q instead of a timestamp — "+
			"the audit-reported double binding of keyID to both placeholders", revokedAt)
	}
	if _, err := time.Parse(time.RFC3339, revokedAt); err != nil {
		t.Fatalf("B1: revoked_at %q is not an RFC3339 timestamp: %v", revokedAt, err)
	}
}
