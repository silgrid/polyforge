package core

// §4.6 repo-inventory tests: the inventory freezes at create into
// work_items.repo_inventory as project repo NAMES only; every refusal is
// typed and persists nothing (a url, a subdir, a base, an unknown key, a
// name outside the project, a duplicate); the claim resolves the URLs
// from the project at claim time and serves only the declared repos
// (re-claim included); a WI without an inventory keeps getting every
// project repo.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// names is a create-body inventory of name-only entries.
func names(ns ...string) RepoInventoryInput {
	in := RepoInventoryInput{}
	for _, n := range ns {
		in = append(in, map[string]json.RawMessage{"name": json.RawMessage(`"` + n + `"`)})
	}
	return in
}

// entry is one raw create-body inventory entry.
func entry(t *testing.T, kv map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = b
	}
	return out
}

// invCreate is an execution-ticket create request on project bravo.
func invCreate(env *testEnv, in RepoInventoryInput) CreateRequest {
	return CreateRequest{
		Project: "bravo", Goal: "repo-backed wi", Reporter: env.UserID,
		BundleName: strPtrTest("demo-bundle"), RepoInventory: in,
	}
}

// reposProject creates project bravo with three repos.
func reposProject(t *testing.T, env *testEnv) {
	t.Helper()
	if _, err := env.Admin.CreateProject(CreateProjectRequest{
		Name: "bravo",
		Repos: []api.ProjectRepo{
			{Name: "app", URL: "file:///srv/app.git"},
			{Name: "lib", URL: "file:///srv/lib.git"},
			{Name: "big", URL: "file:///srv/big.git"},
		},
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
}

// wantTypedRefusal asserts the typed envelope and that nothing persisted.
func wantTypedRefusal(t *testing.T, env *testEnv, err error, code, reason, nameSub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s %s refusal, got success", code, reason)
	}
	if !IsCode(err, code) {
		t.Fatalf("want code %s, got %v", code, err)
	}
	if got := ErrorReason(err); got != reason {
		t.Fatalf("want reason %s, got %q (%v)", reason, got, err)
	}
	if nameSub != "" && !strings.Contains(err.Error(), nameSub) {
		t.Fatalf("refusal must name the offending repo %q, got %v", nameSub, err)
	}
	wis, lerr := env.WIs.List(ListFilters{Project: "bravo"})
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	if len(wis) != 0 {
		t.Fatalf("refused create must not persist; found %d work item(s)", len(wis))
	}
}

// TestWIRepoInventoryRefusals: everything but a unique project repo
// name is refused, typed, with nothing persisted.
func TestWIRepoInventoryRefusals(t *testing.T) {
	env := newEnv(t)
	reposProject(t, env)
	for _, c := range []struct {
		name   string
		in     RepoInventoryInput
		reason string
		sub    string
	}{
		// a client url is never cloned — not even the project's own one
		{"url", RepoInventoryInput{entry(t, map[string]any{"name": "app", "url": "file:///etc"})}, "INVENTORY_URL_NOT_ALLOWED", "app"},
		{"url-same-as-project", RepoInventoryInput{entry(t, map[string]any{"name": "app", "url": "file:///srv/app.git"})}, "INVENTORY_URL_NOT_ALLOWED", "app"},
		{"subdir", RepoInventoryInput{entry(t, map[string]any{"name": "app", "subdir": "x"})}, "INVENTORY_SUBDIR_UNSUPPORTED", "app"},
		{"base", RepoInventoryInput{entry(t, map[string]any{"name": "app", "base": "abc123"})}, "", "app"},
		{"unknown-key", RepoInventoryInput{entry(t, map[string]any{"name": "app", "ref": "main"})}, "", "app"},
		{"no-name", RepoInventoryInput{entry(t, map[string]any{"url": "file:///etc"})}, "", ""},
		{"not-in-project", names("app", "nope"), "REPO_NOT_IN_PROJECT", "nope"},
		{"duplicate", names("app", "app"), "INVENTORY_DUPLICATE_NAME", "app"},
		{"too-many", names("app", "lib", "big", "a", "b", "c", "d", "e", "f"), "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := env.WIs.Create(invCreate(env, c.in))
			wantTypedRefusal(t, env, err, api.ErrCoreBadInput, c.reason, c.sub)
		})
	}
}

