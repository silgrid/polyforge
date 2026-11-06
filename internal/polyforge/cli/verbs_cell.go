package cli

// verbs_cell.go: `pf cell` — the owner's Cell lifecycle on this Host. A
// Cell is one AIHub identity domain served by the one daemon (the Host);
// its file is the truth (`cli-<profile>.toml` next to cli.toml) and the
// Host applies a change without a restart.
//
//	pf cell list                     the Host's Cell registry (GET /profiles)
//	pf cell add <profile> --server URL [--model-source S] [--workspace PATH --projects a,b]
//	                                 write cli-<profile>.toml (api_key on stdin) and add the Cell
//	pf cell update|repair <profile>  re-apply the Cell's file (after a `pf config set` or a hand edit)
//	pf cell remove <profile>         remove the Cell (refused while it holds work; its state dir is kept)
//	pf cell cleanup <profile> [--confirm <profile>]
//	                                 permanently delete a removed Cell's state and Host references
//
// Reach: a human session only. Inside a step or task every `pf cell` verb
// is refused (DAEMON-403) before anything is read or sent, and the daemon's
// /profiles lifecycle routes refuse an agent caller too. Permanent cleanup
// is `pf cell cleanup` (CLI-only, explicit confirmation).

import (
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
)

var cmdCell = &Command{
	Name:         "cell",
	Group:        GroupMgmt,
	Summary:      "Cells of this Host (one per AIHub profile): list, add, update, repair, remove, cleanup",
	Usage:        "pf cell list | add <profile> --server URL [--model-source shared-default|DIR] [--workspace PATH --projects a,b] (api_key on stdin) | update <profile> | repair <profile> | remove <profile> | cleanup <profile> [--confirm <profile>]",
	NoConfigLoad: true,
	Setup:        func(fs *flag.FlagSet) {},
	Run:          runCell,
}

func runCell(ctx *Context, fs *flag.FlagSet, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("cell: expected list|add|update|repair|remove|cleanup (see `pf help cell`)")
	}
	d := NewDaemonClient(ctx.hostSocket())
	switch args[0] {
	case "list":
		return cellList(ctx, d)
	case "add":
		return cellAdd(ctx, d, args[1:])
	case "update", "repair":
		if len(args) != 2 {
			return fmt.Errorf("cell %s: expected exactly one profile", args[0])
		}
		return cellApply(ctx, d, args[1])
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("cell remove: expected exactly one profile")
		}
		return cellRemove(ctx, d, args[1])
	case "cleanup":
		return cellCleanup(ctx, d, args[1:])
	}
	return fmt.Errorf("cell: unknown action %q (list|add|update|repair|remove|cleanup)", args[0])
}

// cellList prints the Host's registry.
func cellList(ctx *Context, d *DaemonClient) error {
	raw, err := d.do("GET", "/profiles", "", nil, nil)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(p *ProfilesResponse) {
		for _, c := range p.Profiles {
			line := fmt.Sprintf("%-16s %-9s %s", c.Profile, c.State, c.Server)
			if c.Reason != "" {
				line += "  (" + c.Reason + ")"
			}
			if c.ExecutionDisabled != "" {
				line += "  [execution disabled: " + c.ExecutionDisabled + "]"
			}
			fmt.Fprintln(ctx.Stdout, strings.TrimRight(line, " "))
		}
	})
	return nil
}

// cellAdd writes a new named Cell's file and asks the Host to add it.
func cellAdd(ctx *Context, d *DaemonClient, args []string) error {
	f := subFlags(ctx, "cell add")
	server := f.String("server", "", "the Cell's AIHub base URL (one Cell per server)")
	modelSource := f.String("model-source", "", `the Cell's model-credential source: "shared-default" or an absolute directory (absent = the Cell's execution plane stays disabled)`)
	ws := f.String("workspace", "", "a workspace path the Cell maps (optional; with --projects)")
	projects := f.String("projects", "", "the comma-separated projects of --workspace")
	apiKeyFlag := f.String("api-key", "", "refused: the api key is read from stdin, never argv")
	var pos []string
	rest := args
	for {
		if err := f.Parse(rest); err != nil {
			return err
		}
		if f.NArg() == 0 {
			break
		}
		pos = append(pos, f.Arg(0))
		rest = f.Args()[1:]
	}
	if len(pos) != 1 {
		return fmt.Errorf("cell add: expected exactly one profile")
	}
	profile := pos[0]
	if err := cellcfg.ValidNamedProfile(profile); err != nil {
		return fmt.Errorf("cell add: %v", err)
	}
	if *apiKeyFlag != "" {
		return fmt.Errorf("cell add: --api-key is refused (a credential never rides argv) — pipe it on stdin: `printf %%s \"$KEY\" | pf cell add %s --server <url>`", profile)
	}
	if *server == "" {
		return fmt.Errorf("cell add: --server is required")
	}
	if _, err := cellcfg.NormalizeServer(*server); err != nil {
		return fmt.Errorf("cell add: %v", err)
	}
	if (*ws == "") != (*projects == "") {
		return fmt.Errorf("cell add: --workspace and --projects go together")
	}
	key, err := readAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("cell add: read the api key from stdin: %w", err)
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("cell add: an api key is required on stdin — nothing was written")
	}
	path := filepath.Join(filepath.Dir(DefaultConfigPath()), cellcfg.CellFileName(profile))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("cell add: %s already exists — use `pf --profile %s config set …` then `pf cell update %s`", path, profile, profile)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "server = %s\napi_key = %s\n", strconv.Quote(*server), strconv.Quote(strings.TrimSpace(key)))
	if *modelSource != "" {
		fmt.Fprintf(&b, "\n[model]\nsource = %s\n", strconv.Quote(*modelSource))
	}
	if *ws != "" {
		var quoted []string
		for _, p := range strings.Split(*projects, ",") {
			if p = strings.TrimSpace(p); p != "" {
				quoted = append(quoted, strconv.Quote(p))
			}
		}
		fmt.Fprintf(&b, "\n[workspace.%s]\nprojects = [%s]\n", strconv.Quote(*ws), strings.Join(quoted, ", "))
	}
	if _, err := ParseConfigBytes(path, []byte(b.String())); err != nil {
		return fmt.Errorf("cell add: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeConfigAtomic(path, []byte(b.String())); err != nil {
		return fmt.Errorf("cell add: write %s: %w", path, err)
	}
	fmt.Fprintf(ctx.Stderr, "wrote %s\n", path)
	return cellApply(ctx, d, profile)
}

