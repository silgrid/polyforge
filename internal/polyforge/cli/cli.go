package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/version"
)

// Context carries the resolved configuration and I/O for one command
// invocation.
type Context struct {
	Cfg    Config
	JSON   bool // --json: machine output, server JSON as-is
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// ConfigPath is the raw --config global flag ("" = the documented
	// default location). Only the NoConfigLoad verbs (doctor, init, config)
	// need it; everything else reads the resolved Cfg.
	ConfigPath string
	// FlagServer/FlagAPIKey/FlagSocket/FlagUser carry the raw global flag
	// values so `pf config` can report a key's source as flag|env|file|default.
	FlagServer, FlagAPIKey, FlagSocket, FlagUser string
	// Step is the work item of the workflow step this invocation runs in
	// ("" = not inside a step; stepctx.go).
	Step string
	// Task is the task id of the one-shot task agent this invocation runs
	// in ("" = not in a task; stepctx.go).
	Task string
	// FlagProfile is the raw global --profile value ("" = absent): the
	// first source of the profile resolver (profile.go).
	FlagProfile string
	// cfgScope is the file a `pf config` invocation addresses
	// (verbs_config.go selectConfigScope).
	cfgScope configScope
	// resolved caches the invocation's profile resolution (cellProfile);
	// resolveErr is its fail-closed answer when the Host's effective
	// index could not be read (every business request refuses with it).
	resolved   *Resolution
	resolveErr error
}

// aihub returns the business client: every AIHub call goes through the
// daemon's data-plane proxy (POST|GET… /aihub/…) to the resolved Cell's
// effective connection. The CLI holds no Hub credential path of its own
// and never dials a Hub; with the daemon down a business call fails
// explicitly (DAEMON-410).
func (ctx *Context) aihub() *aihubclient.Client {
	profile := ""
	if ctx.Step == "" && ctx.Task == "" {
		profile = ctx.cellProfile()
		if ctx.resolveErr != nil {
			// the Cell is unknown: every call answers the refusal before
			// any dial, so nothing reaches the daemon or a Hub
			c := aihubclient.New(proxyBase, "")
			c.HTTP = &http.Client{Transport: refusingTransport{err: ctx.resolveErr}}
			return c
		}
	}
	return newProxyClient(ctx.Cfg.DaemonSocket, profile)
}

// refusingTransport answers every request with err and dials nothing.
type refusingTransport struct{ err error }

