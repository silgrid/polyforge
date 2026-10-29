package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// Management face M1–M5 (same port, same auth, admin unless noted).
// Semantics ported from the v0 CLI: users, api-keys, projects
// (incl. membership), backup.

// ---------------------------------------------------------------------------
// users (M1)
// ---------------------------------------------------------------------------

var cmdUsers = &Command{
	Name:    "users",
	Group:   GroupMgmt,
	Summary: "User management (M1, admin only)",
	Usage: "pf users create --display-name N [--user-type human|machine] [--role writer|admin]\n" +
		"pf users list\n" +
		"pf users get <id>\n" +
		"pf users patch <id> [--display-name N] [--role writer|admin]",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("users: expected create|list|get|patch")
		}
		client := ctx.aihub()
		sub, rest := args[0], args[1:]
		switch sub {
		case "create":
			f := subFlags(ctx, "users create")
			name := f.String("display-name", "", "human-readable display name (required)")
			kind := f.String("user-type", "human", "human|machine")
			role := f.String("role", "writer", "writer|admin (global role)")
			rest, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(rest) != 0 || *name == "" {
				return fmt.Errorf("users create: --display-name is required")
			}
			raw, err := client.CreateUser(aihubclient.UserCreateRequest{
				DisplayName: *name,
				UserType:    *kind,
				Role:        *role,
			})
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(u *api.User) {
				fmt.Fprintf(ctx.Stdout, "created user %s (%s, %s)\n", u.ID, u.UserType, u.Role)
			})
			return nil
		case "list":
			raw, err := client.ListUsers()
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(l *itemList[api.User]) {
				renderUsers(ctx.Stdout, l.Items)
			})
			return nil
		case "get":
			if len(rest) != 1 {
				return fmt.Errorf("users get: expected exactly one user id")
			}
			raw, err := client.GetUser(rest[0])
			if err != nil {
				return err
			}
			ctx.finish(raw, func() { printJSON(ctx.Stdout, raw) })
			return nil
		case "patch":
			f := subFlags(ctx, "users patch")
			name := f.String("display-name", "", "new display name")
			role := f.String("role", "", "writer|admin")
			ids, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(ids) != 1 || (*name == "" && *role == "") {
				return fmt.Errorf("users patch: expected <id> and at least one of --display-name / --role")
			}
			raw, err := client.PatchUser(ids[0], aihubclient.UserPatchRequest{
				DisplayName: *name,
				Role:        *role,
			})
			if err != nil {
				return err
			}
			ctx.finishLine(raw, "user %s updated\n", ids[0])
			return nil
		default:
			return fmt.Errorf("users: unknown action %q", sub)
		}
	},
}

// ---------------------------------------------------------------------------
// api-keys (M2)
// ---------------------------------------------------------------------------

var cmdApiKeys = &Command{
	Name:    "api-keys",
	Group:   GroupMgmt,
	Summary: "API key management (M2, admin only; plaintext shown exactly once)",
	Usage: "pf api-keys create --user U [--name N] [--project-scope P]\n" +
		"pf api-keys list\n" +
		"pf api-keys revoke <id>",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("api-keys: expected create|list|revoke")
		}
		client := ctx.aihub()
		sub, rest := args[0], args[1:]
		switch sub {
		case "create":
			f := subFlags(ctx, "api-keys create")
			user := f.String("user", "", "user id to mint the key for (required)")
			scope := f.String("project-scope", "", "optional project scope (narrows, never grants)")
			name := f.String("name", "", "optional key name")
			rest, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(rest) != 0 || *user == "" {
				return fmt.Errorf("api-keys create: --user is required")
			}
			req := aihubclient.ApiKeyCreateRequest{UserID: *user, Name: *name}
			if *scope != "" {
				req.ProjectID = strPtr(*scope)
			}
			raw, err := client.CreateApiKey(req)
			if err != nil {
				return err
			}
			ctx.finish(raw, func() {
				printJSON(ctx.Stdout, raw)
				fmt.Fprintln(ctx.Stdout, "NOTE: the key plaintext is shown exactly once and never stored.")
			})
			return nil
		case "list":
			raw, err := client.ListApiKeys()
			if err != nil {
				return err
			}
			ctx.finish(raw, func() {
				renderApiKeys(ctx.Stdout, raw)
			})
			return nil
		case "revoke":
			if len(rest) != 1 {
				return fmt.Errorf("api-keys revoke: expected exactly one key id")
			}
			raw, err := client.RevokeApiKey(rest[0])
			if err != nil {
				return err
			}
			ctx.finishLine(raw, "key %s revoked\n", rest[0])
			return nil
		default:
			return fmt.Errorf("api-keys: unknown action %q", sub)
		}
	},
}

