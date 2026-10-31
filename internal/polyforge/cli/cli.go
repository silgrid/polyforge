package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
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
}

// aihub returns the direct-aihub client for the resolved config.
func (ctx *Context) aihub() *aihubclient.Client {
	return aihubclient.New(ctx.Cfg.Server, ctx.Cfg.APIKey)
}

// daemon returns the daemon-socket client for the resolved config.
func (ctx *Context) daemon() *DaemonClient {
	d := NewDaemonClient(ctx.Cfg.DaemonSocket)
	if ctx.Cfg.ChatID != "" || ctx.Cfg.UserID != "" {
		d.Origin = &SubmitOrigin{ChatID: ctx.Cfg.ChatID, Owner: ctx.Cfg.UserID}
	}
	return d
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
// after watch, followed by `verify-run` (a verification step's one
// mechanical run, local: no daemon, no aihub; also GroupExec, so the help
// face shows 11 exec verbs), then the management face M1–M5, then the
// machine health face (doctor, #113), the build version (version, #196:
// the verb form of the `--version` flag) and the workspace layout (init,
// #177), then the daemon
// placeholder (help-only: cmd/pf/main.go intercepts the real launch).
var commands = []*Command{
	cmdFile,
	cmdQueue,
	cmdCancel,
	cmdRecall,
	cmdRemember,
	cmdMemory,
	cmdSubmit,
	cmdAnswer,
	cmdReviseBinding,
	cmdPause,
	cmdResume,
	cmdWrap,
	cmdOps,
	cmdChat,
	cmdWatch,
	cmdTask,
	cmdResult,
	cmdVerifyRun,
	cmdMsg,
	cmdUsers,
	cmdApiKeys,
	cmdMachines,
	cmdProjects,
	cmdBackup,
	cmdDoctor,
	cmdVersion,
	cmdInit,
	cmdConfig,
	cmdDaemon,
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
	serverFlag := gfs.String("server", "", "aihub base URL (env PF_SERVER)")
	apiKeyFlag := gfs.String("api-key", "", "aihub api key (env PF_API_KEY)")
	socketFlag := gfs.String("socket", "", "daemon unix socket (env PF_DAEMON_SOCKET)")
	userFlag := gfs.String("user", "", "caller user id, e.g. for answer.answered_by (env PF_USER_ID)")
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
			fmt.Fprintf(stdout, "{\"version\":%q}\n", version.Version)
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
	if *serverFlag != "" {
		cfg.Server = *serverFlag
	}
	if *apiKeyFlag != "" {
		cfg.APIKey = *apiKeyFlag
	}
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
	}
	if wi := stepWorkItem(); wi != "" {
		if err := stepRefusal(cmd); err != nil {
			renderError(ctx, err)
			return exitCode(err)
		}
		// no key, and an unresolvable server: nothing reaches aihub directly
		ctx.Step, ctx.Cfg.APIKey, ctx.Cfg.Server = wi, "", "http://step-mode.invalid"
	}

	if taskID != "" && ctx.Step == "" {
		// no key, and an unresolvable server: nothing reaches aihub directly
		ctx.Task, ctx.Cfg.APIKey, ctx.Cfg.Server = taskID, "", "http://task-mode.invalid"
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
	{GroupData, "Data-plane verbs (direct to aihub):"},
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
	fmt.Fprintln(w, "  --server URL    aihub base URL (env PF_SERVER)")
	fmt.Fprintln(w, "  --api-key KEY   aihub api key (env PF_API_KEY)")
	fmt.Fprintln(w, "  --socket PATH   daemon unix socket (env PF_DAEMON_SOCKET)")
	fmt.Fprintln(w, "  --user ID       caller identity (env PF_USER_ID)")
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
	{".polyforge.yaml steps.<project>.<step|default>.models", "Model candidates"},
	{".polyforge.yaml summarize.models, summarize.language, summarize.interval_sec", "Summarize models"},
	{".polyforge.yaml memory_policy", "Memory policy"},
	{"cli.toml [workspace.\"<path>\"]", "Workspace mapping"},
	{"cli.toml [daemon]", "Daemon knobs"},
	{"cli.toml claude_bin", "Claude binary"},
	{"workflows dir <bundle>/workflow.md, steps/<id>.md", "Custom workflow bundles"},
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
