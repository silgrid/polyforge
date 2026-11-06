package cli

// verbs_config.go: `pf config` — the single shipped writer/validator of
// cli.toml. Verbs: get, set, unset, list, workspace,
// validate, reload and migrate (verbs_config_migrate.go). The command is NoConfigLoad: it loads the file itself because
// an unloadable file is the subject, and the step-origin guard runs
// before any of that.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

var cmdConfig = &Command{
	Name:         "config",
	Group:        GroupMgmt,
	Summary:      "Read and write the Host and Cell config files (the one supported writer) and reload the daemon",
	Usage:        "pf [--profile NAME] config get <key> | set <key> <value> [--workspace <path>|--cwd <dir>] | unset <key> [--workspace <path>|--cwd <dir>] | list | workspace --cwd <dir> | validate | reload | migrate   [--host: the Host scope, cli.toml's Host-only keys]",
	NoConfigLoad: true,
	Setup:        func(fs *flag.FlagSet) {},
	Run:          runConfig,
}

func runConfig(ctx *Context, fs *flag.FlagSet, args []string) error {
	args, err := selectConfigScope(ctx, args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("config: expected a subcommand (get, set, unset, list, workspace, validate, reload, migrate); see `pf help config`")
	}
	switch args[0] {
	case "get":
		return runConfigGet(ctx, args[1:])
	case "set":
		return runConfigSet(ctx, args[1:])
	case "unset":
		return runConfigUnset(ctx, args[1:])
	case "list":
		return runConfigList(ctx)
	case "workspace":
		return runConfigWorkspace(ctx, args[1:])
	case "validate":
		return runConfigValidate(ctx)
	case "reload":
		return runConfigReload(ctx)
	case "migrate":
		return runConfigMigrate(ctx, args[1:])
	}
	return fmt.Errorf("config: unknown subcommand %q (get, set, unset, list, workspace, validate, reload, migrate)", args[0])
}

func configPath(ctx *Context) string {
	if ctx.cfgScope.path != "" {
		return ctx.cfgScope.path
	}
	if ctx.ConfigPath != "" {
		return ctx.ConfigPath
	}
	return DefaultConfigPath()
}

// configScope is the file one `pf config` invocation addresses: the Host
// scope (--host: cli.toml's Host-only keys), or one Cell's file chosen by
// the profile resolver — cli.toml for default, cli-<profile>.toml for a
// named Cell. --config PATH addresses exactly that one file (its role comes
// from its name) and never selects a Cell.
type configScope struct {
	path    string
	host    bool
	profile string // the Cell of a Cell scope ("" with --host or --config)
}

// selectConfigScope strips the --host flag from the subcommand's argv and
// fixes ctx.cfgScope. --host together with an explicit --profile is a
// contradictory scope.
func selectConfigScope(ctx *Context, args []string) ([]string, error) {
	var rest []string
	host := false
	for _, a := range args {
		if a == "--host" || a == "-host" {
			host = true
			continue
		}
		rest = append(rest, a)
	}
	switch {
	case host && ctx.FlagProfile != "":
		return nil, fmt.Errorf("config: --host and --profile select contradictory scopes — the Host scope is cli.toml's Host-only keys, a profile selects one Cell's file")
	case ctx.ConfigPath != "":
		ctx.cfgScope = configScope{path: ctx.ConfigPath, host: host}
	case host:
		ctx.cfgScope = configScope{path: DefaultConfigPath(), host: true}
	default:
		res, err := ctx.resolveProfile(true)
		if err != nil {
			return nil, err
		}
		ctx.cfgScope = configScope{
			path:    filepath.Join(filepath.Dir(DefaultConfigPath()), cellcfg.CellFileName(res.Profile)),
			profile: res.Profile,
		}
	}
	return rest, nil
}

// checkKeyScope refuses a key the selected scope does not own: a Cell key
// under --host, a Host-only key while a named Cell is selected. Nothing is
// written on a refusal.
func checkKeyScope(ctx *Context, verb string, k configKey) error {
	section, name := keySection(k)
	hostOnly := cellcfg.HostOnly(section, name)
	sc := ctx.cfgScope
	switch {
	case sc.host && !hostOnly:
		return fmt.Errorf("config %s %s: a Cell key, not a Host key — drop --host to write the default Cell (cli.toml) or select a Cell with --profile (the Host-only keys are %s)",
			verb, k.raw, strings.Join(cellcfg.HostOnlyKeyNames(), ", "))
	case hostOnly && sc.profile != "" && sc.profile != cellcfg.DefaultProfile:
		return fmt.Errorf("config %s %s: %s is a Host-only key and Cell %q is selected (file %s) — Host keys live in %s only: use `pf config %s %s --host`",
			verb, k.raw, name, sc.profile, configPath(ctx), cellcfg.HostFileName, verb, k.raw)
	}
	return nil
}

