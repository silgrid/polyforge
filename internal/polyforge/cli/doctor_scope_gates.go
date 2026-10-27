package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_scope_gates.go: the read-only readiness probe behind `pf doctor`'s
// "scoped-gates readiness" row. The verification-tier mechanism is
// platform-wide: the release tree ships the engine `tools/verify-scope`, and a
// repository may narrow its scope with a declarative
// `.polyforge/verify-scope.conf`. This row makes readiness visible: for every
// mapped project's registered repos it reads (never executes) the clone's
// manifests and conf and the release tree's engine, and answers whether the
// engine default or the repo conf is effective. A repo with no local clone is
// `unknown`, never faked green.
//
// The row is informational and additive: it is registered with prerequisite
// needMapping (NOT needAihub, so the locally decidable half still fires when
// aihub is down), it never returns `fail` (an unsupported stack or a missing
// prerequisite is a capability gap, not an illness — it cannot move `ok` or
// the exit code), and `--harness` skips doctorChecks wholesale. One authority
// is cited, never restated: docs/contributing.md's "Verification tiers"
// section (the same section the filing skill names).

// docChScopedGates is the row name (sibling convention: one const beside
// the layer it implements).
const docChScopedGates = "scoped-gates readiness"

// scopedGatesEngine is the release-shipped engine path, relative to the
// release checkout (<data-dir>/release/current/tools/verify-scope).
const scopedGatesEngine = "tools/verify-scope"

// scopedGatesConf is the declarative repository override, relative to the
// clone root.
const scopedGatesConf = ".polyforge/verify-scope.conf"

// scopedGatesPointer is the one shared authority both this row and the
// filing skill name (the ticket's 不另造权威): the repo's own
// verification-tier section.
const scopedGatesPointer = "docs/contributing.md『Verification tiers』"

// scopedGatesStack answers a conf's first valid `stack=` pin, if any. The
// file is only read, never evaluated.
func scopedGatesStack(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "stack=") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "stack="))
			if v == "go" || v == "npm" {
				return v
			}
			// An invalid pin is ignored; the first valid pin wins, as in the engine.
		}
	}
	return ""
}

// scopedGatesFileExists is a presence read (never an execution).
func scopedGatesFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// scopedGatesCheck is the doctor row body: for every mapped project's
// registered repos, read the local clone's manifests and conf and the release
// tree's engine and report the readiness. The registered-repo set is read from
// aihub's project record (the member-readable GET /api/v1/projects/{id}), one
// read per mapped project; a project whose read fails contributes no repos and
// its reason is surfaced — the row never invents a pass over an unread set.
// Status pass|warn|skip, never fail.
func scopedGatesCheck(r *doctorRun) (string, string) {
	aihub := aihubclient.New(r.cfg.Server, r.cfg.APIKey)
	var problems, unknown, readFails []string
	ready, registered := 0, 0
	home, _ := os.UserHomeDir()
	enginePath := filepath.Join(releaseCurrent(home), scopedGatesEngine)
	enginePresent := scopedGatesFileExists(enginePath)
	for _, project := range r.cfg.Mapping.Projects() {
		ws, err := r.cfg.Mapping.WorkspaceFor(project)
		if err != nil {
			continue // the mapping enumeration guarantees a workspace
		}
		raw, err := aihub.GetProject(project)
		if err != nil {
			readFails = append(readFails, fmt.Sprintf("project %q: %v", project, err))
			continue
		}
		var proj api.Project
		if err := json.Unmarshal(raw, &proj); err != nil {
			readFails = append(readFails, fmt.Sprintf("project %q: %v", project, err))
			continue
		}
		for _, repo := range proj.Repos {
			if repo.Name == "" {
				continue
			}
			registered++
			clone := workspace.RepoCloneDir(ws, repo.Name)
			if !workspace.IsRepoClone(clone) {
				unknown = append(unknown, fmt.Sprintf("%s/%s: no local clone under %s — readiness unknown", project, repo.Name, clone))
				continue
			}
			confPath := filepath.Join(clone, workspace.GeneratedDir, "verify-scope.conf")
			hasGo := scopedGatesFileExists(filepath.Join(clone, "go.mod"))
			hasNpm := scopedGatesFileExists(filepath.Join(clone, "package.json"))
			pin := scopedGatesStack(confPath)
			switch {
			case pin != "":
				// the conf pins the analyzer, so the repo's own declaration is effective.
			case hasGo && hasNpm:
				problems = append(problems, fmt.Sprintf("%s/%s: both go.mod and package.json are present without a stack= pin — an unpinned multi-manifest repo; a scoped ticket falls back to full (%s)", project, repo.Name, scopedGatesPointer))
				continue
			case !hasGo && !hasNpm:
				problems = append(problems, fmt.Sprintf("%s/%s: no supported stack manifest (go.mod or package.json) — a scoped ticket falls back to full (%s)", project, repo.Name, scopedGatesPointer))
				continue
			}
			// A conf names declarations the engine executes; without the
			// release-tree engine it does nothing, so the repo is not ready.
			if !enginePresent {
				problems = append(problems, fmt.Sprintf("%s/%s: the release tree lacks %s — a scoped ticket falls back to full (%s)", project, repo.Name, enginePath, scopedGatesPointer))
				continue
			}
			ready++
		}
	}
	switch {
	case len(problems) > 0:
		detail := fmt.Sprintf("%d of %d registered repo(s) are not scoped-ready: %s", len(problems), registered, strings.Join(capRows(problems), "; "))
		if len(unknown) > 0 {
			detail += fmt.Sprintf("; %d repo(s) unknown (no local clone): %s", len(unknown), strings.Join(capRows(unknown), "; "))
		}
		if len(readFails) > 0 {
			detail += "; the registered-repo set is incomplete: " + strings.Join(capRows(readFails), "; ")
		}
		return "warn", detail
	case len(readFails) > 0:
		return "skip", fmt.Sprintf("scoped-gate readiness not decidable: the registered-repo set is incomplete: %s", strings.Join(capRows(readFails), "; "))
	default:
		detail := fmt.Sprintf("%d of %d registered repo(s) are scoped-ready (%s or %s)", ready, registered, scopedGatesEngine, scopedGatesConf)
		if len(unknown) > 0 {
			detail += fmt.Sprintf("; %d repo(s) unknown (no local clone): %s", len(unknown), strings.Join(capRows(unknown), "; "))
		}
		return "pass", detail
	}
}
