package cli

// verbs_migrate.go: `pf migrate cell-layout` — the one-time, explicit
// migration of a STOPPED Host's data dir to the per-Cell layout
// (cellcfg/migrate.go). Local and owner-only: it refuses inside a step or
// task (management group); a mutating run takes the data dir's Host lock
// (daemon.lock) before its first check and holds it to its last byte, so
// it refuses while a Host runs and a Host start is refused while it runs.
//
//	pf migrate cell-layout              plan: print the manifest, write nothing
//	pf migrate cell-layout --apply      copy, publish, write layout-version last
//	pf migrate cell-layout --rollback   return to the pre-migration copy (only before
//	                                    a Host served the new layout)
//	pf migrate cell-layout --restore-backup DIR [--confirm]
//	                                    the last resort after the window: list the
//	                                    data-loss scope; --confirm replaces the state
//	                                    by the backup (the current items kept aside)

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

var cmdMigrate = &Command{
	Name:         "migrate",
	Group:        GroupMgmt,
	Summary:      "one-time data-dir migrations of a stopped Host: cell-layout (plan, --apply, --rollback)",
	Usage:        "pf migrate cell-layout [--apply | --rollback | --restore-backup DIR [--confirm]] [--data-dir DIR]",
	NoConfigLoad: true,
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("apply", false, "carry the plan out")
		fs.Bool("rollback", false, "return to the pre-migration copy (refused once a Host served the new layout)")
		fs.String("restore-backup", "", "replace the data dir's state by this backup directory (lists the data-loss scope; acts only with --confirm)")
		fs.Bool("confirm", false, "confirm --restore-backup after reading its data-loss scope")
		fs.String("data-dir", "", "the Host data dir (default: POLYFORGE_DATA_DIR, else ~/.local/share/polyforge)")
	},
	Run: runMigrate,
}

func runMigrate(ctx *Context, fs *flag.FlagSet, args []string) error {
	if len(args) != 1 || args[0] != "cell-layout" {
		return fmt.Errorf("migrate: expected `pf migrate cell-layout [--apply|--rollback]`")
	}
	apply := fs.Lookup("apply").Value.String() == "true"
	rollback := fs.Lookup("rollback").Value.String() == "true"
	backup := fs.Lookup("restore-backup").Value.String()
	confirm := fs.Lookup("confirm").Value.String() == "true"
	modes := 0
	for _, on := range []bool{apply, rollback, backup != ""} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		return fmt.Errorf("migrate cell-layout: --apply, --rollback and --restore-backup exclude each other")
	}
	if confirm && backup == "" {
		return fmt.Errorf("migrate cell-layout: --confirm belongs to --restore-backup")
	}
	dataDir := fs.Lookup("data-dir").Value.String()
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dataDir = DataDir(home)
	}
	if apply || rollback || (backup != "" && confirm) {
		lock, err := cellcfg.HoldHostLock(dataDir)
		if errors.Is(err, cellcfg.ErrHostLockHeld) {
			return fmt.Errorf("migrate cell-layout: a daemon holds %s — stop it first (`pf ops shutdown`); nothing was changed", filepath.Join(dataDir, cellcfg.HostLockFile))
		}
		if err != nil {
			return fmt.Errorf("migrate cell-layout: %v; nothing was changed", err)
		}
		defer lock.Close() // held through the run's last byte
		if c, err := net.DialTimeout("unix", ctx.hostSocket(), time.Second); err == nil {
			_ = c.Close()
			return fmt.Errorf("migrate cell-layout: a daemon answers on %s — stop it first (`pf ops shutdown`); nothing was changed", ctx.hostSocket())
		}
	}
	if backup != "" {
		scope, err := cellcfg.RestoreScope(dataDir, backup)
		if err != nil {
			return err
		}
		if !confirm {
			fmt.Fprintf(ctx.Stdout, "restoring %s over %s replaces these items (every work record, decision and identity written since the backup leaves the live data dir; they are moved aside under migration/, not deleted):\n", backup, dataDir)
			for _, name := range scope {
				fmt.Fprintf(ctx.Stdout, "  %s\n", name)
			}
			return fmt.Errorf("migrate cell-layout: nothing was changed — rerun with --confirm to restore")
		}
		aside, err := cellcfg.RestoreBackup(dataDir, backup)
		if err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "restored %s from %s; the replaced items are under %s\n", dataDir, backup, aside)
		return nil
	}
	if rollback {
		if err := cellcfg.RollbackMigration(dataDir); err != nil {
			return err
		}
		fmt.Fprintf(ctx.Stdout, "rolled back %s to its pre-migration copy (layout-version removed); the new runtime refuses it until a new --apply\n", dataDir)
		return nil
	}
	var (
		p   *cellcfg.MigrationPlan
		err error
	)
	if apply {
		p, err = cellcfg.ApplyMigration(dataDir)
	} else {
		p, err = cellcfg.PlanMigration(dataDir)
	}
	if p != nil {
		if ctx.JSON {
			raw, _ := json.MarshalIndent(p, "", "  ")
			fmt.Fprintln(ctx.Stdout, string(raw))
		} else {
			renderMigrationPlan(ctx, p, apply)
		}
	}
	if err != nil {
		return err
	}
	if !apply && len(p.Problems()) > 0 {
		return fmt.Errorf("migrate cell-layout: the plan has failed checks (listed above); --apply would change nothing")
	}
	return nil
}

func renderMigrationPlan(ctx *Context, p *cellcfg.MigrationPlan, applied bool) {
	if !p.Pending() {
		if v, _ := cellcfg.ReadLayoutVersion(p.DataDir); v == cellcfg.CurrentLayout {
			fmt.Fprintf(ctx.Stdout, "%s: already on the cell layout (%s %s) — nothing to do; no byte was changed\n", p.DataDir, cellcfg.LayoutVersionFile, v)
			return
		}
		fmt.Fprintf(ctx.Stdout, "%s: nothing to move — the data dir is on the per-Cell layout (cells/default)\n", p.DataDir)
		return
	}
	verb := "would"
	if applied {
		verb = "did"
	}
	fmt.Fprintf(ctx.Stdout, "cell-layout migration of %s (%s act):\n", p.DataDir, verb)
	for _, it := range p.Items {
		line := fmt.Sprintf("  %-9s %s -> %s", it.Action, it.Item, it.Dest)
		if it.Size > 0 {
			line += fmt.Sprintf(" (%d bytes)", it.Size)
		}
		if it.Problem != "" {
			line += "  FAILED: " + it.Problem
		}
		fmt.Fprintln(ctx.Stdout, line)
	}
	if !applied {
		fmt.Fprintln(ctx.Stdout, "nothing was changed; stop the daemon and run `pf migrate cell-layout --apply`")
	}
}