// keySection maps a parsed key onto the cellcfg partition's (section, name).
func keySection(k configKey) (string, string) {
	switch k.kind {
	case keyDaemon:
		return "daemon", k.name
	case keyWorkspace:
		return "workspace", k.name
	case keySummarize:
		return "summarize", strings.TrimPrefix(k.name, "summarize.")
	case keyPolicy:
		return "memory_policy", k.name
	case keyModel:
		return "model", strings.TrimPrefix(k.name, "model.")
	}
	return "", k.name
}

// listedInScope reports whether a key belongs in the selected scope's list.
func listedInScope(ctx *Context, section, name string) bool {
	hostOnly := cellcfg.HostOnly(section, name)
	if ctx.cfgScope.host {
		return hostOnly
	}
	if ctx.cfgScope.profile != "" && ctx.cfgScope.profile != cellcfg.DefaultProfile {
		return !hostOnly
	}
	return section != "model" // the default Cell's credentials come from operator_agent_dir
}

// configView is one load of the file plus the effective (file+env+flag)
// view and the raw key presence the source reporting needs.
type configView struct {
	path     string
	raw      []byte
	exists   bool
	flat     map[string]string
	sections []workspaceSection
	dkvs     []daemonKV
	file     Config
	eff      Config
}

func loadConfigView(ctx *Context) (*configView, error) {
	path := configPath(ctx)
	raw, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	fileCfg, err := ParseConfigBytes(path, raw)
	if err != nil {
		return nil, err
	}
	flat := map[string]string{}
	var sections []workspaceSection
	var dkvs []daemonKV
	if exists {
		p, err := parseConfigTOML(path, string(raw))
		if err != nil {
			return nil, err
		}
		flat, sections, dkvs = p.flat, p.sections, p.daemon
	}
	eff := fileCfg
	applyEnv(&eff)
	applyConfigFlags(&eff, ctx)
	return &configView{path: path, raw: raw, exists: exists, flat: flat, sections: sections, dkvs: dkvs, file: fileCfg, eff: eff}, nil
}

func applyConfigFlags(cfg *Config, ctx *Context) {
	if ctx.FlagSocket != "" {
		cfg.DaemonSocket = ctx.FlagSocket
	}
	if ctx.FlagUser != "" {
		cfg.UserID = ctx.FlagUser
	}
}

// configDaemon answers the daemon client the config verbs use: flag >
// PF_DAEMON_SOCKET > the file's daemon_socket > the documented default.
func (ctx *Context) configDaemon() *DaemonClient {
	d := NewDaemonClient(ctx.hostSocket())
	if !ctx.cfgScope.host {
		d.Profile = ctx.cfgScope.profile
	}
	return d
}

// ---------------------------------------------------------------------------
// get / list.

// flatValue answers a flat key's effective value and source.
func (v *configView) flatValue(ctx *Context, key string) (string, string) {
	env := func(name string) (string, bool) {
		x := os.Getenv(name)
		return x, x != ""
	}
	switch key {
	case "daemon_socket":
		if ctx.FlagSocket != "" {
			return ctx.FlagSocket, "flag"
		}
		if x, ok := env("PF_DAEMON_SOCKET"); ok {
			return x, "env"
		}
	case "user_id":
		if ctx.FlagUser != "" {
			return ctx.FlagUser, "flag"
		}
		if x, ok := env("PF_USER_ID"); ok {
			return x, "env"
		}
	}
	if x, ok := v.flat[key]; ok {
		return x, "file"
	}
	switch key {
	case "server":
		return defaultServer, "default"
	case "daemon_socket":
		return defaultDaemonSocket(), "default"
	}
	return "", "default"
}

func (v *configView) hasDaemonKey(key string) bool {
	for _, kv := range v.dkvs {
		if kv.key == key {
			return true
		}
	}
	return false
}

// daemonValue renders one [daemon] key's effective value ("" = unset).
func daemonValue(d DaemonSettings, key string) string {
	switch key {
	case "step_timeout":
		if d.StepTimeout != 0 {
			return d.StepTimeout.String()
		}
	case "stall_window":
		if d.StallWindow != 0 {
			return d.StallWindow.String()
		}
	case "harness_root":
		return d.HarnessRoot
	case "operator_agent_dir":
		return d.OperatorAgentDir
	case "python":
		return d.Python
	case "max_concurrent_jobs":
		if d.MaxConcurrentJobs > 0 {
			return strconv.Itoa(d.MaxConcurrentJobs)
		}
	case "max_concurrent_bars":
		if d.MaxConcurrentBars > 0 {
			return strconv.Itoa(d.MaxConcurrentBars)
		}
	case "bar_load_threshold":
		if d.BarLoadThreshold > 0 {
			return strconv.FormatFloat(d.BarLoadThreshold, 'g', -1, 64)
		}
	case "command_path":
		return d.CommandPath
	case "cell_max_concurrent_jobs":
		if d.CellMaxConcurrentJobs > 0 {
			return strconv.Itoa(d.CellMaxConcurrentJobs)
		}
	case "machine_id":
		return d.MachineID
	case "heartbeat_every":
		if d.HeartbeatEvery != 0 {
			return d.HeartbeatEvery.String()
		}
	case "agent_language":
		return d.AgentLanguage
	}
	return ""
}

