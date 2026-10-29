package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// verbs_doctor.go: `pf doctor` — the daemon's single health face. Every
// health dimension the daemon depends on is one row of doctorChecks:
// the config and the workspace mapping (dirs, aihub projects, the
// .polyforge.yaml a claim would load), the workspace layout
// (doctor_workspace.go), connectivity (server reach, daemon socket,
// auth), daemon supervision and environment hygiene, the claude step
// harness, and the harness family (doctor_display.go, doctor_layout.go:
// progress display and install layout; never fail, and `--harness` runs
// them alone for tools/install.sh). A check whose prerequisite is
// missing — an unloadable config, an absent mapping, an unreachable
// aihub — is reported skip, never a faked pass. Credential values are
// never shown: set/unset and names only.
//
// Exit codes: 0 every check passes, 1 any check fails, 2 usage errors.
// --json emits {"ok": <bool>, "checks": [{"name", "status":
// "pass"|"fail"|"skip"|"warn", "detail"}]}.
//
// The verb loads the config ITSELF (Command.NoConfigLoad): an unloadable
// config is the subject of the report, not an early exit.

// doctorCheck is one check row of the report.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass | fail | skip | warn
	Detail string `json:"detail"`
}

func (c doctorCheck) row() (string, string) { return c.Status, c.Detail }

// doctorReport is the --json document shape.
type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

// The check names (stable faces; the details carry the specifics).
const (
	docChConfig   = "config"
	docChMapping  = "workspace mapping"
	docChDirs     = "workspace dirs"
	docChPrereqs  = "prerequisites"
	docChYaml     = "workspace yaml"
	docChProjects = "aihub projects"
	docChServer   = "server reach"
	docChSocket   = "daemon socket"
	// docChReload is the warn-only cli.toml hot-reload row:
	// restart-only keys a reload left pending, and flag-overridden keys.
	docChReload    = "config reload"
	docChAuth      = "auth"
	docChClaude    = "harness claude"
	docChEnv       = "retired env"
	docChDaemonEnv = "daemon env"
	// docChModelHealth is the additive warn-at-most row.
	docChModelHealth = "model health"
)

// doctorRun is what one run loaded and probed once; the checks read it.
type doctorRun struct {
	cfg               Config
	present, missing  []string // mapped workspaces that exist as directories / do not
	prune             bool     // --prune-scratch
	pruneGitDirs      bool     // --prune-git-dirs
	reachOK, sockOK   bool
	reachMsg, sockMsg string
	status            StatusResponse
	claudeSteps       []workspace.StepResolution // set by the workspace yaml check
	auth              doctorCheck                // set by the aihub projects check
}

// doctorNeed is a check's prerequisite beyond a loadable config.
type doctorNeed int

const (
	needConfig  doctorNeed = iota
	needMapping            // a non-empty [workspace] mapping
	needAihub              // a reachable aihub, then a mapping
)

