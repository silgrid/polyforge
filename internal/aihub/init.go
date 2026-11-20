package aihub

// init.go — polyforge#176 (84-components-C): the local first-boot
// initialization entry, `pf aihub init`. This is the ticket's settled
// Q1 shape: a subcommand of `pf aihub` that opens the core database
// IN PROCESS (no network listener, no HTTP route), so the workstation's
// first boot can establish the daily identity before `pf supervise`
// starts the server — the server's own fresh-install bootstrap
// (bootstrapAdmin, usr_root) never runs on a first-boot-initialized
// store, because the users table is no longer empty when it starts.
//
// Reach: a workstation-boot process only. The entry refuses to run
// inside a step or task environment (PF_WI_ID / PF_TASK_ID are the
// daemon's spawn markers — the same read `pf daemon ensure` uses), it
// shares the server's single-active lock (service.lock), so it never
// runs beside a live server, and the issued key's plaintext never
// reaches stdout, the log or argv — only the 0600 file --key-out names.

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// The workstation's fixed first-boot business constants (Q3/Q2): the
// daily user, the local project, and the key's fixed name a retry
// revokes and reissues.
const (
	// InitUserEmail is the daily user's fixed identity.
	InitUserEmail = "workstation@polyforge.local"
	// InitUserDisplayName is the daily user's display name.
	InitUserDisplayName = "workstation"
	// InitProjectName is the local project's name.
	InitProjectName = "workstation"
	// InitKeyName is the daily key's fixed name.
	InitKeyName = "workstation-first-boot"
)

// InitOptions is the local init entry's input.
type InitOptions struct {
	// DataDir is the aihub data directory (the AIHub volume in the
	// workstation).
	DataDir string
}

// InitLocal runs the local first-boot initialization: it takes the
// single-active lock (the server's own service.lock), opens the core
// in process and runs the combined identity transaction
// (core.WorkstationInit). The result's PlainKey exists exactly once,
// in the returned value — the caller folds it into the machine's
// config; the standalone subcommand lands it in the --key-out file.
//
// The entry refuses inside a step or task environment (the daemon's
// spawn markers — a step agent or task process never reaches the hub's
// identity tables through this door), and beside a running server (the
// lock is held). On a store the identity already exists in, the
// transaction only verifies it: the user is found by its fixed email
// and never duplicated, and the key is reissued under its fixed name
// (the retry discipline, Q2 — a completed first boot never calls here
// again; the caller's done-phase gate decides that).
func InitLocal(opts InitOptions) (*core.WorkstationInitResult, error) {
	for _, marker := range []string{"PF_WI_ID", "PF_TASK_ID"} {
		if os.Getenv(marker) != "" {
			return nil, fmt.Errorf("aihub init: refused inside a %s environment: the local init entry is the workstation boot's own, a step or task process never reaches the hub's identity tables through it", marker)
		}
	}
	release, err := lockDataDir(opts.DataDir)
	if err != nil {
		return nil, err
	}
	defer release()
	db, err := core.Open(opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("aihub init: open core: %w", err)
	}
	defer db.Close()
	res, err := core.NewAdminManager(db).WorkstationInit(core.WorkstationInitRequest{
		Email:       InitUserEmail,
		DisplayName: InitUserDisplayName,
		ProjectName: InitProjectName,
		KeyName:     InitKeyName,
	})
	if err != nil {
		return nil, fmt.Errorf("aihub init: %w", err)
	}
	return res, nil
}

// initMain executes `pf aihub init <flags>` and answers the exit code.
// The key file is written AFTER the transaction commits, through the
// same writeKeyFile the bootstrap uses (0600, a leftover file from an
// earlier run is replaced, never reused); a crash in between loses the
// plaintext — the retry revokes the same-named key and reissues (Q2),
// so nothing is stranded.
func initMain(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pf aihub init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "/var/lib/polyforge/aihub", "AIHub data directory")
	keyOut := fs.String("key-out", "", "the 0600 file the issued key's plaintext lands in (required; the key is never printed)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "pf aihub init: unexpected arguments %v\n", fs.Args())
		return 2
	}
	if *keyOut == "" {
		fmt.Fprintln(stderr, "pf aihub init: --key-out is required — the issued key's plaintext goes to a 0600 file, never to stdout")
		return 2
	}
	res, err := InitLocal(InitOptions{DataDir: *dataDir})
	if err != nil {
		fmt.Fprintf(stderr, "pf aihub init: %v\n", err)
		return 1
	}
	if err := writeKeyFile(*keyOut, res.PlainKey); err != nil {
		fmt.Fprintf(stderr, "pf aihub init: write %s: %v\n", *keyOut, err)
		return 1
	}
	if res.UserCreated {
		fmt.Fprintf(stdout, "aihub init: created the workstation user %s and the project %s; the key is in %s (0600)\n", res.UserID, InitProjectName, *keyOut)
	} else {
		fmt.Fprintf(stdout, "aihub init: the workstation user %s was already there (verified, not duplicated); a fresh key is in %s (0600)\n", res.UserID, *keyOut)
	}
	return 0
}