// daemonDefault renders the value a [daemon] key takes when the file
// leaves it unset, for the keys whose default `pf config` shows ("" =
// shown as <unset>).
func daemonDefault(key string) string {
	switch key {
	case "max_concurrent_bars":
		return strconv.Itoa(DefaultMaxConcurrentBars)
	case "bar_load_threshold":
		return strconv.FormatFloat(DefaultBarLoadThreshold(), 'g', -1, 64)
	case "agent_language":
		return stepcall.DefaultLanguage
	}
	return ""
}

// displayFlat masks api_key: the value is never emitted.
func displayFlat(key, value string) string {
	if key == "api_key" {
		if value == "" {
			return "<unset>"
		}
		return "<set>"
	}
	if value == "" {
		return "<unset>"
	}
	return value
}

func runConfigGet(ctx *Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("config get: expected exactly one key")
	}
	k, err := parseConfigKey(args[0])
	if err != nil {
		return err
	}
	if k.kind == keySummarize || k.kind == keyPolicy {
		return fmt.Errorf("config get %s: read it via `pf config list` (the one read face of the [summarize] table and the memory_policy family)", k.raw)
	}
	if err := checkKeyScope(ctx, "get", k); err != nil {
		return err
	}
	if k.kind == keyModel {
		v, err := loadConfigView(ctx)
		if err != nil {
			return err
		}
		src := "default"
		if v.file.ModelSource != "" {
			src = "file"
		}
		if ctx.JSON {
			return emitJSON(ctx, map[string]any{"key": k.name, "value": v.file.ModelSource, "source": src})
		}
		fmt.Fprintf(ctx.Stdout, "%s = %s (source: %s)\n", k.name, orUnset(v.file.ModelSource), src)
		return nil
	}
	v, err := loadConfigView(ctx)
	if err != nil {
		return err
	}
	if k.isWorkspace() {
		projects := v.eff.Mapping.ProjectsOf(k.name)
		if projects == nil {
			if ctx.JSON {
				return emitJSON(ctx, map[string]any{"key": k.raw, "value": nil, "source": "default"})
			}
			fmt.Fprintf(ctx.Stdout, "%s = <unset> (source: default)\n", k.raw)
			return nil
		}
		if ctx.JSON {
			return emitJSON(ctx, map[string]any{"key": k.raw, "projects": projects, "source": "file"})
		}
		fmt.Fprintf(ctx.Stdout, "%s projects = [%s] (source: file)\n", k.raw, quoteList(projects))
		return nil
	}
	if k.kind == keyDaemon {
		val := daemonValue(v.eff.Daemon, k.name)
		src := "default"
		if v.hasDaemonKey(k.name) {
			src = "file"
		}
		if val == "" {
			val = daemonDefault(k.name)
		}
		if val == "" {
			val = "<unset>"
		}
		if ctx.JSON {
			return emitJSON(ctx, map[string]any{"key": k.name, "value": val, "source": src})
		}
		fmt.Fprintf(ctx.Stdout, "%s = %s (source: %s)\n", k.name, val, src)
		return nil
	}
	val, src := v.flatValue(ctx, k.name)
	if ctx.JSON {
		out := map[string]any{"key": k.name, "source": src}
		if k.name == "api_key" {
			out["value"] = ""
			out["set"] = val != ""
		} else {
			out["value"] = val
		}
		return emitJSON(ctx, out)
	}
	fmt.Fprintf(ctx.Stdout, "%s = %s (source: %s)\n", k.name, displayFlat(k.name, val), src)
	return nil
}

type configFileFact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Exists bool   `json:"exists"`
}

func (v *configView) fileFact() configFileFact {
	f := configFileFact{Path: v.path, Exists: v.exists}
	if v.exists {
		sum := sha256.Sum256(v.raw)
		f.SHA256 = hex.EncodeToString(sum[:])
	}
	return f
}

// configListItem is one row of `pf config list`. Value is the effective
// value: a string for the flat/[daemon] keys (omitted when unset), a list
// or an integer for the summarize keys.
type configListItem struct {
	Key     string         `json:"key"`
	Value   any            `json:"value,omitempty"`
	Set     bool           `json:"set,omitempty"`
	Source  string         `json:"source"`
	Section bool           `json:"section,omitempty"`
	Class   ConfigClass    `json:"class"`
	Schema  map[string]any `json:"schema,omitempty"`
}