// doctorChecks is the check ladder in report order. A check whose need
// is unmet is reported skip with the reason.
var doctorChecks = []struct {
	name string
	need doctorNeed
	run  func(*doctorRun) (string, string)
}{
	{docChMapping, needConfig, mappingCheck},
	{docChDirs, needMapping, func(r *doctorRun) (string, string) {
		if len(r.missing) > 0 {
			return "fail", "mapped workspace directory missing or not a directory: " + strings.Join(r.missing, ", ") + " — create it (the daemon never does) or fix the [workspace] key"
		}
		return "pass", fmt.Sprintf("all %d mapped workspace(s) exist as directories", len(r.present))
	}},
	{docChLayout, needMapping, func(r *doctorRun) (string, string) { return workspaceLayoutCheck(r.present).row() }},
	{docChScratch, needMapping, func(r *doctorRun) (string, string) { return scratchCheck(r.present, time.Now(), r.prune).row() }},
	{docChGitDirs, needConfig, func(r *doctorRun) (string, string) {
		home, _ := os.UserHomeDir()
		return gitDirsCheck(r, DataDir(home), time.Now())
	}},
	{docChRepoClones, needMapping, func(r *doctorRun) (string, string) { return repoClonesCheck(r) }},
	{docChScopedGates, needMapping, func(r *doctorRun) (string, string) { return scopedGatesCheck(r) }},
	{docChMemPolicy, needMapping, func(r *doctorRun) (string, string) { return memoryPolicyCheck(r.present).row() }},
	{docChPrereqs, needConfig, func(r *doctorRun) (string, string) { return prerequisitesCheck(exec.LookPath).row() }},
	{docChServer, needConfig, func(r *doctorRun) (string, string) { return passOrFail(r.reachOK), r.reachMsg }},
	{docChSocket, needConfig, func(r *doctorRun) (string, string) { return passOrFail(r.sockOK), r.sockMsg }},
	{docChSupervision, needConfig, func(r *doctorRun) (string, string) { return supervisionCheck(r.sockOK).row() }},
	{docChReload, needConfig, reloadCheck},
	{docChModelHealth, needConfig, func(r *doctorRun) (string, string) { return modelHealthCheck(r) }},
	{docChEnv, needConfig, func(r *doctorRun) (string, string) { return retiredEnvCheck(os.Getenv, r.status.EnvWarnings).row() }},
	{docChDaemonEnv, needConfig, func(r *doctorRun) (string, string) {
		return daemonEnvCheck(r.sockOK, r.status.EnvNames, r.status.PiModelsEnvRefs).row()
	}},
	{docChYaml, needMapping, func(r *doctorRun) (string, string) {
		st, detail, steps := workspaceYamlCheck(r.cfg.Mapping, r.status.WorkflowsDir, r.sockOK)
		r.claudeSteps = steps
		return st, detail
	}},
	{docChClaude, needMapping, func(r *doctorRun) (string, string) {
		// the daemon's harness view (it may run `claude --version`) is
		// asked for only when some step actually runs on claude
		var hs map[string]HarnessStatus
		if len(r.claudeSteps) > 0 && r.sockOK {
			hs = probeDaemonHarnesses(r.cfg.DaemonSocket)
		}
		return claudeHarnessCheck(r.claudeSteps, r.status.WorkflowsDir, r.sockOK, hs)
	}},
	{docChProjects, needAihub, projectsCheck},
	{docChAuth, needAihub, func(r *doctorRun) (string, string) { return r.auth.row() }},
}

// reloadCheck is the warn-only cli.toml hot-reload row (R13):
// a restart-only key changed by a reload is pending until the daemon
// restarts, and a flag-overridden key is reported too. It never fails —
// a staged restart-only change is intentional.
func reloadCheck(r *doctorRun) (string, string) {
	if !r.sockOK {
		return "skip", "not run: the daemon does not answer"
	}
	rel := r.status.Reload
	if rel == nil {
		return "skip", "not run: the running daemon reports no reload state (an older daemon?)"
	}
	var parts []string
	if len(rel.Pending) > 0 {
		parts = append(parts, "pending restart: "+strings.Join(rel.Pending, ", "))
	}
	if len(rel.Overridden) > 0 {
		parts = append(parts, "overridden by flag: "+strings.Join(rel.Overridden, ", "))
	}
	if len(parts) == 0 {
		return "pass", "no restart-only key is pending"
	}
	return "warn", strings.Join(parts, "; ") + " — apply a pending change with `pf ops shutdown` then `pf daemon ensure`"
}

// retiredHarnessEnv are the POLYFORGE_* variables decision 2026-10-16 G2
// stopped reading, with the [daemon] key that replaces each.
var retiredHarnessEnv = []struct{ env, key string }{
	{"POLYFORGE_HARNESS_ROOT", "harness_root"},
	{"POLYFORGE_OPERATOR_AGENT_DIR", "operator_agent_dir"},
}