func (t refusingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

// proxyBase is the decorative base URL of the daemon-mediated client (the
// transport dials the socket; the path prefix is the proxy route).
const proxyBase = "http://pf-daemon/aihub"

// newProxyClient builds an aihub client whose transport is the daemon's
// Unix socket and whose requests carry the routed Cell (profile "" = the
// default Cell). A dial failure is the typed DAEMON-410 refusal.
func newProxyClient(sock, profile string) *aihubclient.Client {
	c := aihubclient.New(proxyBase, "")
	c.HTTP = &http.Client{Timeout: 120 * time.Second, Transport: daemonDownTransport{sock: sock, rt: NewDaemonClient(sock).HTTP.Transport}}
	if profile != "" {
		c.Header = http.Header{protocol.ProfileHeader: []string{profile}}
	}
	return c
}

// daemonDownTransport turns a failed dial of the daemon socket into the
// explicit business refusal: no daemon, no business — never a direct Hub
// fallback.
type daemonDownTransport struct {
	sock string
	rt   http.RoundTripper
}

func (t daemonDownTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.rt.RoundTrip(req)
	var op *net.OpError
	if err != nil && errors.As(err, &op) && op.Op == "dial" {
		return nil, &api.Error{Code: api.ErrDaemonGone, Detail: fmt.Sprintf(
			"the daemon does not answer (dial unix %s: %v): AIHub business goes through the daemon's Cell connection and never dials a Hub directly — start it with `pf daemon ensure`; local commands (pf config, pf doctor, pf init, pf profile) work without it",
			t.sock, op.Err)}
	}
	return resp, err
}

// daemon returns the daemon-socket client for the resolved config. Outside
// a step or task it stamps the resolved Cell (profile.go) on every request;
// a step or task agent sends no header — the Host routes it by its own
// ownership record.
func (ctx *Context) daemon() *DaemonClient {
	d := NewDaemonClient(ctx.Cfg.DaemonSocket)
	if ctx.Cfg.UserID != "" {
		d.Origin = &SubmitOrigin{Owner: ctx.Cfg.UserID}
	}
	if ctx.Step == "" && ctx.Task == "" {
		d.Profile = ctx.cellProfile()
		if ctx.resolveErr != nil {
			d.Profile, d.ProfileErr = unresolvedProfile, ctx.resolveErr
		}
	}
	return d
}

// cellProfile resolves (once) the Cell this invocation's Cell-scoped
// requests go to. An invalid --profile spelling still routes the raw
// value, so the Host refuses it explicitly; an unreadable effective index
// is kept in resolveErr (the business clients refuse with it).
func (ctx *Context) cellProfile() string {
	if ctx.resolved == nil {
		res, err := ctx.resolveProfile(false)
		var ae *api.Error
		switch {
		case errors.As(err, &ae) && ae.Code == api.ErrDaemonGone:
			ctx.resolveErr = err
			res = Resolution{}
		case err != nil:
			res = Resolution{Profile: ctx.FlagProfile, Source: ProfileSourceFlag}
		}
		ctx.resolved = &res
	}
	return ctx.resolved.Profile
}

// Command is one `pf <name>` subcommand.
type Command struct {
	Name    string
	Summary string
	Usage   string
	// Group is the help group the verb is listed under; "" = dispatchable
	// but not surfaced.
	Group string
	// NoConfigLoad marks verbs that load the config THEMSELVES because
	// an unloadable config is the verb's subject, not an early exit
	// (`pf doctor`): Run skips the shared pre-load and hands the verb a
	// defaults Context plus the raw --config path (Context.ConfigPath).
	NoConfigLoad bool
	Setup        func(fs *flag.FlagSet)
	Run          func(ctx *Context, fs *flag.FlagSet, args []string) error
}

// commands is the full registry (help grouping is per-command metadata,
// Command.Group, not position): the 13 routing verbs — 6 direct to aihub
// (file, queue, cancel, recall, remember, memory; the blocked exits are
// `resume` = continue and `cancel` = abandon, v2-converge §7) + 7 on the
// daemon UDS face (submit, watch, answer, pause, resume, wrap, ops) — with
// `chat` (the harness chat identity, also GroupExec) between ops and watch
// and `result` (a workflow step's result delivery, #214, also GroupExec)
// after watch, `ask` and `step` (a step's one entry point, workflow
// contract 4) after it, then the management face M1–M5, then the
// machine health face (doctor, #113), the build version (version, #196:
// the verb form of the `--version` flag) and the workspace layout (init,
// #177), then the daemon and aihub server placeholders (help-only:
// cmd/pf/main.go intercepts both real launches).
var commands = []*Command{
	cmdFile,
	cmdQueue,
	cmdCancel,
	cmdRecall,
	cmdRemember,
	cmdMemory,
	cmdSubmit,
	cmdAnswer,
	cmdPause,
	cmdResume,
	cmdWrap,
	cmdOps,
	cmdWatch,
	cmdTask,
	cmdStep,
	cmdMsg,
	cmdUsers,
	cmdApiKeys,
	cmdMachines,
	cmdProjects,
	cmdBackup,
	cmdCell,
	cmdMigrate,
	cmdDoctor,
	cmdVersion,
	cmdProfile,
	cmdInit,
	cmdConfig,
	cmdWorkflows,
	cmdDaemon,
	cmdAihubServer,
}

// Run executes the CLI with argv (without the program name) and returns
// the process exit code:
//
//	0 success
//	1 client-class failure (local validation, a sub-action's flag or
//	  argument error, 4xx wire answers: not-found, conflict, forbidden,
//	  bad input, transport failure)
//	2 server-class failure (5xx wire answers), an unknown verb, and a
//	  parse error of the global or the verb's own (Setup) flag set
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	gfs := flag.NewFlagSet("pf", flag.ContinueOnError)
	gfs.SetOutput(stderr)
	gfs.Usage = func() { printUsage(stderr) }
	jsonFlag := gfs.Bool("json", false, "machine-readable output (server JSON as-is)")
	cfgFlag := gfs.String("config", "", "config file (default ~/.config/polyforge/cli.toml)")
	// --server/--api-key are parsed only to be refused (and PF_SERVER/
	// PF_API_KEY are refused, or ignored with a notice on the local repair
	// verbs): a Cell's connection lives in its config file and business
	// goes through the daemon (refuseConnectionOverride).
	serverFlag := gfs.String("server", "", "refused: a Cell's AIHub connection is configured, never overridden per command")
	apiKeyFlag := gfs.String("api-key", "", "refused: a Cell's AIHub credential is configured, never overridden per command")
	socketFlag := gfs.String("socket", "", "daemon unix socket (env PF_DAEMON_SOCKET)")
	userFlag := gfs.String("user", "", "caller user id, e.g. for answer.answered_by (env PF_USER_ID)")
	profileFlag := gfs.String("profile", "", "the Cell (profile) a Cell-scoped command targets (env PF_PROFILE; else the cwd's mapped Cell, else default)")
	versionFlag := gfs.Bool("version", false, "print version and exit")

	if err := gfs.Parse(argv); err != nil {
		return 2
	}
	// --version is handled before verb dispatch (and before the
	// empty-positional usage branch): with no verb left after the flag,
	// Run would otherwise fall into the usage dump. Exit 0, print the
	// build-injected version string, never usage. The verb form of this
	// face is `pf version` (verbs_version.go): same source, same shapes.
	if *versionFlag {
		if *jsonFlag {
			fmt.Fprintln(stdout, versionJSON())
		} else {
			fmt.Fprintln(stdout, version.Version)
		}
		return 0
	}
	args := gfs.Args()
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	if args[0] == "help" {
		if len(args) == 2 && args[1] == helpTopicExtensions {
			printExtensions(stdout)
			return 0
		}
		if len(args) == 2 {
			return printCommandUsage(stdout, args[1])
		}
		printUsage(stdout)
		return 0
	}

	cmd := lookupCommand(args[0])
	if cmd == nil {
		fmt.Fprintf(stderr, "pf: unknown command %q (see `pf help`)\n", args[0])
		return 2
	}

	// A task agent never drives the daemon and cannot use the aihub-direct
	// verbs: its write verbs are refused here, before the
	// config read and before any wire call — the refusal must not depend on
	// a readable cli.toml or a reachable daemon. The spawn env carries no
	// aihub key and task mode clears it, but the shared HOME keeps cli.toml
	// readable: an accident guard, not a boundary. A process carrying BOTH
	// marker families is a step first (stepRefusal below is the stricter
	// guard) and is not treated as a task.
	taskID := taskProcessID()
	if taskID != "" && stepWorkItem() == "" {
		if err := taskRefusal(cmd); err != nil {
			renderError(&Context{JSON: *jsonFlag, Stdout: stdout, Stderr: stderr}, err)
			return exitCode(err)
		}
	}

	cfg := Config{
		Server:       defaultServer,
		DaemonSocket: defaultDaemonSocket(),
		Mapping:      &mapping.Plan{},
	}
	if !cmd.NoConfigLoad {
		loaded, err := LoadConfig(*cfgFlag)
		if err != nil {
			fmt.Fprintf(stderr, "pf: %v\n", err)
			return 1
		}
		cfg = loaded
	}
	// Flags override environment, environment overrides the file.
	if *socketFlag != "" {
		cfg.DaemonSocket = *socketFlag
	}
	if *userFlag != "" {
		cfg.UserID = *userFlag
	}

	ctx := &Context{
		Cfg:        cfg,
		JSON:       *jsonFlag,
		Stdout:     stdout,
		Stderr:     stderr,
		Stdin:      stdin,
		ConfigPath: *cfgFlag,
		FlagServer: *serverFlag,
		FlagAPIKey: *apiKeyFlag,
		FlagSocket: *socketFlag,
		FlagUser:   *userFlag,

		FlagProfile: *profileFlag,
	}
	if wi := stepWorkItem(); wi != "" {
		if err := stepRefusal(cmd); err != nil {
			renderError(ctx, err)
			return exitCode(err)
		}
		// no key, and an unresolvable server: nothing reaches aihub directly
		ctx.Step, ctx.Cfg.APIKey, ctx.Cfg.Server = wi, "", "http://step-mode.invalid"
	}
	if err := refuseConnectionOverride(cmd, *serverFlag, *apiKeyFlag, args[1:]); err != nil {
		renderError(ctx, err)
		return exitCode(err)
	}
	warnInheritedConnectionEnv(cmd, stderr)

	if taskID != "" && ctx.Step == "" {
		// no key, and an unresolvable server: nothing reaches aihub directly
		ctx.Task, ctx.Cfg.APIKey, ctx.Cfg.Server = taskID, "", "http://task-mode.invalid"
	}
	if (ctx.Step != "" || ctx.Task != "") && *profileFlag != "" {
		err := &api.Error{Code: api.ErrDaemonForbidden, Detail: fmt.Sprintf(
			"--profile %s refused inside a workflow step or task: an agent acts only within its owning Cell, which the daemon resolves from its own records", *profileFlag)}
		renderError(ctx, err)
		return exitCode(err)
	}

	fs := flag.NewFlagSet("pf "+cmd.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cmd.Setup(fs)
	pos, perr := parseCommandArgs(fs, args[1:])
	if perr != nil {
		return 2
	}
	if err := cmd.Run(ctx, fs, pos); err != nil {
		renderError(ctx, err)
		return exitCode(err)
	}
	return 0
}

// exitCode maps an error to the process exit code from the typed wire
// status: 4xx → 1, 5xx → 2. Transport failures and local validation
// carry no code and stay 1.
func exitCode(err error) int {
	var ae *api.Error
	if errors.As(err, &ae) && ae.HTTPStatus() >= 500 {
		return 2
	}
	return 1
}

// renderError prints an error with the typed code prominent; --json mode
// emits the typed envelope on stderr INSTEAD of the human line (v0
// semantics: machine consumers get one JSON document, not both).
func renderError(ctx *Context, err error) {
	var ae *api.Error
	if errors.As(err, &ae) {
		if ctx.JSON {
			if b, merr := json.Marshal(ae); merr == nil {
				fmt.Fprintln(ctx.Stderr, string(b))
				return
			}
		}
		fmt.Fprintf(ctx.Stderr, "pf: %s\n", ae.Error())
		return
	}
	fmt.Fprintf(ctx.Stderr, "pf: %v\n", err)
}

func lookupCommand(name string) *Command {
	for _, c := range commands {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// parseCommandArgs parses the command tail. For commands that registered
// flags in Setup, flags may appear anywhere (before, between or after
// positionals) — humans write `pf watch wi_a --after 5` as naturally
// as `pf watch --after 5 wi_a`. For flag-less commands parsing stops
// at the first positional and the rest is handed over verbatim for the
// sub-action to parse.
func parseCommandArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	if !fsHasFlags(fs) {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		return fs.Args(), nil
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		switch {
		case len(rest) == 0:
			return pos, nil
		case len(rest) == len(args):
			// No flag was consumed: rest[0] is a positional.
			pos = append(pos, rest[0])
			args = rest[1:]
		default:
			// Flags were consumed; continue after them.
			args = rest
		}
	}
}

func fsHasFlags(fs *flag.FlagSet) bool {
	has := false
	fs.VisitAll(func(*flag.Flag) { has = true })
	return has
}

// Help groups (Command.Group), in print order. A verb with an empty Group
// stays dispatchable but is not listed.
const (
	GroupData    = "data"
	GroupExec    = "exec"
	GroupMgmt    = "mgmt"
	GroupDiag    = "diag"
	GroupProcess = "process"
)

var helpGroups = []struct{ id, title string }{
	{GroupData, "Data-plane verbs (via the daemon to the Cell's aihub):"},
	{GroupExec, "Execution-plane verbs (local daemon UDS face):"},
	{GroupMgmt, "Management face (M1–M5):"},
	{GroupDiag, "Diagnostics:"},
	{GroupProcess, "Process:"},
}

// printUsage lists all commands grouped by Command.Group, in registry order.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "pf — polyforge client CLI")
	fmt.Fprintln(w)
	for _, g := range helpGroups {
		fmt.Fprintln(w, g.title)
		for _, c := range commands {
			if c.Group == g.id {
				fmt.Fprintf(w, "  %-14s %s\n", c.Name, c.Summary)
			}
		}
	}
	fmt.Fprintln(w, "Topics (pf help <topic>):")
	fmt.Fprintf(w, "  %-14s %s\n", helpTopicExtensions, "where each user extension point is documented (docs/extending.md)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags (before the command):")
	fmt.Fprintln(w, "  --json          machine-readable output (server JSON as-is)")
	fmt.Fprintln(w, "  --config PATH   config file (default ~/.config/polyforge/cli.toml)")
	fmt.Fprintln(w, "  --socket PATH   daemon unix socket (env PF_DAEMON_SOCKET)")
	fmt.Fprintln(w, "  --user ID       caller identity (env PF_USER_ID)")
	fmt.Fprintln(w, "  --profile NAME  the Cell a Cell-scoped command targets (env PF_PROFILE; else cwd match; else default)")
	fmt.Fprintln(w, "  --version      print version and exit")
}

// helpTopicExtensions is the one help topic: `pf help extensions`.
const helpTopicExtensions = "extensions"

// extensionsPointer is the hint naming where the extension points'
// shapes are documented (doctor's config failure details carry it).
const extensionsPointer = "shapes: docs/extending.md (pf help extensions)"

// extensionPoints is the index `pf help extensions` prints: one line per
// user extension point, naming the docs/extending.md subsection that
// owns it. docs/extending.md is the one authority; this index restates no shape.
var extensionPoints = []struct{ what, section string }{
	{"repos/<repo>/AGENTS.md (else CLAUDE.md)", "Per-repo rules"},
	{"docs/agents/ area notes", "Area notes"},
	{".polyforge.yaml steps.<project>.<step|default>.models, steps.<project>.<step>.skip", "Model candidates"},
	{"cli.toml [summarize] models, language, interval_sec", "Summarize models"},
	{"cli.toml [memory_policy.\"<path>\".<entry>]", "Memory policy"},
	{"cli.toml [workspace.\"<path>\"]", "Workspace mapping"},
	{"cli.toml [daemon]", "Daemon knobs"},
	{"cli.toml claude_bin", "Claude binary"},
	{"workflows dir <bundle>/workflow.yaml, steps/<id>.md", "Custom workflow bundles"},
}

// printExtensions prints the extension-point index (local text only).
func printExtensions(w io.Writer) {
	fmt.Fprintln(w, "Extension points — each documented in one docs/extending.md subsection:")
	for _, e := range extensionPoints {
		fmt.Fprintf(w, "  %-54s docs/extending.md › %s\n", e.what, e.section)
	}
	fmt.Fprintln(w, "Daily-use guide: docs/user-guide.md")
}

// printCommandUsage prints one command's usage.
func printCommandUsage(w io.Writer, name string) int {
	cmd := lookupCommand(name)
	if cmd == nil {
		fmt.Fprintf(w, "pf: unknown command %q\n", name)
		return 2
	}
	fmt.Fprintf(w, "usage: %s\n\n%s", cmd.Usage, cmd.Summary)
	fmt.Fprintln(w)
	return 0
}

// ---------------------------------------------------------------------------
// Flag value helpers.
// ---------------------------------------------------------------------------

// subFlags builds the flag set of one sub-action, wired to the CLI's
// stderr (the flag package writes its usage errors there).
func subFlags(ctx *Context, name string) *flag.FlagSet {
	f := flag.NewFlagSet("pf "+name, flag.ContinueOnError)
	f.SetOutput(ctx.Stderr)
	return f
}

// optString is a flag.Value that records whether it was explicitly set,
// distinguishing "flag absent" from "flag set to empty string" (PATCH
// semantics need both).
type optString struct {
	val string
	set bool
}

func (o *optString) String() string { return o.val }
func (o *optString) Set(s string) error {
	o.val, o.set = s, true
	return nil
}
func (o *optString) IsSet() bool { return o.set }
func (o *optString) Val() string { return o.val }
func (o *optString) Ptr() *string {
	if !o.set {
		return nil
	}
	v := o.val
	return &v
}

// flagChanged reports whether the named flag was explicitly set on the
// command line (PATCH semantics: absent must be distinguishable from a
// set-to-zero-value spelling).
func flagChanged(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// parseRepoRefs parses the -repos spelling "name=url[,name=url...]" into
// the wire's full-replacement inventory list. An EMPTY string answers an
// empty non-nil list (the explicit clear); a malformed entry is an error,
// never a silent drop.
func parseRepoRefs(s string) ([]aihubclient.RepoRef, error) {
	out := []aihubclient.RepoRef{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, url, ok := strings.Cut(pair, "=")
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if !ok || name == "" || url == "" {
			return nil, fmt.Errorf("-repos entry %q must be name=url", pair)
		}
		out = append(out, aihubclient.RepoRef{Name: name, URL: url})
	}
	return out, nil
}

// stringList collects a repeatable string flag (--label a --label b).
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }
func (l *stringList) Set(s string) error {
	*l = append(*l, s)
	return nil
}

// kvPair is one key=value flag occurrence, order-preserving.
type kvPair struct {
	Key   string
	Value string
}

// kvList collects repeatable key=value flags in declaration order.
type kvList []kvPair

func (l *kvList) String() string {
	parts := make([]string, len(*l))
	for i, kv := range *l {
		parts[i] = kv.Key + "=" + kv.Value
	}
	return strings.Join(parts, ",")
}
func (l *kvList) Set(s string) error {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return fmt.Errorf("expected key=value, got %q", s)
	}
	*l = append(*l, kvPair{Key: s[:eq], Value: s[eq+1:]})
	return nil
}