func runConfigList(ctx *Context) error {
	v, err := loadConfigView(ctx)
	if err != nil {
		return err
	}
	strValue := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	var items []configListItem
	for _, key := range knownFlatKeyNames() {
		if !listedInScope(ctx, "", key) {
			continue
		}
		val, src := v.flatValue(ctx, key)
		it := configListItem{Key: key, Source: src, Class: mustConfigClass(key), Schema: ConfigSchema(key)}
		if key == "api_key" {
			it.Set = val != ""
		} else {
			it.Value = strValue(val)
		}
		items = append(items, it)
	}
	for _, key := range knownDaemonKeyNames() {
		if !listedInScope(ctx, "daemon", key) {
			continue
		}
		val := daemonValue(v.eff.Daemon, key)
		src := "default"
		if v.hasDaemonKey(key) {
			src = "file"
		}
		value := strValue(val)
		if n, err := strconv.Atoi(val); err == nil && ConfigSchema(key)["kind"] == "int" {
			value = n // an integer key's value is a JSON number (like summarize.interval_sec)
		}
		items = append(items, configListItem{Key: key, Value: value, Source: src, Class: mustConfigClass(key), Schema: ConfigSchema(key)})
	}
	for _, key := range knownSummarizeKeyNames() {
		if !listedInScope(ctx, "summarize", key) {
			continue
		}
		val, src := summarizeValueOf(v.file.Summarize, key)
		items = append(items, configListItem{Key: key, Value: val, Source: src, Class: mustConfigClass(key), Schema: ConfigSchema(key)})
	}
	if listedInScope(ctx, "model", "source") {
		src := "default"
		if v.file.ModelSource != "" {
			src = "file"
		}
		var val any
		if v.file.ModelSource != "" {
			val = v.file.ModelSource
		}
		items = append(items, configListItem{Key: "model.source", Value: val, Source: src, Class: mustConfigClass("model.source"), Schema: ConfigSchema("model.source")})
	}
	wsItems := make([]configListItem, 0, len(v.eff.Mapping.Workspaces()))
	for _, ws := range v.eff.Mapping.Workspaces() {
		if ctx.cfgScope.host {
			break
		}
		wsItems = append(wsItems, configListItem{Key: `[workspace."` + ws + `"]`, Value: strings.Join(v.eff.Mapping.ProjectsOf(ws), ","), Source: "file", Section: true, Class: ConfigSectionClass})
	}
	policy := memoryPolicyList(v.file)
	if ctx.JSON {
		return emitJSON(ctx, map[string]any{"file": v.fileFact(), "items": append(items, wsItems...), "memory_policy": policy})
	}
	for _, it := range items {
		val := "<unset>"
		switch {
		case it.Key == "api_key":
			if it.Set {
				val = "<set>"
			}
		case it.Value == nil:
		default:
			if list, ok := it.Value.([]string); ok {
				val = "[" + quoteList(list) + "]"
			} else {
				val = fmt.Sprint(it.Value)
			}
		}
		fmt.Fprintf(ctx.Stdout, "%s = %s (source: %s, class: %s)\n", it.Key, val, it.Source, it.Class)
	}
	for _, it := range wsItems {
		fmt.Fprintf(ctx.Stdout, "%s projects = [%s] (source: file, class: %s)\n", it.Key, it.Value, it.Class)
	}
	for _, ws := range v.eff.Mapping.Workspaces() {
		entries := policy[ws]
		names := append([]string{workspace.DefaultKey}, v.eff.Mapping.ProjectsOf(ws)...)
		for _, name := range names {
			e := entries[name]
			src := "default"
			if e.Declared {
				src = "file"
			}
			fmt.Fprintf(ctx.Stdout, "[memory_policy.%q.%s] record = [%s] skip = [%s] (source: %s, class: %s)\n", ws, name, quoteList(e.Record), quoteList(e.Skip), src, MemoryPolicyClass)
		}
	}
	return nil
}

// summarizeValueOf answers one summarize key's effective value and
// source: the table's value, else the documented default (models: none —
// the lane is off without the table).
func summarizeValueOf(s *workspace.SummarizeSettings, key string) (any, string) {
	switch key {
	case "summarize.models":
		if s == nil {
			return nil, "default"
		}
		return append([]string{}, s.Models...), "file"
	case "summarize.language":
		if s == nil || s.Language == "" {
			return workspace.DefaultSummarizeLanguage, "default"
		}
		return s.Language, "file"
	case "summarize.interval_sec":
		if s == nil || s.IntervalSec == 0 {
			return workspace.DefaultSummarizeIntervalSec, "default"
		}
		return s.IntervalSec, "file"
	}
	return nil, "default"
}

// policyListEntry is one entry of the memory_policy read: its lists and
// whether cli.toml declares it.
type policyListEntry struct {
	Record   []string `json:"record"`
	Skip     []string `json:"skip"`
	Declared bool     `json:"declared"`
}