// retiredEnvCheck warns when a retired harness variable is set in this
// shell, or the daemon reported one set in ITS environment to a value
// differing from what it resolved: the variable is no longer read, so
// harness state would move silently.
func retiredEnvCheck(getenv func(string) string, daemonWarnings []string) doctorCheck {
	c := doctorCheck{Name: docChEnv, Status: "pass", Detail: "no retired POLYFORGE_* harness variable in effect"}
	var notes []string
	for _, v := range retiredHarnessEnv {
		if val := getenv(v.env); val != "" {
			notes = append(notes, fmt.Sprintf("%s=%s is set in this shell but no longer read — set [daemon] %s = %q in cli.toml instead and unset it", v.env, val, v.key, val))
		}
	}
	for _, w := range daemonWarnings {
		notes = append(notes, "daemon: "+w)
	}
	if len(notes) > 0 {
		c.Status, c.Detail = "warn", strings.Join(notes, "; ")
	}
	return c
}

// daemonEnvCheck (env hygiene) judges the RUNNING daemon's environment
// by the names GET /status reports (never values): any ANTHROPIC_* or
// other credential-looking name outside the `pf daemon ensure`
// allow-list (supervisor/env.go) means the daemon was started from a
// polluted environment — restart it clean. It also warns when the pi
// step agent's models.json interpolates a variable (`$NAME`) outside the
// allow-list: pi steps would never see it.
func daemonEnvCheck(daemonUp bool, names, piModelsRefs []string) doctorCheck {
	c := doctorCheck{Name: docChDaemonEnv, Status: "skip"}
	switch {
	case !daemonUp:
		c.Detail = "not run: the daemon does not answer"
		return c
	case len(names) == 0:
		c.Detail = "not run: the daemon reports no environment names (an older daemon?)"
		return c
	}
	home, _ := os.UserHomeDir()
	var notes []string
	if bad := supervisor.SuspectDaemonEnv(names); len(bad) > 0 {
		notes = append(notes, "the daemon's environment carries "+strings.Join(bad, ", ")+
			" (names only; outside the `pf daemon ensure` allow-list) — restart it clean (graceful; running jobs are checkpointed first): "+ensureFixCommand(home))
	}
	var unseen []string
	for _, r := range piModelsRefs {
		if !supervisor.DaemonEnvAllowed(r) {
			unseen = append(unseen, "$"+r)
		}
	}
	if len(unseen) > 0 {
		notes = append(notes, "the pi step agent's models.json references "+strings.Join(unseen, ", ")+
			" (names only), which no `pf daemon ensure`-started daemon carries, so pi steps cannot resolve it — put the key in the operator agent dir's auth.json (pi /login) or as a literal in models.json")
	}
	if len(notes) > 0 {
		c.Status, c.Detail = "warn", strings.Join(notes, "; ")
	} else {
		c.Status, c.Detail = "pass", "the daemon's environment carries no ANTHROPIC_* and no credential-looking name outside the ensure allow-list"
	}
	return c
}