// cellApply asks the Host to (re)apply one profile's file.
func cellApply(ctx *Context, d *DaemonClient, profile string) error {
	raw, err := d.do("POST", "/profiles", "", map[string]string{"profile": profile}, nil)
	if err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) {
			fmt.Fprintf(ctx.Stdout, "Cell %s: saved; no daemon answers — the Host applies it at its next start\n", profile)
			return nil
		}
		return err
	}
	ctx.finishLine(raw, "Cell %s: applied (no Host restart)\n", profile)
	return nil
}

// cellRemove removes a Cell at the Host (refused while it holds work), then
// its file; the Cell's state dir stays on disk.
func cellRemove(ctx *Context, d *DaemonClient, profile string) error {
	if profile == cellcfg.DefaultProfile {
		return fmt.Errorf("cell remove: the default Cell cannot be removed")
	}
	raw, err := d.do("DELETE", "/profiles/"+profile, "", nil, nil)
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(DefaultConfigPath()), cellcfg.CellFileName(profile))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cell remove: the Cell stopped but its file %s could not be removed: %w", path, err)
	}
	ctx.finishLine(raw, "Cell %s removed (its file %s is gone; its state dir is kept — `pf cell cleanup %s` deletes it for good)\n", profile, path, profile)
	return nil
}

// cellCleanup is the permanent cleanup of a removed Cell: without
// --confirm <profile> it prints the impact (state dir, subscriptions with
// their cursors, retained delivery records) and exits 1 having changed
// nothing; with it the Host drops every reference and the state dir.
func cellCleanup(ctx *Context, d *DaemonClient, args []string) error {
	var profile, confirm string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--confirm" && i+1 < len(args):
			confirm = args[i+1]
			i++
		case strings.HasPrefix(a, "--confirm="):
			confirm = strings.TrimPrefix(a, "--confirm=")
		case !strings.HasPrefix(a, "-") && profile == "":
			profile = a
		default:
			return fmt.Errorf("cell cleanup: unexpected argument %q (pf cell cleanup <profile> [--confirm <profile>])", a)
		}
	}
	if profile == "" {
		return fmt.Errorf("cell cleanup: expected a profile")
	}
	if profile == cellcfg.DefaultProfile {
		return fmt.Errorf("cell cleanup: the default Cell cannot be cleaned up")
	}
	raw, err := d.do("POST", "/profiles/"+profile+"/cleanup", "", map[string]string{"confirm": confirm}, nil)
	if err != nil {
		return err
	}
	var im struct {
		StateDir       string `json:"state_dir"`
		StateDirExists bool   `json:"state_dir_exists"`
		Decisions      int    `json:"decisions"`
		Applied        bool   `json:"applied"`
	}
	_ = json.Unmarshal(raw, &im)
	state := "absent"
	if im.StateDirExists {
		state = "present"
	}
	if !im.Applied {
		ctx.finishLine(raw, "Cell %s cleanup would permanently delete:\n  state dir %s (%s)\n  %d decision feed entr(y/ies)\n",
			profile, im.StateDir, state, im.Decisions)
		return fmt.Errorf("cell cleanup: rerun with --confirm %s to delete them; nothing was changed", profile)
	}
	ctx.finishLine(raw, "Cell %s cleaned up for good: state dir %s, %d decision feed entr(y/ies) deleted\n", profile, im.StateDir, im.Decisions)
	return nil
}