// ---------------------------------------------------------------------------
// projects (M3), including membership add/remove
// ---------------------------------------------------------------------------

var cmdProjects = &Command{
	Name:    "projects",
	Group:   GroupMgmt,
	Summary: "Project and membership management (M3), plus the hard delete",
	Usage: "pf projects create --name N\n" +
		"pf projects list\n" +
		"pf projects get <id-or-name>\n" +
		"pf projects patch <id-or-name> [--repos name=url[,name=url...]]\n" +
		"pf projects members add <id-or-name> --user U --role viewer|writer|maintainer\n" +
		"pf projects members remove <id-or-name> --user U [--user U ...]\n" +
		"pf projects delete <id-or-name> [--yes]\n" +
		"\n" +
		"delete is IRREVERSIBLE and takes no automatic backup: run `pf backup` first\n" +
		"(it does not include the git repos — copy the server's git/ directory too).\n" +
		"Every work item must be terminal and no unrevoked api key may be scoped\n" +
		"to the project. --yes skips the typed-name confirmation; a terminal\n" +
		"prompts for the project name, anything else cancels.\n",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("projects: expected create|list|get|patch|members|delete")
		}
		client := ctx.aihub()
		sub, rest := args[0], args[1:]
		switch sub {
		case "create":
			f := subFlags(ctx, "projects create")
			name := f.String("name", "", "project name (required)")
			rest, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(rest) != 0 || *name == "" {
				return fmt.Errorf("projects create: --name is required")
			}
			raw, err := client.CreateProject(aihubclient.ProjectCreateRequest{Name: *name})
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(p *api.Project) {
				fmt.Fprintf(ctx.Stdout, "created project %s (%s)\n", p.Name, p.ID)
			})
			return nil
		case "list":
			raw, err := client.ListProjects()
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(l *itemList[api.Project]) {
				renderProjects(ctx.Stdout, l.Items)
			})
			return nil
		case "get":
			if len(rest) != 1 {
				return fmt.Errorf("projects get: expected exactly one project id or name")
			}
			raw, err := client.GetProject(rest[0])
			if err != nil {
				return err
			}
			ctx.finish(raw, func() { printJSON(ctx.Stdout, raw) })
			return nil
		case "patch":
			f := subFlags(ctx, "projects patch")
			// -repos is the task-repo inventory as an EXPLICIT FULL
			// REPLACEMENT (the wire face's semantics): omit to leave
			// unchanged, -repos "" clears, and every spelling is
			// name=url pairs, comma-separated.
			repos := f.String("repos", "", "task-repo inventory (explicit full replacement): name=url[,name=url...]")
			ids, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(ids) != 1 {
				return fmt.Errorf("projects patch: expected one project id or name")
			}
			req := aihubclient.ProjectPatchRequest{}
			if flagChanged(f, "repos") {
				refs, perr := parseRepoRefs(*repos)
				if perr != nil {
					return fmt.Errorf("projects patch: %v", perr)
				}
				req.Repos = &refs
			}
			raw, err := client.PatchProject(ids[0], req)
			if err != nil {
				return err
			}
			ctx.finishLine(raw, "project %s updated\n", ids[0])
			return nil
		case "members":
			return projectsMembers(ctx, client, rest)
		case "delete":
			f := subFlags(ctx, "projects delete")
			yes := f.Bool("yes", false, "delete without the typed-name confirmation")
			ids, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(ids) != 1 {
				return fmt.Errorf("projects delete: expected exactly one project id or name")
			}
			return projectsDelete(ctx, client, ids[0], *yes)
		default:
			return fmt.Errorf("projects: unknown action %q", sub)
		}
	},
}