// ---------------------------------------------------------------------------
// Flag accessors for flags registered in Setup.
// ---------------------------------------------------------------------------

func boolFlag(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}
func stringFlag(fs *flag.FlagSet, name string) string {
	return fs.Lookup(name).Value.(flag.Getter).Get().(string)
}
func int64Flag(fs *flag.FlagSet, name string) int64 {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int64)
}
func intFlag(fs *flag.FlagSet, name string) int {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int)
}
func durationFlag(fs *flag.FlagSet, name string) time.Duration {
	return fs.Lookup(name).Value.(flag.Getter).Get().(time.Duration)
}

// strPtr wraps a plain string in a pointer.
func strPtr(s string) *string { return &s }

// localRepairVerbs are the local verbs an operator repairs a Cell with
// (a fixed list, no config): they never take a connection from the
// environment, so an inherited PF_SERVER/PF_API_KEY — a shell or harness
// left over from the retired overrides — must not lock the operator out of
// them. On these verbs the inherited variables are ignored with one notice
// (warnInheritedConnectionEnv); an explicit --server/--api-key is still
// refused, as on every verb.
var localRepairVerbs = []string{"config", "profile", "doctor", "init", "migrate", "version"}

// inheritedConnectionEnv names the retired connection variables set in the
// environment (names only, never values).
func inheritedConnectionEnv() []string {
	var set []string
	for _, env := range []string{"PF_SERVER", "PF_API_KEY"} {
		if os.Getenv(env) != "" {
			set = append(set, env)
		}
	}
	return set
}

