package server

// The §4.6 repo inventory on the HTTP surface: a create carrying a
// client url, any key other than name (a base commit included) or a name
// that is not one of the project's repos is refused with CORE-400 and
// persists nothing; a PATCH of the frozen inventory is refused IMMUTABLE.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// refuseShape asserts the typed refusal envelope: HTTP 400, CORE-400,
// machine-readable reason.
func refuseShape(t *testing.T, code int, raw []byte, reason string) {
	t.Helper()
	if code != http.StatusBadRequest {
		t.Fatalf("want HTTP 400 (CORE-400 %s), got %d %s", reason, code, raw)
	}
	var envl api.Error
	if err := json.Unmarshal(raw, &envl); err != nil {
		t.Fatalf("bad error envelope: %s (%v)", raw, err)
	}
	if envl.Code != api.ErrCoreBadInput {
		t.Fatalf("want error code %s, got %q (raw: %s)", api.ErrCoreBadInput, envl.Code, raw)
	}
	var m map[string]string
	if len(envl.Extra) == 0 || json.Unmarshal(envl.Extra, &m) != nil || m["reason"] != reason {
		t.Fatalf("want reason %s in extra, got %s", reason, raw)
	}
}

// nothingPersisted asserts project alpha holds no work items at all.
func nothingPersisted(t *testing.T, e *srvEnv) {
	t.Helper()
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	code, raw := e.do(t, "GET", "/api/v1/work-items?project=alpha", e.key, nil, &list)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, raw)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("refused create must not persist; found %d work item(s): %s", list.Total, raw)
	}
}

func TestWIRepoInventoryHTTPRefusals(t *testing.T) {
	const repoURL = "https://example.com/polyforge.git"

	t.Run("create/base-in-entry", func(t *testing.T) {
		e := newSrvEnv(t)
		code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
			"project": "alpha", "goal": "a frozen base is not supported",
			"repo_inventory": []map[string]any{{"name": "polyforge", "base": "abc123"}},
		}, nil)
		var envl api.Error
		if code != http.StatusBadRequest || json.Unmarshal(raw, &envl) != nil || envl.Code != api.ErrCoreBadInput ||
			!strings.Contains(envl.Detail, "unknown repo_inventory key") {
			t.Fatalf("a base entry must be refused CORE-400 as an unknown key, got %d %s", code, raw)
		}
		nothingPersisted(t, e)
	})

	t.Run("create/removed-base-array", func(t *testing.T) {
		e := newSrvEnv(t)
		code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
			"project": "alpha", "goal": "the removed repo_inventory_base is refused, never dropped",
			"repo_inventory":      []map[string]any{{"name": "polyforge"}},
			"repo_inventory_base": []map[string]any{{"name": "polyforge", "base": "abc123"}},
		}, nil)
		refuseShape(t, code, raw, "INVENTORY_BASE_UNSUPPORTED")
		nothingPersisted(t, e)
		for _, empty := range []any{nil, []any{}} {
			code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
				"project": "alpha", "goal": "an empty base array means no bases",
				"repo_inventory_base": empty,
			}, nil)
			if code >= 300 {
				t.Fatalf("repo_inventory_base %v must mean no bases, got %d %s", empty, code, raw)
			}
		}
	})

	t.Run("create/client-url", func(t *testing.T) {
		e := newSrvEnv(t)
		code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
			"project": "alpha", "goal": "the daemon never clones a filer-supplied url",
			"repo_inventory": []map[string]any{{"name": "polyforge", "url": repoURL}},
		}, nil)
		refuseShape(t, code, raw, "INVENTORY_URL_NOT_ALLOWED")
		nothingPersisted(t, e)
	})

	t.Run("create/unknown-project-repo", func(t *testing.T) {
		e := newSrvEnv(t)
		code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
			"project": "alpha", "goal": "a name-only entry must be a project repo",
			"repo_inventory": []map[string]any{{"name": "nope"}},
		}, nil)
		refuseShape(t, code, raw, "REPO_NOT_IN_PROJECT")
		nothingPersisted(t, e)
	})

	t.Run("patch/frozen", func(t *testing.T) {
		e := newSrvEnv(t)
		var created struct {
			ID string `json:"id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
			map[string]any{"project": "alpha", "goal": "patch carrier"}, &created); code != http.StatusCreated || created.ID == "" {
			t.Fatalf("seed create: %d %s", code, raw)
		}
		code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+created.ID, e.key, map[string]any{
			"repo_inventory": []map[string]any{{"name": "polyforge"}},
		}, nil)
		refuseShape(t, code, raw, "IMMUTABLE")
	})
}
