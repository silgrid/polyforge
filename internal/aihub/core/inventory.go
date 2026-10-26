package core

// WI repo inventory (reference.md §2.1): the project repos one work item declares
// it works in. It is stored as ONE JSON array of NAMES in
// work_items.repo_inventory (NULL = no inventory): [{"name": "..."}].
// Frozen at create — there is no UPDATE path (a changed inventory means a
// new WI). The claim answer serves it instead of the project's full repo
// list, so a ticket only clones the repos it declared.
//
// Only names are accepted: every URL the claiming daemon clones (with
// the machine's git credentials) comes from projects.repos, resolved at
// claim time — never from the filer. A client-supplied url, a subdir or
// any other key is refused at create instead of being stored.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// RepoDecl is one stored repo_inventory entry: a project repo name.
type RepoDecl struct {
	Name string `json:"name"`
}

// RepoInventoryInput is the create body's repo_inventory: one object
// per entry, kept raw so every key other than name is refused by name
// rather than silently dropped.
type RepoInventoryInput []map[string]json.RawMessage

// maxRepoInventory is the §4.6 cardinality bound: 1..8 entries per WI.
const maxRepoInventory = 8

// resolveRepoInventory validates a create's declared inventory against
// the §4.6 rules: 1..8 entries, each exactly {name}, the name one of the
// project's repos and unique. It answers the array to freeze (nil when
// none was declared).
func resolveRepoInventory(in RepoInventoryInput, projectRepos []api.ProjectRepo) ([]RepoDecl, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxRepoInventory {
		return nil, Fail(api.ErrCoreBadInput, "", "repo_inventory allows 1..8 entries")
	}
	inProject := map[string]bool{}
	for _, pr := range projectRepos {
		inProject[pr.Name] = true
	}
	out := make([]RepoDecl, 0, len(in))
	seen := map[string]bool{}
	for _, entry := range in {
		var name string
		if raw, ok := entry["name"]; !ok || json.Unmarshal(raw, &name) != nil || strings.TrimSpace(name) == "" {
			return nil, Fail(api.ErrCoreBadInput, "", "every repo_inventory entry needs a string name")
		}
		name = strings.TrimSpace(name)
		if _, ok := entry["url"]; ok {
			return nil, Fail(api.ErrCoreBadInput, "INVENTORY_URL_NOT_ALLOWED",
				"repo %q: url is not accepted; name a project repo (its url comes from the project)", name)
		}
		if _, ok := entry["subdir"]; ok {
			return nil, Fail(api.ErrCoreBadInput, "INVENTORY_SUBDIR_UNSUPPORTED",
				"repo %q: subdir is not supported; omit it", name)
		}
		if len(entry) != 1 {
			return nil, Fail(api.ErrCoreBadInput, "", "repo %q: unknown repo_inventory key (entries are {name})", name)
		}
		if seen[name] {
			return nil, Fail(api.ErrCoreBadInput, "INVENTORY_DUPLICATE_NAME", "duplicate inventory name %q", name)
		}
		seen[name] = true
		if !inProject[name] {
			return nil, Fail(api.ErrCoreBadInput, "REPO_NOT_IN_PROJECT",
				"repo %q is not one of the project's repos", name)
		}
		out = append(out, RepoDecl{Name: name})
	}
	return out, nil
}

// repoInventoryJSON is the column value of a resolved inventory: NULL
// when none was declared.
func repoInventoryJSON(inv []RepoDecl) (any, error) {
	if len(inv) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(inv)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// repoInventoryOf reads a WI's frozen inventory in declaration order
// (nil when it declared none).
func repoInventoryOf(q queryRower, wiID string) ([]RepoDecl, error) {
	var raw sql.NullString
	err := q.QueryRow(`SELECT repo_inventory FROM work_items WHERE id=?1`, wiID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "work item %s", wiID)
	}
	if err != nil {
		return nil, err
	}
	return parseInventory(raw.String)
}

// parseInventory decodes a stored inventory column ("" = none: nil).
func parseInventory(raw string) ([]RepoDecl, error) {
	if raw == "" {
		return nil, nil
	}
	var inv []RepoDecl
	if err := json.Unmarshal([]byte(raw), &inv); err != nil {
		return nil, err
	}
	return inv, nil
}

// claimReposTx answers the claim's task repos: the WI's inventory when
// it declared one — each name resolved to its URL from the project's
// repos NOW, so the clone source is always the project's record — else
// every repo of its project (WIs filed without an inventory keep the
// pre-§4.6 behavior). A declared name the project no longer has refuses
// the claim (CORE-409 INVENTORY_REPO_GONE; the caller's transaction
// rolls back, the WI stays queued).
func claimReposTx(tx *sql.Tx, wiID, projectID string) ([]api.ProjectRepo, error) {
	inv, err := repoInventoryOf(tx, wiID)
	if err != nil {
		return nil, err
	}
	projectRepos, err := projectReposTx(tx, projectID)
	if err != nil || len(inv) == 0 {
		return projectRepos, err
	}
	byName := map[string]api.ProjectRepo{}
	for _, pr := range projectRepos {
		byName[pr.Name] = pr
	}
	repos := make([]api.ProjectRepo, 0, len(inv))
	for _, r := range inv {
		pr, ok := byName[r.Name]
		if !ok {
			return nil, Fail(api.ErrCoreConflict, "INVENTORY_REPO_GONE",
				"declared repo %q is no longer one of the project's repos; restore it on the project, or cancel and re-file the work item", r.Name)
		}
		repos = append(repos, pr)
	}
	return repos, nil
}

// inventoryRepoGone reports whether a WI's frozen inventory (the raw
// repo_inventory column) names a repo
// that is not in inProject (the project's current repo-name set). Such a
// WI would only draw CORE-409 INVENTORY_REPO_GONE on every claim, so the
// ready projection leaves it out (like an unfinished blocks edge) until
// the repo is restored or the WI is canceled.
func inventoryRepoGone(raw string, inProject map[string]bool) (bool, error) {
	inv, err := parseInventory(raw)
	if err != nil {
		return false, err
	}
	for _, r := range inv {
		if !inProject[r.Name] {
			return true, nil
		}
	}
	return false, nil
}