var cmdDoctor = &Command{
	Name:         "doctor",
	Group:        GroupDiag,
	Summary:      "Check this daemon's health: config, workspace mapping and layout, connectivity, progress display",
	Usage:        "pf doctor [--harness] [--prune-scratch] [--prune-git-dirs] [--json]",
	NoConfigLoad: true,
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("harness", false, "run only the harness progress-display checks")
		fs.Bool("prune-scratch", false, "remove the mapped workspaces' scratch/ entries older than 14 days (default: report them only)")
		fs.Bool("prune-git-dirs", false, "remove the external git dirs and set-aside scenes of wrapped/failed/canceled work items untouched for more than 14 days (default: report them only; never while the daemon is down)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("doctor: unexpected arguments %v", args)
		}
		checks := harnessDisplayChecks()
		if !boolFlag(fs, "harness") {
			checks = append(runDoctorChecks(ctx, boolFlag(fs, "prune-scratch"), boolFlag(fs, "prune-git-dirs")), checks...)
		}
		rep := doctorReport{Checks: checks}
		fails := countFails(rep)
		rep.OK = fails == 0
		if ctx.JSON {
			b, err := json.Marshal(rep)
			if err != nil {
				return err
			}
			ctx.Out(b)
		} else {
			for _, c := range rep.Checks {
				fmt.Fprintf(ctx.Stdout, "%-6s %s  %s\n", c.Status, c.Name, c.Detail)
			}
			fmt.Fprintf(ctx.Stdout, "\nok: %t\n", rep.OK)
		}
		// Exit-code semantics are the report's: 0 all-pass, 1 any-fail
		// (renderError prints the one-line verdict).
		if fails > 0 {
			return fmt.Errorf("doctor: %d check(s) failed — see the report above", fails)
		}
		return nil
	},
}

// countFails counts the failing checks of a report.
func countFails(rep doctorReport) int {
	n := 0
	for _, c := range rep.Checks {
		if c.Status == "fail" {
			n++
		}
	}
	return n
}

// runDoctorChecks runs the config check and then doctorChecks, skipping
// each check whose prerequisite is missing. pruneScratch lets the
// scratch check remove the stale entries it would otherwise report;
// pruneGitDirs does the same for the git dir retention check.
func runDoctorChecks(ctx *Context, pruneScratch, pruneGitDirs bool) []doctorCheck {
	cfg, err := LoadConfig(ctx.ConfigPath)
	if err != nil {
		checks := []doctorCheck{{docChConfig, "fail", err.Error() + " — see the install docs for the migration steps"}}
		for _, d := range doctorChecks {
			checks = append(checks, doctorCheck{d.name, "skip", "config-dependent check not run: the config could not be loaded"})
		}
		return checks
	}
	where := ctx.ConfigPath
	if where == "" {
		where = DefaultConfigPath()
	}
	checks := []doctorCheck{{docChConfig, "pass", "loaded from " + where}}

	r := &doctorRun{cfg: cfg, prune: pruneScratch, pruneGitDirs: pruneGitDirs}
	for _, ws := range cfg.Mapping.Workspaces() {
		if st, err := os.Stat(ws); err == nil && st.IsDir() {
			r.present = append(r.present, ws)
		} else {
			r.missing = append(r.missing, ws)
		}
	}
	r.reachOK, r.reachMsg = probeServerReach(cfg.Server)
	r.sockOK, r.sockMsg, r.status = probeDaemonSocket(cfg.DaemonSocket)

	for _, d := range doctorChecks {
		c := doctorCheck{Name: d.name, Status: "skip"}
		switch {
		case d.need == needAihub && !r.reachOK:
			c.Detail = "not run: aihub unreachable (" + cfg.Server + ")"
		case d.need >= needMapping && cfg.Mapping.Empty():
			c.Detail = "not run: the mapping is absent"
		default:
			c.Status, c.Detail = d.run(r)
		}
		checks = append(checks, c)
	}
	return checks
}

// mappingCheck runs the SHARED mapping/workspace validator (the same one
// daemon startup and `pf config` use) and fails the fresh-template shape
// (zero [workspace] sections) with the shape guidance.
func mappingCheck(r *doctorRun) (string, string) {
	if err := ValidateMappingForDaemon(r.cfg); err != nil {
		return "fail", err.Error() + "; " + extensionsPointer
	}
	return "pass", fmt.Sprintf("%d workspace(s) mapping %d project(s)", len(r.cfg.Mapping.Workspaces()), len(r.cfg.Mapping.Projects()))
}