// stdinIsTerminal answers whether r is an interactive terminal (a
// tty) — NOT merely a char device: /dev/null is one of those and is,
// by spec, the outright refusal. A package var so tests can drive both
// faces (no pty harness exists in a test run; a piped stdin is, by
// spec, the refusal too).
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return termiosOK(f)
}

// projectsDelete runs the project hard delete (DELETE
// /api/v1/projects/{id}, its CLI face). --yes
// deletes without prompting; a terminal first prints the dry-run
// preview (project name, work-item count — the server's dry run IS
// the terminal check) and requires typing the project name, and a
// wrong name cancels with no write. Without --yes on a non-TTY stdin
// it refuses naming the escape hatch and performs no request at all.
// The preview and the prompt go to stderr, so stdout carries only the
// result (the raw body under --json).
func projectsDelete(ctx *Context, client *aihubclient.Client, idOrName string, yes bool) error {
	if !yes && !stdinIsTerminal(ctx.Stdin) {
		return fmt.Errorf("projects delete: stdin is not a terminal and --yes was not given (the delete is irreversible; re-run with --yes, after `pf backup` — the backup does not include the git repos)")
	}
	if !yes {
		// The dry run: the server's checks answer the preview, and a
		// typed refusal renders with no prompt and no write.
		raw, err := client.DeleteProject(idOrName, true)
		if err != nil {
			return err
		}
		var res api.ProjectDeleteResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stderr, "project %s (%s): %d work item(s), terminal check: passed\n",
			res.Name, res.ID, res.WorkItems)
		fmt.Fprintf(ctx.Stderr, "the delete is irreversible and takes no automatic backup: run `pf backup` first (it does not include the git repos)\n")
		fmt.Fprintf(ctx.Stderr, "type the project name to confirm: ")
		line, rerr := bufio.NewReader(ctx.Stdin).ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("projects delete: %v", rerr)
		}
		if answer := strings.TrimSpace(line); answer != res.Name {
			return fmt.Errorf("projects delete: canceled (%q does not name the project %q)", answer, res.Name)
		}
	}
	raw, err := client.DeleteProject(idOrName, false)
	if err != nil {
		return err
	}
	ctx.finish(raw, func() {
		var res api.ProjectDeleteResult
		if json.Unmarshal(raw, &res) == nil {
			fmt.Fprintf(ctx.Stdout, "deleted project %s (%s), %d work item(s)\n", res.Name, res.ID, res.WorkItems)
		}
	})
	return nil
}

// projectsMembers handles `pf projects members add|remove ...` — both map
// onto the M3 project PATCH (members upsert / remove_members).
func projectsMembers(ctx *Context, client *aihubclient.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("projects members: expected add|remove")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		f := subFlags(ctx, "projects members add")
		user := f.String("user", "", "user id (required)")
		role := f.String("role", "writer", "viewer|writer|maintainer")
		ids, err := parseCommandArgs(f, rest)
		if err != nil {
			return err
		}
		if len(ids) != 1 || *user == "" {
			return fmt.Errorf("projects members add: expected <id-or-name> --user U --role R")
		}
		switch *role {
		case "viewer", "writer", "maintainer":
		default:
			return fmt.Errorf("projects members add: --role must be viewer|writer|maintainer")
		}
		members := []aihubclient.ProjectMember{{UserID: *user, Role: *role}}
		raw, err := client.PatchProject(ids[0], aihubclient.ProjectPatchRequest{Members: &members})
		if err != nil {
			return err
		}
		ctx.finishLine(raw, "member %s upserted on %s as %s\n", *user, ids[0], *role)
		return nil
	case "remove", "rm":
		f := subFlags(ctx, "projects members remove")
		var users stringList
		f.Var(&users, "user", "user id to remove (repeatable)")
		ids, err := parseCommandArgs(f, rest)
		if err != nil {
			return err
		}
		if len(ids) != 1 || len(users) == 0 {
			return fmt.Errorf("projects members remove: expected <id-or-name> --user U [--user U ...]")
		}
		rem := []string(users)
		raw, err := client.PatchProject(ids[0], aihubclient.ProjectPatchRequest{RemoveMembers: &rem})
		if err != nil {
			return err
		}
		ctx.finishLine(raw, "removed from %s: %s\n", ids[0], strings.Join(rem, ","))
		return nil
	default:
		return fmt.Errorf("projects members: unknown action %q", sub)
	}
}