// memoryPolicyList answers the `config list` memory_policy object: one
// block per mapped workspace path, carrying its `default` entry (the
// declared lists, else the built-in ones) and every mapped project's OWN
// declared lists (never pre-merged: a project's lists append to
// default's), each with whether it is declared.
func memoryPolicyList(cfg Config) map[string]map[string]policyListEntry {
	builtin := workspace.DefaultMemoryPolicy()
	out := map[string]map[string]policyListEntry{}
	for _, ws := range cfg.Mapping.Workspaces() {
		declared := cfg.MemoryPolicy[ws]
		d, dok := declared[workspace.DefaultKey]
		block := map[string]policyListEntry{
			workspace.DefaultKey: {Record: orList(d.Record, builtin.Record), Skip: orList(d.Skip, builtin.Skip), Declared: dok},
		}
		for _, p := range cfg.Mapping.ProjectsOf(ws) {
			e, ok := declared[p]
			block[p] = policyListEntry{Record: orList(e.Record, []string{}), Skip: orList(e.Skip, []string{}), Declared: ok}
		}
		out[ws] = block
	}
	return out
}

func emitJSON(ctx *Context, v any) error {
	enc := json.NewEncoder(ctx.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---------------------------------------------------------------------------
// validate.

func runConfigValidate(ctx *Context) error {
	if ctx.ConfigPath == "" {
		return runConfigValidateSet(ctx)
	}
	path := configPath(ctx)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config validate: %s does not exist", path)
		}
		return fmt.Errorf("config validate: %w", err)
	}
	cfg, err := ParseConfigBytes(path, raw)
	if err != nil {
		return err
	}
	if err := ValidateConfigForDaemon(cfg); err != nil {
		return fmt.Errorf("config validate: %w", err)
	}
	if ctx.JSON {
		return emitJSON(ctx, map[string]any{"valid": true, "path": path})
	}
	fmt.Fprintf(ctx.Stdout, "config ok: %s is daemon-acceptable\n", path)
	return nil
}

// ---------------------------------------------------------------------------
// set / unset.

func runConfigSet(ctx *Context, args []string) error {
	pos, exp, wsFlag, cwdFlag, err := parseConfigWriteArgs(args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return fmt.Errorf("config set: expected <key> <value>")
	}
	if isWorkspaceKey(pos[0]) {
		return runWorkspaceWrite(ctx, true, pos, exp, wsFlag, cwdFlag)
	}
	k, err := parseConfigKey(pos[0])
	if err != nil {
		return err
	}
	if err := checkKeyScope(ctx, "set", k); err != nil {
		return err
	}
	ws, err := configWritePath(ctx, "set", k, wsFlag, cwdFlag)
	if err != nil {
		return err
	}
	rawValue, projects, err := configSetValue(ctx, k, pos[1:])
	if err != nil {
		return err
	}
	apply := func(text []byte) []byte {
		c := newConfigLines(string(text))
		switch {
		case k.isWorkspace():
			c.setWorkspace(k.name, projects)
		case k.kind == keyDaemon:
			c.setDaemon(k.name, rawValue)
		case k.kind == keySummarize:
			c.setSummarize(strings.TrimPrefix(k.name, "summarize."), rawValue)
		case k.kind == keyModel:
			c.setModel(strings.TrimPrefix(k.name, "model."), rawValue)
		case k.kind == keyPolicy:
			c.setPolicyList(ws, k.name, k.list, projects)
		default:
			c.setFlat(k.name, rawValue)
		}
		return []byte(c.String())
	}
	return applyConfigEdit(ctx, k, apply, exp)
}

func runConfigUnset(ctx *Context, args []string) error {
	pos, exp, wsFlag, cwdFlag, err := parseConfigWriteArgs(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("config unset: expected exactly one key")
	}
	if isWorkspaceKey(pos[0]) {
		return runWorkspaceWrite(ctx, false, pos, exp, wsFlag, cwdFlag)
	}
	k, err := parseConfigKey(pos[0])
	if err != nil {
		return err
	}
	if err := checkKeyScope(ctx, "unset", k); err != nil {
		return err
	}
	ws, err := configWritePath(ctx, "unset", k, wsFlag, cwdFlag)
	if err != nil {
		return err
	}
	apply := func(text []byte) []byte {
		c := newConfigLines(string(text))
		switch {
		case k.isWorkspace():
			c.unsetWorkspace(k.name)
		case k.kind == keyDaemon:
			c.unsetDaemon(k.name)
		case k.kind == keySummarize:
			c.unsetSummarize(strings.TrimPrefix(k.name, "summarize."))
		case k.kind == keyModel:
			c.unsetModel(strings.TrimPrefix(k.name, "model."))
		case k.kind == keyPolicy:
			c.unsetPolicyList(ws, k.name, k.list)
		default:
			c.unsetFlat(k.name)
		}
		return []byte(c.String())
	}
	return applyConfigEdit(ctx, k, apply, exp)
}

// configWritePath applies the path selection of a cli.toml write: a
// memory_policy key needs --workspace <mapped path> or --cwd <dir> (the
// one path whose entry it writes); every other cli.toml key — the flat
// [summarize] keys included — refuses either flag.
func configWritePath(ctx *Context, verb string, k configKey, wsFlag, cwdFlag string) (string, error) {
	if k.kind == keyPolicy {
		if wsFlag == "" && cwdFlag == "" {
			return "", fmt.Errorf("config %s %s: --workspace <path> or --cwd <dir> is required (a memory_policy entry belongs to one mapped workspace path)", verb, k.raw)
		}
		return resolveWorkspaceTarget(ctx, k.raw, wsFlag, cwdFlag)
	}
	if wsFlag != "" || cwdFlag != "" {
		return "", fmt.Errorf("config %s %s: --workspace/--cwd apply only to memory_policy.<entry>.record|skip and the workspace .polyforge.yaml keys (steps.<project>.<step-id>.models|skip); %s is one machine-level cli.toml key", verb, k.raw, k.raw)
	}
	return "", nil
}

