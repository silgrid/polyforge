package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// TestMemorySearchMatchAny: the search face's match= mode. match=any
// answers a long task-description query (the daemon's claim-time
// recall) that the default AND mode answers nothing for, with the
// project scope (B5), membership gate and limit still enforced; an
// unknown mode is a 400, never a silent AND.
func TestMemorySearchMatchAny(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	alphaID := projectIDOfName(t, e, "alpha")
	mk := func(project, content string) {
		t.Helper()
		if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": project, "type": "experience.debug", "visibility": "project", "content": content,
		}, nil); code != http.StatusCreated {
			t.Fatalf("create memory: %d %s", code, raw)
		}
	}
	mk("alpha", "the provider retry ladder must back off exponentially")
	mk("alpha", "a daemon restart note")
	mk("beta", "beta also knows about the retry ladder")

	long := "Fix the flaky provider retry ladder in the daemon\n\nThe step agent gives up after two attempts."
	type res struct {
		Items []struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		} `json:"items"`
	}
	search := func(key, params string) (int, res, string) {
		t.Helper()
		var r res
		code, raw := e.do(t, "GET", "/api/v1/memories/search?"+params, key, nil, &r)
		return code, r, string(raw)
	}
	q := url.QueryEscape(long)

	if code, r, raw := search(e.key, "q="+q+"&project=alpha"); code != 200 || len(r.Items) != 0 {
		t.Fatalf("default (AND) mode on a long query: %d %s, want 200 with none", code, raw)
	}
	if code, r, raw := search(e.key, "q="+q+"&project=alpha&match=all"); code != 200 || len(r.Items) != 0 {
		t.Fatalf("match=all on a long query: %d %s, want 200 with none", code, raw)
	}
	code, r, raw := search(e.key, "q="+q+"&project=alpha&match=any")
	if code != 200 || len(r.Items) != 2 {
		t.Fatalf("match=any: %d %s, want both alpha memories", code, raw)
	}
	for _, it := range r.Items {
		if it.ProjectID != alphaID {
			t.Fatalf("match=any leaked a %s memory past project=alpha", it.ProjectID)
		}
	}
	if code, r, raw := search(e.key, "q="+q+"&project=alpha&match=any&limit=1"); code != 200 || len(r.Items) != 1 {
		t.Fatalf("match=any limit=1: %d %s", code, raw)
	}
	// Unscoped, the admin sees all three projects' matches.
	if code, r, raw := search(e.key, "q="+q+"&match=any"); code != 200 || len(r.Items) != 3 {
		t.Fatalf("match=any unscoped: %d %s, want 3", code, raw)
	}
	// A non-member sees none of them in any mode.
	outsider, _ := e.admin.CreateUser("any-stranger", "any-stranger@example.com", "writer", "human")
	okey, _ := e.admin.CreateAPIKey(outsider.ID, "o", "")
	if code, r, raw := search(okey.Plain, "q="+q+"&match=any"); code != 200 || len(r.Items) != 0 {
		t.Fatalf("non-member match=any: %d %s, want 200 with none", code, raw)
	}
	// Stopword-only query: no hits, no error.
	if code, r, raw := search(e.key, "q=the+and+of&match=any"); code != 200 || len(r.Items) != 0 {
		t.Fatalf("stopword-only match=any: %d %s", code, raw)
	}
	// Unknown mode: refused.
	if code, _, raw := search(e.key, "q=ladder&match=some"); code != http.StatusBadRequest {
		t.Fatalf("match=some: %d %s, want 400", code, raw)
	}
}