// ---------------------------------------------------------------------------
// backup (M4)
// ---------------------------------------------------------------------------

var cmdBackup = &Command{
	Name:    "backup",
	Group:   GroupMgmt,
	Summary: "Write a database snapshot on the server (M4, admin only; every memory, no git repos)",
	Usage: "pf backup\n" +
		"  Writes a consistent snapshot of the database (VACUUM INTO) under the server's data dir backups/, on the same disk.\n" +
		"  Memories live in the database, so it holds every memory; it does NOT include the git repos (git/).\n" +
		"  For a full backup use the backup steps of .polyforge/deploy.sh, or copy the whole data dir.",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		raw, err := ctx.aihub().Backup()
		if err != nil {
			return err
		}
		finishOrRaw(ctx, raw, func(resp *struct {
			Backup string `json:"backup"`
		}) bool {
			fmt.Fprintf(ctx.Stdout, "snapshot written: %s\n", resp.Backup)
			return true
		})
		return nil
	},
}

// ---------------------------------------------------------------------------
// machines (M3 heartbeat wiring) — the machines liveness read
// face; the operator half of GET /api/v1/machines (admin).

var cmdMachines = &Command{
	Name:    "machines",
	Group:   GroupMgmt,
	Summary: "Machine identity/liveness face (M3, admin only; rekey is admin-only)",
	Usage: "pf machines list\n" +
		"pf machines rekey <machine> [--public-key <base64> | --key-file <path>]\n\n" +
		"Requires an admin credential. rekey without a key source reads the local\n" +
		"<data-dir>/machine-key seed and rebinds its public half.",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("machines: expected list|rekey")
		}
		switch args[0] {
		case "list":
			if len(args) != 1 {
				return fmt.Errorf("machines list: no arguments")
			}
			raw, err := ctx.aihub().ListMachinesRaw()
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(out *aihubclient.MachinesList) {
				renderMachines(ctx.Stdout, out)
			})
			return nil
		case "rekey":
			f := subFlags(ctx, "machines rekey")
			pub := f.String("public-key", "", "base64 Ed25519 public key")
			keyFile := f.String("key-file", "", "file carrying the hex seed or a base64 key")
			rest, err := parseCommandArgs(f, args[1:])
			if err != nil {
				return err
			}
			if len(rest) != 1 {
				return fmt.Errorf("machines rekey: expected exactly one machine id")
			}
			if *pub != "" && *keyFile != "" {
				return fmt.Errorf("machines rekey: --public-key and --key-file are exclusive")
			}
			key := *pub
			source := "--public-key"
			if key == "" {
				path := *keyFile
				source = path
				if path == "" {
					home, _ := os.UserHomeDir()
					path = filepath.Join(DataDir(home), "machine-key")
				}
				key, err = publicKeyFromFile(path)
				if err != nil {
					return fmt.Errorf("machines rekey: %v", err)
				}
			}
			raw, err := ctx.aihub().RekeyMachine(rest[0], key)
			if err != nil {
				return err
			}
			ctx.finishLine(raw, "machine %s rebound to the public key from %s\n", rest[0], source)
			return nil
		default:
			return fmt.Errorf("machines: unknown action %q", args[0])
		}
	},
}

// publicKeyFromFile answers the base64 public half of a machine key file
// (the daemon's 32-byte hex seed, or a base64 raw seed/public/private key).
func publicKeyFromFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	text := strings.TrimSpace(string(raw))
	if seed, herr := hex.DecodeString(text); herr == nil && len(seed) == ed25519.SeedSize {
		return base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)), nil
	}
	if b, berr := base64.StdEncoding.DecodeString(text); berr == nil {
		switch len(b) {
		case ed25519.SeedSize: // a raw seed and a raw public key are both 32 bytes
			return base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(b).Public().(ed25519.PublicKey)), nil
		case ed25519.PrivateKeySize:
			return base64.StdEncoding.EncodeToString(ed25519.PrivateKey(b).Public().(ed25519.PublicKey)), nil
		}
	}
	return "", fmt.Errorf("%s carries neither a 32-byte hex seed nor a base64 key", path)
}