// configSetValue answers the text the editor writes for a key and, for a
// workspace section, the projects list. api_key never comes from argv.
func configSetValue(ctx *Context, k configKey, args []string) (string, []string, error) {
	if k.isWorkspace() {
		if len(args) != 1 {
			return "", nil, fmt.Errorf("config set %s: expected one value, projects=<a,b,c>", k.raw)
		}
		projects, err := parseProjectsValue(args[0])
		if err != nil {
			return "", nil, err
		}
		return "", projects, nil
	}
	if k.kind == keyFlat && k.name == "api_key" {
		if len(args) != 0 {
			return "", nil, fmt.Errorf("config set api_key: the value is accepted on stdin only (never as an argument) — pipe it, e.g. `printf %%s \"$KEY\" | pf config set api_key`")
		}
		val, err := readAPIKey(ctx)
		if err != nil {
			return "", nil, err
		}
		if val == "" {
			return "", nil, fmt.Errorf("config set api_key: empty input — nothing was written")
		}
		return val, nil, nil
	}
	if len(args) != 1 {
		return "", nil, fmt.Errorf("config set %s: expected exactly one value", k.raw)
	}
	val := args[0]
	switch k.kind {
	case keySummarize:
		v, err := summarizeTOMLValue(k.name, val)
		return v, nil, err
	case keyPolicy:
		var items []string
		if err := json.Unmarshal([]byte(val), &items); err != nil {
			return "", nil, fmt.Errorf("config set %s: the value is a JSON list of strings, e.g. '[\"a note\", \"another\"]': %v", k.raw, err)
		}
		if items == nil {
			items = []string{}
		}
		return "", items, nil
	}
	if k.kind == keyModel {
		return strconv.Quote(val), nil, nil
	}
	if k.kind == keyDaemon {
		switch k.name {
		case "max_concurrent_jobs", "max_concurrent_bars", "bar_load_threshold", "cell_max_concurrent_jobs":
			return strings.TrimSpace(val), nil, nil
		default:
			return strconv.Quote(val), nil, nil
		}
	}
	return val, nil, nil
}

// summarizeTOMLValue renders the cli.toml text of one [summarize] value:
// models (a JSON/flow list or a comma list) as a quoted array, language
// quoted, interval_sec a bare integer. The values themselves are judged by
// the one table validator when the edited file is re-parsed.
func summarizeTOMLValue(key, raw string) (string, error) {
	switch key {
	case "summarize.models":
		items, err := parseModelList(raw)
		if err != nil {
			return "", err
		}
		return "[" + quoteList(items) + "]", nil
	case "summarize.language":
		if strings.ContainsAny(raw, "\r\n") {
			return "", fmt.Errorf("summarize.language: must be a single line")
		}
		return strconv.Quote(strings.TrimSpace(raw)), nil
	case "summarize.interval_sec":
		return strings.TrimSpace(raw), nil
	}
	return "", unknownConfigKey(key)
}

// readAPIKey reads the api_key from stdin only: a no-echo prompt on a
// tty, a plain line from a pipe.
func readAPIKey(ctx *Context) (string, error) {
	if f, ok := ctx.Stdin.(*os.File); ok && termiosOK(f) {
		return readSecretLine(f)
	}
	return readOneLine(ctx.Stdin)
}