// warnInheritedConnectionEnv prints the one stderr notice of a local
// repair verb run under an inherited PF_SERVER/PF_API_KEY: it names the
// variables, never a value, and changes no exit code.
func warnInheritedConnectionEnv(cmd *Command, stderr io.Writer) {
	set := inheritedConnectionEnv()
	if len(set) == 0 || !slices.Contains(localRepairVerbs, cmd.Name) {
		return
	}
	fmt.Fprintf(stderr, "pf %s: ignoring the inherited %s — a Cell's connection comes only from its config file, and this local command dials no address from the environment (unset it to silence this notice)\n",
		cmd.Name, strings.Join(set, " and "))
}

// refuseConnectionOverride refuses the retired per-command connection
// overrides: --server and --api-key on every verb, and PF_SERVER /
// PF_API_KEY on every verb but the local repair verbs (which ignore them
// with a notice). AIHub business goes through the daemon to the resolved
// Cell's configured connection, so an override can only be a mistake, and
// it is never silently ignored. Nothing is sent on a refusal.
func refuseConnectionOverride(cmd *Command, server, apiKey string, verbArgs []string) error {
	var named []string
	if server != "" {
		named = append(named, "--server")
	}
	if apiKey != "" {
		named = append(named, "--api-key")
	}
	// the same flags after the verb (`pf projects create --server …`): no
	// business verb defines them, so they can only be a connection override
	// (`pf cell add --server` is the Cell's configuration itself)
	for _, a := range verbArgs {
		if cmd.Name == "cell" {
			break
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && (name == "server" || name == "api-key") && !slices.Contains(named, "--"+name) {
			named = append(named, "--"+name)
		}
	}
	if !slices.Contains(localRepairVerbs, cmd.Name) {
		named = append(named, inheritedConnectionEnv()...)
	}
	if len(named) == 0 {
		return nil
	}
	return fmt.Errorf("%s refused: a Cell's AIHub server and api key come from its config file and every business call goes through the daemon to that Cell — configure the Cell instead (`pf [--profile NAME] config set server <url>`, `printf %%s \"$KEY\" | pf [--profile NAME] config set api_key`, or `pf cell add`), unset %s, and retry; nothing was sent",
		strings.Join(named, ", "), strings.Join(named, ", "))
}
