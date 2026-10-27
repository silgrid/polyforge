package daemon

// reload.go: the daemon's config reload entry point (workbench#3). A
// reload re-reads the daemon's own cli.toml, runs the SAME shared
// validator `pf config` runs, and applies each key by its class:
//
//   - live: max_concurrent_jobs (the queue), step_timeout/stall_window/
//     claude_bin (the live state the runtime reads);
//   - next-claim: the [workspace] mapping (additions take effect at the
//     next claim; a removal/re-route that would strand a held job is
//     refused and the work items are listed);
//   - restart-only: server, api_key, daemon_socket, harness_root,
//     operator_agent_dir — recorded as pending against the running
//     baseline, never applied;
//   - cli-only: user_id (the daemon never reads it).
//
// A failure applies NOTHING, emits one redacted event (key names only)
// and answers a typed error while the daemon keeps running.
//
// Locking (AGENTS.md runtime locking): all file I/O and validation run
// BEFORE any lock a reader takes. `reloadApplyMu` serializes reloads and
// guards lastAppliedFile; no reader takes it, so a reload never blocks
// GET /status. `reloadMu` guards only the in-memory reload bookkeeping
// and is held for the publish, never across I/O.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

// plan answers the live mapping (never nil).
func (d *Daemon) plan() *mapping.Plan {
	if d.live != nil {
		return d.live.Mapping()
	}
	if d.cfg.Mapping == nil {
		return &mapping.Plan{}
	}
	return d.cfg.Mapping
}

// reloadPath answers the file this daemon reloads.
func (d *Daemon) reloadPath() string {
	if d.cfg.ConfigPath != "" {
		return d.cfg.ConfigPath
	}
	return cli.DefaultConfigPath()
}

// reloadConfig re-reads and applies the file. The read, parse, validation
// and mapping-stranding check all run before the bookkeeping lock is
// taken; the apply happens under reloadApplyMu (which no reader takes),
// and the result is published under a short-held reloadMu.
func (d *Daemon) reloadConfig() (*cli.ConfigReloadResponse, error) {
	d.reloadApplyMu.Lock()
	defer d.reloadApplyMu.Unlock()

	path := d.reloadPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, d.reloadFailed(api.ErrDaemonBadInput,
			fmt.Sprintf("config reload: cannot read %s: %v", path, err), nil)
	}
	newCfg, err := cli.ParseConfigBytes(path, raw)
	if err != nil {
		return nil, d.reloadFailed(api.ErrDaemonBadInput, "config reload: "+err.Error(), nil)
	}
	if err := cli.ValidateConfigForDaemon(newCfg); err != nil {
		return nil, d.reloadFailed(api.ErrDaemonBadInput, "config reload: "+err.Error(), nil)
	}
	// The mapping removal/re-route check runs before anything is applied:
	// all-or-nothing.
	if err := d.checkMappingRemoval(newCfg.Mapping); err != nil {
		return nil, d.reloadFailed(api.ErrDaemonConflict, err.Error(), nil)
	}

	// Everything validated — apply. startup is the FILE-ONLY baseline the
	// running daemon started from; prev is the file config as of the last
	// successful reload. Restart-only keys compare against startup (they
	// never apply until a restart, so setting one back to the baseline
	// clears it from pending); live and CLI-only keys compare against
	// prev (a subsequent unrelated reload must not reapply or re-report
	// them).
	startup := d.cfg.StartupFile
	prev := d.lastAppliedFile

	keys := cli.ConfigKeys()
	outcomes := make([]cli.ConfigKeyOutcome, 0, len(keys)+1)
	var pending, overridden []string
	add := func(key string, class cli.ConfigClass, status, detail string) {
		outcomes = append(outcomes, cli.ConfigKeyOutcome{Key: key, Class: string(class), Status: status, Detail: detail})
	}

	// Every key's class AND the path a change takes (apply live / record
	// pending / report cli-only) come from the one key model in cli; this
	// loop only knows how to read and apply a key's value.
	for _, key := range keys {
		class, ok := cli.ConfigClassOf(key)
		if !ok {
			continue
		}
		baseline := prev
		if class == cli.ConfigRestartOnly || class == cli.ConfigCLIOnly {
			baseline = startup
		}
		if !configValueChanged(newCfg, baseline, key) {
			continue
		}
		if d.cfg.FlagPinned[key] {
			overridden = append(overridden, key)
			add(key, class, "overridden", pinnedDetail(key))
			continue
		}
		switch class {
		case cli.ConfigCLIOnly:
			// CLI-only: never reaches the daemon; report only a change.
			add(key, class, "cli-only", "")
		case cli.ConfigRestartOnly:
			pending = append(pending, key)
			add(key, class, "pending-restart", "")
		case cli.ConfigLive:
			if err := d.applyLive(key, newCfg); err != nil {
				return nil, d.reloadFailed(api.ErrDaemonBadInput, "config reload: "+err.Error(), []string{key})
			}
			add(key, class, "applied", "")
		}
	}

	// The mapping is next-claim: report it only when it actually changed
	// (a set that rewrites the same projects is handled by the CLI
	// fallback, which still reports the next-claim class).
	if !d.plan().Equal(newCfg.Mapping) {
		add("[workspace]", cli.ConfigSectionClass, "next-claim", "")
	}
	d.live.SetMapping(newCfg.Mapping)
	d.lastAppliedFile = newCfg

	sort.Strings(pending)
	sort.Strings(overridden)
	resp := &cli.ConfigReloadResponse{
		Outcomes:   outcomes,
		Pending:    pending,
		Overridden: overridden,
	}
	// Publish the reload bookkeeping under the short-held status lock —
	// no I/O here.
	d.reloadMu.Lock()
	d.reloadStatus = cli.ReloadStatus{Pending: pending, Overridden: overridden, Outcomes: outcomes}
	d.reloadMu.Unlock()

	_ = d.obs.Events().Append("", observe.EvConfigReload, map[string]any{
		"applied_keys": liveKeys(outcomes),
		"pending":      pending,
		"overridden":   overridden,
	})
	return resp, nil
}