// parseProjectsValue parses `projects=a,b,c` (or `projects=[]`).
func parseProjectsValue(arg string) ([]string, error) {
	arg = strings.TrimSpace(arg)
	if i := strings.IndexByte(arg, '='); i >= 0 {
		if key := strings.TrimSpace(arg[:i]); key != "projects" {
			return nil, fmt.Errorf("config set [workspace]: the only section key is projects=<a,b,c> (got %q)", key)
		}
		arg = strings.TrimSpace(arg[i+1:])
	}
	if arg == "[]" || arg == "" {
		return []string{}, nil
	}
	parts := strings.Split(arg, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// applyConfigEdit is the shared set/unset flow: preflight the proposed
// bytes (parse + shared validator + mapping removal check), then take the
// writer flock and re-run the edit against the current file before the
// atomic write. A refusal writes nothing. The mapping/workspace checks are
// scoped to a write that touches the mapping or leaves a mapping in place
// (owner ruling on review finding 9); a flat/[daemon] write to a
// mapping-less file is accepted and the daemon's start refusal is noted.
// The flock guards only the local read-modify-write: it is released
// before the daemon is notified, because that round trip is a
// cross-process wait ( F1).
func applyConfigEdit(ctx *Context, k configKey, apply func([]byte) []byte, exp configExpectation) error {
	path := configPath(ctx)
	touchesMapping := k.isWorkspace()
	oldRaw, err := os.ReadFile(path)
	oldExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config %s: %w", path, err)
	}
	proposed := apply(oldRaw)
	proposedCfg, err := ParseConfigBytes(path, proposed)
	if err != nil {
		return err
	}
	if err := ValidateConfigWrite(proposedCfg, touchesMapping); err != nil {
		return err
	}
	if !oldExists {
		// a cold file has no mapping to remove; nothing to preflight
	} else if err := preflightMapping(ctx, path, oldRaw, proposedCfg); err != nil {
		return err
	}

	finalCfg, err := editConfigFileLocked(ctx, path, apply, touchesMapping, exp)
	if err != nil {
		return err
	}
	// The flock is free again: this notification is a UDS round trip
	// (POST /config/reload, 120 s client timeout), and holding the writer
	// flock across it would block every other `pf config` writer for a
	// slow or stuck daemon.
	return notifyConfigWrite(ctx, k, finalCfg)
}

// editConfigFileLocked runs the serialized section of a config edit: take
// the writer flock, re-read the file, re-apply the edit, validate the
// edited bytes and land them atomically. The lock is released (by defer,
// on every path out — refusals included) before this returns, and the
// no-mapping note is written under it: the caller's daemon notification
// runs after the release.
func editConfigFileLocked(ctx *Context, path string, apply func([]byte) []byte, touchesMapping bool, exp configExpectation) (Config, error) {
	lock, err := lockConfigFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: lock %s: %w", path, err)
	}
	defer unlockConfigFile(lock)

	curRaw, err := os.ReadFile(path)
	curExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if err := checkFileExpectation(exp, curRaw, curExists); err != nil {
		return Config{}, err
	}
	final := apply(curRaw)
	finalCfg, err := ParseConfigBytes(path, final)
	if err != nil {
		return Config{}, err
	}
	if err := ValidateConfigWrite(finalCfg, touchesMapping); err != nil {
		return Config{}, err
	}
	if err := writeConfigAtomic(path, final); err != nil {
		return Config{}, fmt.Errorf("config: write %s: %w", path, err)
	}
	if finalCfg.Mapping.Empty() {
		fmt.Fprintln(ctx.Stderr, "note: no [workspace] mapping is configured; the daemon refuses to start until one exists — add one with `pf config set '[workspace.\"<path>\"]' projects=...`")
	}
	return finalCfg, nil
}

// preflightMapping refuses a mapping removal/re-route that would strand a
// held job, before the flock and before any write.
func preflightMapping(ctx *Context, path string, oldRaw []byte, proposed Config) error {
	oldCfg, err := ParseConfigBytes(path, oldRaw)
	if err != nil {
		return nil // a broken baseline has no mapping to compare against
	}
	if oldCfg.Mapping.Equal(proposed.Mapping) {
		return nil
	}
	_, cerr := ctx.configDaemon().ConfigCheck(mappingEntries(proposed.Mapping))
	if cerr == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(cerr, &ae) {
		return cerr
	}
	return nil // no daemon running: nothing to strand
}

func mappingEntries(p *mapping.Plan) []ConfigMappingEntry {
	if p == nil {
		return nil
	}
	out := make([]ConfigMappingEntry, 0, len(p.Workspaces()))
	for _, ws := range p.Workspaces() {
		out = append(out, ConfigMappingEntry{Workspace: ws, Projects: p.ProjectsOf(ws)})
	}
	return out
}

// notifyConfigWrite asks the running daemon to reload; with no daemon the
// write still stands and the outcome says it applies at the next start.
func notifyConfigWrite(ctx *Context, k configKey, finalCfg Config) error {
	resp, err := ctx.configDaemon().ConfigReload()
	if err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) {
			return renderConfigNoDaemon(ctx, k)
		}
		return err
	}
	return renderConfigOutcomes(ctx, k, resp)
}

func renderConfigNoDaemon(ctx *Context, k configKey) error {
	key := configKeyLabel(k)
	if ctx.JSON {
		return emitJSON(ctx, ConfigReloadResponse{Outcomes: []ConfigKeyOutcome{{
			Key: key, Class: string(classOfKey(k)), Status: "not-running",
			Detail: "no daemon is running; the change takes effect at the next daemon start",
		}}})
	}
	if classOfKey(k) == ConfigCLIOnly {
		fmt.Fprintf(ctx.Stdout, "%s: saved (cli-only); applies to the next pf invocation\n", key)
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "%s: saved; takes effect at the next daemon start\n", key)
	return nil
}

// renderConfigReloadNoDaemon is R8's no-daemon answer for an explicit
// reload: nothing was saved, so say the file on disk is unchanged.
func renderConfigReloadNoDaemon(ctx *Context) error {
	if ctx.JSON {
		return emitJSON(ctx, map[string]any{"reload": "no daemon is running"})
	}
	fmt.Fprintln(ctx.Stdout, "config reload: no daemon is running; nothing was reloaded, and the next daemon start reads the file on disk")
	return nil
}