// projectsCheck reads every mapped project's ready queue from aihub — the
// project-scoped read the daemon's auto-pull uses, open to any project
// member (GET /projects/{id} is admin-only, so a member's key would 403
// there) — and judges auth from the same reads into r.auth: a 401/403
// fails it, any successful read proves it. aihub answers 404 both for an
// unknown project and for one the key's user is not a member of.
func projectsCheck(r *doctorRun) (string, string) {
	client := aihubclient.New(r.cfg.Server, r.cfg.APIKey)
	var fails []string
	denied, proven := false, false
	for _, project := range r.cfg.Mapping.Projects() {
		ws, _ := r.cfg.Mapping.WorkspaceFor(project)
		_, err := client.Queue(project)
		if err == nil {
			proven = true
			continue
		}
		var ae *api.Error
		if errors.As(err, &ae) {
			switch ae.HTTPStatus() {
			case 401, 403:
				denied = true
			case 404:
				fails = append(fails, fmt.Sprintf("project %q (workspace %s) is unknown to aihub, or this api key's user is not a member of it: %s", project, ws, ae.Error()))
				continue
			}
		}
		fails = append(fails, fmt.Sprintf("project %q (workspace %s): %v", project, ws, err))
	}
	switch {
	case denied:
		r.auth = doctorCheck{Status: "fail", Detail: "aihub rejected the credential with a 401/403 answer — fix the api key (config api_key / PF_API_KEY)"}
	case proven:
		r.auth = doctorCheck{Status: "pass", Detail: "the api key got past aihub's auth boundary (the project read answered)"}
	default:
		r.auth = doctorCheck{Status: "skip", Detail: "not proven: the aihub project read failed for another reason"}
	}
	if len(fails) > 0 {
		return "fail", strings.Join(fails, "; ")
	}
	return "pass", fmt.Sprintf("all %d mapped project(s) are known to aihub", len(r.cfg.Mapping.Projects()))
}

func passOrFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// probeServerReach classifies server reachability.
func probeServerReach(server string) (bool, string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(server, "/") + "/api/v1/projects")
	if err != nil {
		return false, fmt.Sprintf("aihub server %s unreachable: %v", server, err)
	}
	resp.Body.Close()
	return true, fmt.Sprintf("aihub server %s answered (HTTP %d)", server, resp.StatusCode)
}

// probeDaemonSocket dials the daemon's UDS face and asks GET /status;
// it also answers the parsed status (zero when unreachable).
func probeDaemonSocket(sock string) (bool, string, StatusResponse) {
	st, err := daemonStatus(sock, 5*time.Second, false)
	if err != nil {
		return false, fmt.Sprintf("daemon socket %s does not answer GET /status: %v — the daemon is not running: start it with `pf daemon ensure` (it starts the supervised restart loop)", sock, err), st
	}
	return true, fmt.Sprintf("daemon socket %s answers (daemon %s)", sock, st.MachineID), st
}

// probeDaemonHarnesses asks GET /status?harnesses=1 — the daemon's view
// of the step harness binaries (nil when it does not answer or is too old
// to report it).
func probeDaemonHarnesses(sock string) map[string]HarnessStatus {
	st, _ := daemonStatus(sock, 15*time.Second, true)
	return st.Harnesses
}

func daemonStatus(sock string, timeout time.Duration, harnesses bool) (StatusResponse, error) {
	c := NewDaemonClient(sock)
	c.HTTP.Timeout = timeout
	status := c.Status
	if harnesses {
		status = c.StatusWithHarnesses
	}
	var st StatusResponse
	raw, err := status()
	if err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st, err
}