func TestWIRepoInventoryWellFormedCreatePersistsAndServes(t *testing.T) {
	env := newEnv(t)
	reposProject(t, env)
	wi, err := env.WIs.Create(invCreate(env, names("lib", "app")))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var stored string
	if err := env.DB.QueryRow(`SELECT repo_inventory FROM work_items WHERE id=?1`, wi.ID).Scan(&stored); err != nil {
		t.Fatalf("read column: %v", err)
	}
	if want := `[{"name":"lib"},{"name":"app"}]`; stored != want {
		t.Fatalf("stored inventory\n got %s\nwant %s (names only)", stored, want)
	}
	view, err := env.WIs.Get(wi.ID, GetOptions{})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	raw, _ := json.Marshal(view)
	if !strings.Contains(string(raw), `"repo_inventory":`+stored) {
		t.Fatalf("view JSON must carry the frozen inventory, got %s", raw)
	}
	// A WI without an inventory stores NULL and omits the key.
	plain := mustCreateWI(t, env, "no inventory", nil, "")
	var col *string
	if err := env.DB.QueryRow(`SELECT repo_inventory FROM work_items WHERE id=?1`, plain).Scan(&col); err != nil || col != nil {
		t.Fatalf("plain WI column = %v (%v), want NULL", col, err)
	}
	pv, err := env.WIs.Get(plain, GetOptions{})
	if err != nil {
		t.Fatalf("plain view: %v", err)
	}
	if raw, _ := json.Marshal(pv); strings.Contains(string(raw), "repo_inventory") {
		t.Fatalf("plain WI must omit repo_inventory, got %s", raw)
	}
}

func TestWIRepoInventoryPatchRefused(t *testing.T) {
	env := newEnv(t)
	id := mustCreateWI(t, env, "patch carrier", nil, "")
	_, err := env.WIs.Patch(id, map[string]json.RawMessage{
		"repo_inventory": json.RawMessage(`[{"name":"app"}]`),
	})
	if !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "IMMUTABLE" {
		t.Fatalf("want IMMUTABLE for an inventory patch, got %v", err)
	}
	inv, err := repoInventoryOf(env.DB, id)
	if err != nil || len(inv) != 0 {
		t.Fatalf("refused patch must not freeze an inventory, got %+v (%v)", inv, err)
	}
}