func renderConfigOutcomes(ctx *Context, k configKey, resp *ConfigReloadResponse) error {
	if ctx.JSON {
		return emitJSON(ctx, resp)
	}
	key := configKeyLabel(k)
	var found *ConfigKeyOutcome
	for i := range resp.Outcomes {
		if resp.Outcomes[i].Key == key {
			found = &resp.Outcomes[i]
			break
		}
	}
	if found == nil {
		found = &ConfigKeyOutcome{Key: key, Class: string(classOfKey(k)), Status: "saved"}
		if k.isWorkspace() || k.kind == keyPolicy {
			found.Status = "next-claim"
		}
	}
	fmt.Fprintln(ctx.Stdout, renderConfigOutcome(*found))
	return nil
}

func configKeyLabel(k configKey) string {
	switch {
	case k.isWorkspace():
		return "[workspace]"
	case k.kind == keyPolicy:
		return MemoryPolicyLabel
	}
	return k.name
}

func renderConfigOutcome(o ConfigKeyOutcome) string {
	switch o.Status {
	case "applied":
		s := o.Key + ": applied"
		if o.Class != "" {
			s += " (" + o.Class + ")"
		}
		return s
	case "next-claim":
		return o.Key + ": next-claim (applies to the next claim; no restart needed)"
	case "pending-restart":
		return o.Key + ": pending restart (the running daemon keeps its current value)"
	case "overridden":
		return o.Key + ": overridden by flag (" + o.Detail + ")"
	case "not-running":
		return o.Key + ": saves; takes effect at the next daemon start"
	case "cli-only":
		return o.Key + ": saved (cli-only)"
	}
	if o.Detail != "" {
		return o.Key + ": " + o.Status + " (" + o.Detail + ")"
	}
	return o.Key + ": " + o.Status
}

// ---------------------------------------------------------------------------
// reload.

func runConfigReload(ctx *Context) error {
	// The daemon is the single authority: the CLI does NOT pre-validate the
	// file, so a bad reload ALWAYS reaches POST /config/reload and yields
	// the daemon's typed error plus its one redacted config_reload_failed
	// event carrying key names only (R14/O5).
	resp, err := ctx.configDaemon().ConfigReload()
	if err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) {
			return renderConfigReloadNoDaemon(ctx)
		}
		return err
	}
	if ctx.JSON {
		return emitJSON(ctx, resp)
	}
	for _, o := range resp.Outcomes {
		fmt.Fprintln(ctx.Stdout, renderConfigOutcome(o))
	}
	return nil
}

// runConfigValidateSet validates the whole Host set: the Host file (it must
// be daemon-acceptable — an invalid one blocks the Host start) and every
// named Cell file, each on its own (an invalid Cell is reported, never
// hidden behind another). Exit 1 when any file is invalid.
func runConfigValidateSet(ctx *Context) error {
	set, err := LoadHostSet("")
	if err != nil {
		return fmt.Errorf("config validate: %w", err)
	}
	type row struct {
		Profile string `json:"profile"`
		Path    string `json:"path"`
		Valid   bool   `json:"valid"`
		Reason  string `json:"reason,omitempty"`
	}
	hostReason := ""
	if _, statErr := os.Stat(set.HostPath); statErr != nil {
		hostReason = set.HostPath + " does not exist"
	} else if !set.Host.Mapping.Empty() {
		if verr := ValidateConfigForDaemon(set.Host); verr != nil {
			hostReason = verr.Error()
		}
	} else if verr := validateOperatorAgentDir(set.Host); verr != nil {
		hostReason = verr.Error()
	}
	rows := []row{{Profile: cellcfg.DefaultProfile, Path: set.HostPath, Valid: hostReason == "", Reason: hostReason}}
	for _, c := range set.Cells {
		rows = append(rows, row{Profile: c.Profile, Path: c.Path, Valid: c.Invalid == "", Reason: c.Invalid})
	}
	bad := 0
	for _, r := range rows {
		if !r.Valid {
			bad++
		}
	}
	if ctx.JSON {
		if err := emitJSON(ctx, map[string]any{"valid": bad == 0, "path": set.HostPath, "cells": rows}); err != nil {
			return err
		}
	} else {
		for _, r := range rows {
			if r.Valid {
				fmt.Fprintf(ctx.Stdout, "config ok: %s (Cell %s)\n", r.Path, r.Profile)
			} else {
				fmt.Fprintf(ctx.Stdout, "config INVALID: %s (Cell %s): %s\n", r.Path, r.Profile, r.Reason)
			}
		}
	}
	if bad > 0 {
		return fmt.Errorf("config validate: %d of %d files invalid", bad, len(rows))
	}
	return nil
}

func orUnset(s string) string {
	if s == "" {
		return "<unset>"
	}
	return s
}