// workspaceYamlCheck loads every mapped workspace's .polyforge.yaml and
// the daemon's installed bundles the way a claim does (the refusal it
// would meet, DAEMON-414, surfaced before any claim). Without the
// daemon's workflows dir the per-step keys cannot be judged: a yaml that
// otherwise loads is a warn, never a pass. It also answers every step
// with a `claude:` candidate (the "harness claude" check's subject).
func workspaceYamlCheck(m *mapping.Plan, workflowsDir string, daemonUp bool) (string, string, []workspace.StepResolution) {
	var fails []string
	var claudeSteps []workspace.StepResolution
	bundles, bad, _ := workspace.Bundles(workflowsDir, "")
	bundleNames := slices.Sorted(maps.Keys(bundles))
	var badBundles []string
	for name, err := range bad {
		badBundles = append(badBundles, fmt.Sprintf("bundle %s: %v", name, err))
	}
	sort.Strings(badBundles)
	present, withSteps := 0, 0
	for _, ws := range m.Workspaces() {
		_, serr := os.Stat(workspace.PathFor(ws))
		if serr == nil {
			present++
		}
		cfg, err := workspace.Load(ws, workflowsDir, m.ProjectsOf(ws))
		if err != nil {
			fails = append(fails, err.Error())
			continue
		}
		if cfg != nil && len(cfg.Models) > 0 {
			withSteps++
		}
		if workflowsDir == "" {
			continue
		}
		for _, project := range m.ProjectsOf(ws) {
			for _, name := range bundleNames {
				for _, r := range workspace.ResolveSteps(name, bundles[name], cfg, project, nil) {
					hs, herr := r.Harnesses()
					if herr != nil {
						fails = append(fails, herr.Error())
						continue
					}
					if slices.Contains(hs, harness.Claude) {
						claudeSteps = append(claudeSteps, r)
					}
				}
			}
		}
	}
	var parts []string
	if len(fails) > 0 {
		parts = append(parts, strings.Join(fails, "; ")+" — a claim of these projects is refused (DAEMON-414) until fixed")
	}
	if len(badBundles) > 0 {
		parts = append(parts, strings.Join(badBundles, "; ")+" — a claim of a work item on such a bundle is refused until it is fixed")
	}
	switch {
	case len(parts) > 0:
		return "fail", strings.Join(parts, "; ") + "; " + extensionsPointer, claudeSteps
	case present == 0 && workflowsDir != "":
		return "pass", "no mapped workspace carries a " + workspace.FileName + "; every installed bundle's candidates name their harness", claudeSteps
	case present == 0:
		return "pass", "no mapped workspace carries a " + workspace.FileName, claudeSteps
	case workflowsDir == "" && withSteps == 0:
		return "pass", fmt.Sprintf("%d %s file(s) load and declare no per-step keys", present, workspace.FileName), claudeSteps
	case workflowsDir == "" && !daemonUp:
		return "warn", fmt.Sprintf("%d %s file(s) parse, but the per-step keys are unchecked: the daemon is unreachable, so its workflows dir is unknown", present, workspace.FileName), claudeSteps
	case workflowsDir == "":
		return "warn", fmt.Sprintf("%d %s file(s) parse, but the per-step keys are unchecked: the daemon reported no workflows dir (an older daemon?)", present, workspace.FileName), claudeSteps
	}
	return "pass", fmt.Sprintf("%d %s file(s) load; every per-step key is a step id of %s and every candidate names its harness", present, workspace.FileName, workflowsDir), claudeSteps
}