// TestClaimServesDeclaredInventoryOnly: the claim answers exactly the
// declared repos with the project's CURRENT url — on the first claim and
// on the re-claim after a blocked→resume — while a WI without an
// inventory gets every project repo.
func TestClaimServesDeclaredInventoryOnly(t *testing.T) {
	env := newEnv(t)
	reposProject(t, env)
	wi, err := env.WIs.Create(invCreate(env, names("lib")))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	m := NewSessionManager(env.DB)
	wantLib := func(res *api.SessionClaim, url string) {
		t.Helper()
		if len(res.Repos) != 1 || res.Repos[0].Name != "lib" || res.Repos[0].URL != url {
			t.Fatalf("claim repos = %+v, want only lib at %s", res.Repos, url)
		}
	}
	secret := strings.Repeat("4", 32)
	res, err := m.Claim(wi.ID, api.SessionClaimRequest{SessionID: secret}, env.UserID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	wantLib(res, "file:///srv/lib.git")
	if err := m.End(wi.ID, api.SessionEndRequest{SessionID: secret, Status: api.StatusBlocked, Note: "x"}, env.UserID); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := env.WIs.ResumeBlocked(wi.ID, env.UserID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// the project moves lib: the re-claim follows the project record
	if _, err := env.DB.Exec(`UPDATE projects SET repos=?1 WHERE name='bravo'`,
		`[{"name":"lib","url":"file:///srv/lib-moved.git"},{"name":"app","url":"file:///srv/app.git"}]`); err != nil {
		t.Fatal(err)
	}
	res, err = m.Claim(wi.ID, api.SessionClaimRequest{SessionID: strings.Repeat("5", 32)}, env.UserID)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	wantLib(res, "file:///srv/lib-moved.git")

	plain, err := env.WIs.Create(CreateRequest{Project: "bravo", Goal: "no inventory",
		Reporter: env.UserID, BundleName: strPtrTest("demo-bundle")})
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	res, err = m.Claim(plain.ID, api.SessionClaimRequest{SessionID: strings.Repeat("6", 32)}, env.UserID)
	if err != nil {
		t.Fatalf("claim plain: %v", err)
	}
	if len(res.Repos) != 2 {
		t.Fatalf("a WI without an inventory must get every project repo, got %+v", res.Repos)
	}
}

// TestClaimRefusesInventoryRepoGone: a declared repo the project no
// longer has refuses the claim typed, and the WI stays queued.
func TestClaimRefusesInventoryRepoGone(t *testing.T) {
	env := newEnv(t)
	reposProject(t, env)
	wi, err := env.WIs.Create(invCreate(env, names("big")))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := env.DB.Exec(`UPDATE projects SET repos='[{"name":"app","url":"file:///srv/app.git"}]' WHERE name='bravo'`); err != nil {
		t.Fatal(err)
	}
	_, err = NewSessionManager(env.DB).Claim(wi.ID, api.SessionClaimRequest{SessionID: strings.Repeat("7", 32)}, env.UserID)
	if !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "INVENTORY_REPO_GONE" || !strings.Contains(err.Error(), "big") {
		t.Fatalf("want CORE-409 INVENTORY_REPO_GONE naming big, got %v", err)
	}
	got, err := env.WIs.Get(wi.ID, GetOptions{})
	if err != nil || got.Status != api.StatusQueued {
		t.Fatalf("refused claim must leave the WI queued, got %+v (%v)", got, err)
	}
}

// TestReadyQueueSkipsInventoryRepoGone: a queued WI whose inventory
// names a repo the project no longer has leaves the executable segment
// (its claim would only be refused INVENTORY_REPO_GONE) and comes back
// when the repo is restored; a WI without an inventory is unaffected.
func TestReadyQueueSkipsInventoryRepoGone(t *testing.T) {
	env := newEnv(t)
	reposProject(t, env)
	withInv, err := env.WIs.Create(invCreate(env, names("big")))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	plain, err := env.WIs.Create(invCreate(env, nil))
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	items := func() map[string]bool {
		t.Helper()
		q, err := env.WIs.ReadyQueue("bravo")
		if err != nil {
			t.Fatalf("ready queue: %v", err)
		}
		out := map[string]bool{}
		for _, it := range q.Items {
			out[it.ID] = true
		}
		return out
	}
	setRepos := func(js string) {
		t.Helper()
		if _, err := env.DB.Exec(`UPDATE projects SET repos=?1 WHERE name='bravo'`, js); err != nil {
			t.Fatal(err)
		}
	}
	if got := items(); !got[withInv.ID] || !got[plain.ID] {
		t.Fatalf("both WIs must be ready while big is a project repo: %v", got)
	}
	setRepos(`[{"name":"app","url":"file:///srv/app.git"}]`)
	if got := items(); got[withInv.ID] || !got[plain.ID] {
		t.Fatalf("big removed: want only the plain WI ready, got %v", got)
	}
	setRepos(`[{"name":"app","url":"file:///srv/app.git"},{"name":"big","url":"file:///srv/big2.git"}]`)
	if got := items(); !got[withInv.ID] || !got[plain.ID] {
		t.Fatalf("big restored: both WIs must be ready again, got %v", got)
	}
}
