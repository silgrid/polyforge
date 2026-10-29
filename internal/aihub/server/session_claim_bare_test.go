package server

// session_claim_bare_test.go: B5 — the claim and attach routes ask the
// session gate before creating the work item's bare repo, so a refused
// claim (terminal, registration-only) or attach leaves no empty repo.

import (
	"os"
	"testing"
)

func repoExists(e *srvEnv, id string) bool {
	_, err := os.Stat(e.srv.Git.RepoDir(id))
	return err == nil
}

func TestRefusedClaimCreatesNoBareRepo(t *testing.T) {
	e := newSrvEnv(t)
	var reg struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "alpha", "goal": "registry only"}, &reg); code != 201 {
		t.Fatalf("create reg: %d %s", code, raw)
	}
	if code, raw, _ := sessClaim(t, e, reg.ID, "b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5", ""); code != 409 {
		t.Fatalf("registration claim: %d %s", code, raw)
	} else if _, reason := errEnvelope(t, raw); reason != "NOT_CLAIMABLE" {
		t.Fatalf("registration claim reason %q: %s", reason, raw)
	}
	if repoExists(e, reg.ID) {
		t.Fatal("a refused registration claim created a bare repo")
	}

	term := createExecWIOverHTTP(t, e, "terminal")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+term+"/cancel", e.key, map[string]any{}, nil); code != 200 {
		t.Fatalf("cancel: %d %s", code, raw)
	}
	if code, raw, _ := sessClaim(t, e, term, "b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6", ""); code != 410 {
		t.Fatalf("terminal claim: %d %s", code, raw)
	} else if _, reason := errEnvelope(t, raw); reason != "TERMINAL_STATE" {
		t.Fatalf("terminal claim reason %q: %s", reason, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+term+"/session-attach", e.key,
		map[string]any{"session_id": "b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7"}, nil); code != 403 {
		t.Fatalf("attach to a terminal work item: %d %s", code, raw)
	}
	if repoExists(e, term) {
		t.Fatal("a refused claim or attach on a terminal work item created a bare repo")
	}

	ok := createExecWIOverHTTP(t, e, "eligible")
	if code, raw, _ := sessClaim(t, e, ok, "b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8", ""); code != 200 {
		t.Fatalf("eligible claim: %d %s", code, raw)
	}
	if !repoExists(e, ok) {
		t.Fatal("an eligible claim found no bare repo")
	}
}