// claudeHarnessCheck judges the Claude Code CLI the daemon would run for
// the steps with a `claude:` candidate, from the facts the daemon
// reports (binary, `claude --version`, set/unset only). The credential
// judgment keys on the claude-config settings.json env block, the single
// authority layer; its pairing warnings render as reported.
func claudeHarnessCheck(steps []workspace.StepResolution, workflowsDir string, daemonUp bool, hs map[string]HarnessStatus) (string, string) {
	if len(steps) == 0 {
		if workflowsDir == "" {
			return "pass", "no claude CLI needed as far as can be judged: the daemon's workflows are unknown, so only the workspace yaml was read"
		}
		return "pass", "no claude CLI needed: no resolved candidate is claude:… (every step runs on pi)"
	}
	var rows []string
	for i, r := range steps {
		if i == 12 {
			rows = append(rows, fmt.Sprintf("… %d more", len(steps)-i))
			break
		}
		rows = append(rows, fmt.Sprintf("%s/%s/%s: [%s] (%s)", r.Project, r.Bundle, r.Step, strings.Join(r.Models, ", "), r.ModelsFrom))
	}
	table := fmt.Sprintf("%d step(s) have claude candidates: %s", len(steps), strings.Join(rows, "; "))
	if !daemonUp {
		return "skip", table + " — the CLI is unchecked: the daemon is unreachable"
	}
	h, ok := hs[harness.Claude]
	if !ok {
		return "warn", table + " — the CLI is unchecked: the daemon reported no harness status (an older daemon?)"
	}
	if !h.Found {
		return "fail", fmt.Sprintf("%s — the claude binary %q does not resolve on the daemon's PATH: install Claude Code (logged in by a human) or set cli.toml claude_bin; %s", table, h.Bin, extensionsPointer)
	}
	status := "pass"
	var notes []string
	warn := func(n string) {
		status = "warn"
		notes = append(notes, n)
	}
	switch {
	case h.VersionError != "":
		warn("version unknown (" + h.VersionError + ")")
	case !h.Verified:
		min := h.MinVersion
		if min == "" {
			min = "the verified minimum"
		}
		warn(fmt.Sprintf("version %s is older than %s or unparsable (the adapter's argv/stream contract is verified from that version on)", h.Version, min))
	}
	// claude steps strip every inherited ANTHROPIC_*, so only the
	// settings.json env block can carry the step's credential; the
	// daemon's own variables are reported, never counted.
	if h.SettingsEnv["ANTHROPIC_AUTH_TOKEN"] != "set" && h.SettingsEnv["ANTHROPIC_API_KEY"] != "set" {
		warn("no credential (ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY) in the claude-config settings.json env block — the steps' only credential source (claude steps do not inherit the daemon's ANTHROPIC_*); the settings.json env pair (ANTHROPIC_AUTH_TOKEN + optional ANTHROPIC_BASE_URL) must live under the claude-config dir (env block: ANTHROPIC_AUTH_TOKEN required, ANTHROPIC_BASE_URL optional — paired, never a project-level settings file)")
	}
	if h.SettingsWarning != "" {
		warn(h.SettingsWarning)
	}
	detail := fmt.Sprintf("%s — %s %s at %s; daemon env (claude steps do not inherit it): ANTHROPIC_AUTH_TOKEN %s, ANTHROPIC_API_KEY %s, ANTHROPIC_BASE_URL %s%s; background tasks %s",
		table, h.Bin, h.Version, h.Path, h.AuthTokenEnv, h.APIKeyEnv, h.BaseURLEnv, settingsFacts(h), h.BackgroundTasks)
	if len(notes) > 0 {
		detail += " — " + strings.Join(notes, "; ")
	}
	return status, detail
}

// settingsFacts renders the claude status facts: the harness dirs and
// the settings.json env key facts (set|unset, never values).
func settingsFacts(h HarnessStatus) string {
	var parts []string
	if len(h.HarnessDirs) > 0 {
		var rows []string
		for _, d := range [][2]string{{"step_agent", "step agent dir"}, {"step_agent_sessions", "step agent sessions"}, {"claude_config", "claude config dir"}} {
			if dir, ok := h.HarnessDirs[d[0]]; ok {
				state := " present"
				if !dir.Exists {
					state = " missing"
				}
				rows = append(rows, d[1]+state)
			}
		}
		parts = append(parts, "harness dirs: "+strings.Join(rows, ", "))
	}
	if len(h.SettingsEnv) > 0 {
		var rows []string
		for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CONFIG_DIR"} {
			if v, ok := h.SettingsEnv[k]; ok {
				rows = append(rows, k+" "+v)
			}
		}
		parts = append(parts, "claude-config settings.json env: "+strings.Join(rows, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}