// reloadFailed records one redacted failure event (key names only) and
// answers the typed error.
func (d *Daemon) reloadFailed(code, detail string, keys []string) error {
	if len(keys) == 0 {
		keys = errKeyNames(detail)
	}
	if d.obs != nil {
		_ = d.obs.Events().Append("", observe.EvConfigReloadFailed, map[string]any{
			"keys": keys,
		})
	}
	return &api.Error{Code: code, Detail: detail}
}

// errKeyNames extracts the known key names an error message mentions —
// the redacted event carries names only, never a value.
func errKeyNames(detail string) []string {
	var out []string
	for _, key := range cli.ConfigKeys() {
		if strings.Contains(detail, key) {
			out = append(out, key)
		}
	}
	return out
}

func liveKeys(outs []cli.ConfigKeyOutcome) []string {
	var out []string
	for _, o := range outs {
		if o.Status == "applied" {
			out = append(out, o.Key)
		}
	}
	return out
}

// pinnedDetail names the explicit flag that pins a key's value.
func pinnedDetail(key string) string {
	switch key {
	case "claude_bin":
		return "the daemon was started with --claude-bin"
	case "max_concurrent_jobs":
		return "the daemon was started with --max-concurrent-jobs"
	}
	return "the daemon was started with an explicit flag"
}

// applyLive applies one live key's file value to the running daemon.
// max_concurrent_jobs is re-applied ONLY when its file value changed
// since the last applied config (the caller's change check), so an
// unrelated reload never undoes a later `pf ops max` (last action wins);
// an unset cap restores the documented default.
func (d *Daemon) applyLive(key string, cfg cli.Config) error {
	switch key {
	case "claude_bin":
		d.live.SetClaudeBin(cfg.ClaudeBin)
	case "step_timeout":
		d.live.SetStepTimeout(cfg.Daemon.StepTimeout)
	case "stall_window":
		d.live.SetStallWindow(cfg.Daemon.StallWindow)
	case "max_concurrent_jobs":
		n := cfg.Daemon.MaxConcurrentJobs
		if n <= 0 {
			n = cli.DefaultMaxConcurrentJobs
		}
		if err := d.q.SetMaxConcurrent(n); err != nil {
			return err
		}
		d.reloadMu.Lock()
		d.maxConfigured = n
		d.reloadMu.Unlock()
		return nil
	}
	return nil
}

// configValueChanged reports whether one key's FILE value differs from a
// baseline (startup for restart-only/CLI-only keys, the last applied file
// for live ones).
func configValueChanged(newCfg, baseline cli.Config, key string) bool {
	switch key {
	case "server":
		return newCfg.Server != baseline.Server
	case "api_key":
		return newCfg.APIKey != baseline.APIKey
	case "daemon_socket":
		return newCfg.DaemonSocket != baseline.DaemonSocket
	case "user_id":
		return newCfg.UserID != baseline.UserID
	case "claude_bin":
		return newCfg.ClaudeBin != baseline.ClaudeBin
	case "step_timeout":
		return newCfg.Daemon.StepTimeout != baseline.Daemon.StepTimeout
	case "stall_window":
		return newCfg.Daemon.StallWindow != baseline.Daemon.StallWindow
	case "harness_root":
		return newCfg.Daemon.HarnessRoot != baseline.Daemon.HarnessRoot
	case "operator_agent_dir":
		return newCfg.Daemon.OperatorAgentDir != baseline.Daemon.OperatorAgentDir
	case "max_concurrent_jobs":
		return newCfg.Daemon.MaxConcurrentJobs != baseline.Daemon.MaxConcurrentJobs
	}
	return false
}

// checkMappingRemoval refuses a mapping change that unmaps or re-routes a
// project with a held (in-flight) job, listing the affected work items.
func (d *Daemon) checkMappingRemoval(proposed *mapping.Plan) error {
	var affected []string
	for _, ji := range d.rt.List() {
		oldWS, err := d.plan().WorkspaceFor(ji.Project)
		if err != nil {
			continue
		}
		newWS, err := proposed.WorkspaceFor(ji.Project)
		if err != nil || newWS != oldWS {
			affected = append(affected, ji.WorkItem)
		}
	}
	if len(affected) == 0 {
		return nil
	}
	sort.Strings(affected)
	return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf(
		"refusing the mapping change: it removes or re-routes project(s) held by in-flight work item(s) %s — finish or cancel them first, or leave the mapping unchanged",
		strings.Join(affected, ", "))}
}

// checkMappingProposal is POST /config/check's half: validate a proposed
// mapping and refuse a stranding removal without writing anything.
func (d *Daemon) checkMappingProposal(entries []cli.ConfigMappingEntry) error {
	me := make([]mapping.Entry, 0, len(entries))
	for _, e := range entries {
		me = append(me, mapping.Entry{Workspace: e.Workspace, Projects: e.Projects})
	}
	plan, err := mapping.Build(me)
	if err != nil {
		return &api.Error{Code: api.ErrDaemonBadInput, Detail: err.Error()}
	}
	return d.checkMappingRemoval(plan)
}
