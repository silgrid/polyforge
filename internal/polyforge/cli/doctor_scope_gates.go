package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_scope_gates.go: the read-only readiness probe behind `pf doctor`'s
// "scoped-gates readiness" row. The verification-tier mechanism is
// platform-wide: the release tree ships the engine `tools/verify-scope`, and a
// repository may narrow its scope or declare its bar with a declarative
// `.polyforge/verify-scope.conf`. This row makes readiness visible: for every
// mapped project's registered repos it reads (never executes) the clone's
// manifests and conf and the release tree's engine, and answers whether the
// engine default or the repo conf is effective. A repo with no local clone is
// `unknown`, never faked green.
//
// The verdict per cloned repo (the engine is conf-first: it reports a repo
// conf's commands for every stack):
//   - a `stack=go|npm` pin, or a single go.mod / package.json: the analyzer
//     is effective — scoped-ready;
//   - no analyzer — a `stack=none` pin, or neither manifest — with a conf
//     declaring at least one command (fast_gate=, full_command=, check=):
//     the repo conf is effective, on the full tier only (no analyzer, no
//     scoped tier) — pass;
//   - no analyzer and no conf command: warn, naming the consequence (the
//     verification step derives the bar ad hoc on every run, there is no
//     scoped tier, and a future runner dispatch fails closed);
//   - go.mod and package.json without a pin: warn (unpinned multi-manifest);
//   - any effective verdict without the release-tree engine: warn.
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

// scopedGatesCheckName and scopedGatesReservedName are the engine's check=
// name rules: letters, digits, '.', '_' and '-', starting with a letter or
// digit, and never the anonymous/analyzer form <phase>-<n>.
var (
	scopedGatesCheckName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	scopedGatesReservedName = regexp.MustCompile(`^(fast|full|scoped)-[0-9]`)
)

// scopedGatesReadConf reads a conf's first valid `stack=` pin (go, npm or
// none; an invalid pin is ignored, as in the engine) and counts the commands
// it declares — fast_gate=, full_command= and well-formed check= lines, as
// the engine accepts them: a value that is empty or begins with a command
// substitution is no command. The file is only read, never evaluated; a
// missing or unreadable file answers no pin and no command.
func scopedGatesReadConf(path string) (string, int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	pin, commands := "", 0
	reportable := func(cmd string) bool {
		return cmd != "" && !strings.HasPrefix(cmd, "$(") && !strings.HasPrefix(cmd, "`")
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimLeft(sc.Text(), " \t\r\v\f")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "stack":
			v := strings.TrimSpace(val)
			if pin == "" && (v == "go" || v == "npm" || v == "none") {
				pin = v
			}
			// An invalid pin is ignored; the first valid pin wins, as in the engine.
		case "fast_gate", "full_command":
			if reportable(val) {
				commands++
			}
		case "check":
			parts := strings.SplitN(val, "|", 3)
			if len(parts) == 3 && scopedGatesCheckName.MatchString(parts[0]) && !scopedGatesReservedName.MatchString(parts[0]) &&
				(parts[1] == "fast" || parts[1] == "full") && reportable(parts[2]) {
				commands++
			}
		}
	}
	return pin, commands
}

// scopedGatesNoAnalyzer is the consequence a repo with no analyzer and no
// conf command carries; the warn detail spells it out.
const scopedGatesNoAnalyzer = "the verification step derives the bar ad hoc on every run, there is no scoped tier (a scoped ticket falls back to full), and a future runner dispatch fails closed; declare the bar in " + scopedGatesConf + " (fast_gate=, full_command= or check= lines; stack=none pins a conf-only repo)"

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
	var problems, unknown, readFails, confOnly []string
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
			pin, commands := scopedGatesReadConf(confPath)
			noAnalyzer := false
			switch {
			case pin == "none" && commands == 0:
				problems = append(problems, fmt.Sprintf("%s/%s: %s pins stack=none but declares no command — %s (%s)", project, repo.Name, scopedGatesConf, scopedGatesNoAnalyzer, scopedGatesPointer))
				continue
			case pin == "none":
				// the conf is the whole bar: no analyzer, full tier only.
				noAnalyzer = true
			case pin != "":
				// the conf pins the analyzer, so the repo's own declaration is effective.
			case hasGo && hasNpm:
				problems = append(problems, fmt.Sprintf("%s/%s: both go.mod and package.json are present without a stack= pin — an unpinned multi-manifest repo; a scoped ticket falls back to full (%s)", project, repo.Name, scopedGatesPointer))
				continue
			case !hasGo && !hasNpm && commands == 0:
				problems = append(problems, fmt.Sprintf("%s/%s: no supported stack manifest (go.mod or package.json) and no repo conf command — %s (%s)", project, repo.Name, scopedGatesNoAnalyzer, scopedGatesPointer))
				continue
			case !hasGo && !hasNpm:
				// another stack whose conf declares its bar: conf-first.
				noAnalyzer = true
			}
			// A conf names declarations the engine executes; without the
			// release-tree engine it does nothing, so the repo is not ready.
			if !enginePresent {
				problems = append(problems, fmt.Sprintf("%s/%s: the release tree lacks %s — a scoped ticket falls back to full (%s)", project, repo.Name, enginePath, scopedGatesPointer))
				continue
			}
			if noAnalyzer {
				confOnly = append(confOnly, project+"/"+repo.Name)
				continue
			}
			ready++
		}
	}
	// confNote names the repos whose conf is effective with no analyzer: they
	// are verified, on the full tier only.
	confNote := ""
	if len(confOnly) > 0 {
		confNote = fmt.Sprintf("; %d repo(s) with no analyzer: repo conf effective (its commands, full tier only — no scoped tier): %s", len(confOnly), strings.Join(capRows(confOnly), "; "))
	}
	switch {
	case len(problems) > 0:
		detail := fmt.Sprintf("%d of %d registered repo(s) are not scoped-ready: %s", len(problems), registered, strings.Join(capRows(problems), "; "))
		detail += confNote
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
		detail += confNote
		if len(unknown) > 0 {
			detail += fmt.Sprintf("; %d repo(s) unknown (no local clone): %s", len(unknown), strings.Join(capRows(unknown), "; "))
		}
		return "pass", detail
	}
}
