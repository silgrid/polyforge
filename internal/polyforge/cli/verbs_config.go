package cli

// verbs_config.go: `pf config` — the single shipped writer/validator of
// cli.toml (workbench#3). Verbs: get, set, unset, list, validate and
// reload. The command is NoConfigLoad: it loads the file itself because
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
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

var cmdConfig = &Command{
	Name:         "config",
	Group:        GroupMgmt,
	Summary:      "Read and write cli.toml (the one supported writer) and reload the daemon",
	Usage:        "pf config get <key> | set <key> <value> | unset <key> | list | workspace --cwd <dir> | validate | reload",
	NoConfigLoad: true,
	Setup:        func(fs *flag.FlagSet) {},
	Run:          runConfig,
}

func runConfig(ctx *Context, fs *flag.FlagSet, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: expected a subcommand (get, set, unset, list, validate, reload); see `pf help config`")
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
	}
	return fmt.Errorf("config: unknown subcommand %q (get, set, unset, list, workspace, validate, reload)", args[0])
}

func configPath(ctx *Context) string {
	if ctx.ConfigPath != "" {
		return ctx.ConfigPath
	}
	return DefaultConfigPath()
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
		if flat, sections, dkvs, err = parseConfigTOML(path, string(raw)); err != nil {
			return nil, err
		}
	}
	eff := fileCfg
	applyEnv(&eff)
	applyConfigFlags(&eff, ctx)
	return &configView{path: path, raw: raw, exists: exists, flat: flat, sections: sections, dkvs: dkvs, file: fileCfg, eff: eff}, nil
}

func applyConfigFlags(cfg *Config, ctx *Context) {
	if ctx.FlagServer != "" {
		cfg.Server = ctx.FlagServer
	}
	if ctx.FlagAPIKey != "" {
		cfg.APIKey = ctx.FlagAPIKey
	}
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
	sock := ctx.FlagSocket
	if sock == "" {
		sock = os.Getenv("PF_DAEMON_SOCKET")
	}
	if sock == "" {
		if cfg, err := LoadFileConfig(configPath(ctx)); err == nil {
			sock = cfg.DaemonSocket
		}
	}
	if sock == "" {
		sock = defaultDaemonSocket()
	}
	return NewDaemonClient(sock)
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
	case "server":
		if ctx.FlagServer != "" {
			return ctx.FlagServer, "flag"
		}
		if x, ok := env("PF_SERVER"); ok {
			return x, "env"
		}
	case "api_key":
		if ctx.FlagAPIKey != "" {
			return ctx.FlagAPIKey, "flag"
		}
		if x, ok := env("PF_API_KEY"); ok {
			return x, "env"
		}
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
	case "max_concurrent_jobs":
		if d.MaxConcurrentJobs > 0 {
			return strconv.Itoa(d.MaxConcurrentJobs)
		}
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

func runConfigList(ctx *Context) error {
	v, err := loadConfigView(ctx)
	if err != nil {
		return err
	}
	type item struct {
		Key     string         `json:"key"`
		Value   string         `json:"value,omitempty"`
		Set     bool           `json:"set,omitempty"`
		Source  string         `json:"source"`
		Section bool           `json:"section,omitempty"`
		Class   ConfigClass    `json:"class"`
		Schema  map[string]any `json:"schema,omitempty"`
	}
	var items []item
	for _, key := range knownFlatKeyNames() {
		val, src := v.flatValue(ctx, key)
		it := item{Key: key, Source: src, Class: mustConfigClass(key), Schema: ConfigSchema(key)}
		if key == "api_key" {
			it.Set = val != ""
		} else {
			it.Value = val
		}
		items = append(items, it)
	}
	for _, key := range knownDaemonKeyNames() {
		val := daemonValue(v.eff.Daemon, key)
		src := "default"
		if v.hasDaemonKey(key) {
			src = "file"
		}
		items = append(items, item{Key: key, Value: val, Source: src, Class: mustConfigClass(key), Schema: ConfigSchema(key)})
	}
	wsItems := make([]item, 0, len(v.eff.Mapping.Workspaces()))
	for _, ws := range v.eff.Mapping.Workspaces() {
		wsItems = append(wsItems, item{Key: `[workspace."` + ws + `"]`, Value: strings.Join(v.eff.Mapping.ProjectsOf(ws), ","), Source: "file", Section: true, Class: ConfigSectionClass})
	}
	if ctx.JSON {
		return emitJSON(ctx, map[string]any{"file": v.fileFact(), "items": append(items, wsItems...)})
	}
	for _, it := range items {
		val := it.Value
		if it.Key == "api_key" {
			val = "<unset>"
			if it.Set {
				val = "<set>"
			}
		} else if val == "" {
			val = "<unset>"
		}
		fmt.Fprintf(ctx.Stdout, "%s = %s (source: %s, class: %s)\n", it.Key, val, it.Source, it.Class)
	}
	for _, it := range wsItems {
		fmt.Fprintf(ctx.Stdout, "%s projects = [%s] (source: file, class: %s)\n", it.Key, it.Value, it.Class)
	}
	return nil
}

func emitJSON(ctx *Context, v any) error {
	enc := json.NewEncoder(ctx.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---------------------------------------------------------------------------
// validate.

func runConfigValidate(ctx *Context) error {
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
	if isSummarizeKey(pos[0]) {
		return runWorkspaceWrite(ctx, true, pos, exp, wsFlag, cwdFlag)
	}
	if wsFlag != "" || cwdFlag != "" {
		return fmt.Errorf("config set %s: --workspace/--cwd apply only to summarize.* keys", pos[0])
	}
	k, err := parseConfigKey(pos[0])
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
	if isSummarizeKey(pos[0]) {
		return runWorkspaceWrite(ctx, false, pos, exp, wsFlag, cwdFlag)
	}
	if wsFlag != "" || cwdFlag != "" {
		return fmt.Errorf("config unset %s: --workspace/--cwd apply only to summarize.* keys", pos[0])
	}
	k, err := parseConfigKey(pos[0])
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
		default:
			c.unsetFlat(k.name)
		}
		return []byte(c.String())
	}
	return applyConfigEdit(ctx, k, apply, exp)
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
	if k.kind == keyDaemon {
		switch k.name {
		case "max_concurrent_jobs":
			return strings.TrimSpace(val), nil, nil
		default:
			return strconv.Quote(val), nil, nil
		}
	}
	return val, nil, nil
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
// cross-process wait (workbench#41 F1).
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
		if k.isWorkspace() {
			found.Status = "next-claim"
		}
	}
	fmt.Fprintln(ctx.Stdout, renderConfigOutcome(*found))
	return nil
}

func configKeyLabel(k configKey) string {
	if k.isWorkspace() {
		return "[workspace]"
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
