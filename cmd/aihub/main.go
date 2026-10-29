// Command aihub is the v1 server binary: single-active, SQLite-backed,
// one HTTP port for the business face, the admin face and the git proxy.
//
// Architecture: docs/design/polyforge-vnext/reference.md §4.
// Modules: internal/aihub/{core,memory,git,server}.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/aihub/memory"
	"github.com/silgrid/polyforge/internal/aihub/server"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/version"
)

// lockDataDir takes the exclusive single-active lock. Uses an open file
// descriptor held for the process lifetime (flock), matching the v0
// service.lock discipline; a second aihub fails immediately.
func lockDataDir(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dataDir+"/service.lock", os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, core.Fail(api.ErrCoreLockHeld, "LOCK_HELD", "another aihub process holds %s/service.lock", dataDir)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// aihubFlags carries the command-line configuration registerFlags fills.
type aihubFlags struct {
	dataDir     string
	listen      string
	janitorFreq time.Duration
	// Machines liveness threshold for GET /api/v1/machines: 5× the
	// daemon heartbeat default (60s). Tune to ≥5× the actual
	// -heartbeat-every the fleet runs with.
	staleAfter time.Duration
	// --version / -version: print the build-injected version string
	// (internal/version.Version, the git tag at build; "dev" un-injected)
	// and exit 0 — before any data-dir lock or server start.
	showVersion bool
}

// registerFlags registers the aihub command-line flags on fs. These are
// the ONLY knobs the binary has: the HTTP pressure bounds (header
// deadline, idle timeout, header size — internal/aihub/server) are
// constants and are deliberately NOT registered here.
func registerFlags(fs *flag.FlagSet) *aihubFlags {
	f := &aihubFlags{
		dataDir:     "/var/lib/polyforge/aihub",
		listen:      ":8420",
		janitorFreq: time.Hour,
		staleAfter:  5 * time.Minute,
	}
	fs.StringVar(&f.dataDir, "data-dir", f.dataDir, "AIHub data directory")
	fs.StringVar(&f.listen, "listen", ":8420", "HTTP listen address")
	fs.DurationVar(&f.janitorFreq, "git-janitor-period", f.janitorFreq, "bare-repo gc/repack period")
	fs.DurationVar(&f.staleAfter, "machines-stale-after", f.staleAfter, "machines liveness threshold for the stale projection (default 5m = 5× heartbeat default)")
	fs.BoolVar(&f.showVersion, "version", false, "print version and exit")
	return f
}

func main() {
	fl := registerFlags(flag.CommandLine)
	flag.Parse()

	if fl.showVersion {
		fmt.Println(version.Version)
		return
	}

	// Single-active guard: exclusive lock on the data dir (CORE-508).
	release, err := lockDataDir(fl.dataDir)
	if err != nil {
		log.Fatalf("aihub: %v", err)
	}
	defer release()

	db, err := core.Open(fl.dataDir)
	if err != nil {
		log.Fatalf("aihub: open core: %v", err)
	}
	defer db.Close()

	// Fresh-install bootstrap: an empty users table gets one admin; its
	// one-time api key goes to a 0600 file in the data dir, never to the
	// log (under compose the log is persisted by docker logs).
	if keyFile := bootstrapAdmin(db, fl.dataDir); keyFile != "" {
		log.Printf("aihub: fresh install: seeded bootstrap admin usr_root — its api key is in %s (0600; store it, then delete the file)", keyFile)
	}

	gitMgr, err := git.NewManager(fl.dataDir, core.NewOwnerManager(db))
	if err != nil {
		log.Fatalf("aihub: git manager: %v", err)
	}

	jan := git.NewJanitor(gitMgr, fl.janitorFreq)
	jan.Start()
	defer jan.Stop()

	srv := server.New(db, fl.dataDir, gitMgr)
	srv.StaleAfter = fl.staleAfter
	httpSrv := server.NewHTTPServer(fl.listen, srv)

	// Bind first, then migrate, then serve: a client connecting during the
	// one-time memory content migration waits in the listen backlog
	// instead of being refused, and no memory is served before its row
	// carries its content.
	ln, err := net.Listen("tcp", fl.listen)
	if err != nil {
		log.Fatalf("aihub: listen: %v", err)
	}
	rep, err := srv.Mems.MigrateContent()
	if err != nil {
		log.Fatalf("aihub: memory content migration: %v", err)
	}
	logMigration(rep)

	// Converge an interrupted project delete: the per-WI bare git repos
	// a committed delete recorded (admin_audit.wi_ids) are removed here
	// when the request itself could not (a crash between the delete's
	// commit and the removal). Idempotent; errors are logged, never
	// fatal — the next start retries.
	srv.ReplayDeletedRepos()

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Printf("aihub: shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("aihub: listening on %s (data-dir %s)", fl.listen, fl.dataDir)
	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("aihub: %v", err)
	}
}

// logMigration logs one content migration pass: the counts always, the
// ids of every row that needed more than a copy.
func logMigration(rep memory.MigrateReport) {
	log.Printf("aihub: memory content migration: %d copied, %d adopted (file differed from content_sha), %d redact completed, %d missing, %d titles derived, in %s",
		rep.Copied, len(rep.Adopted), len(rep.Redacted), len(rep.Missing), rep.Titled, rep.Elapsed.Round(time.Millisecond))
	for _, l := range []struct {
		what string
		ids  []string
	}{{"adopted", rep.Adopted}, {"redact completed", rep.Redacted}, {"missing (not served)", rep.Missing}} {
		if len(l.ids) > 0 {
			log.Printf("aihub: memory content migration: %s: %s", l.what, strings.Join(l.ids, " "))
		}
	}
}

// bootstrapKeyFile is the data-dir file a fresh install writes the
// bootstrap admin's plaintext api key to (0600).
const bootstrapKeyFile = "bootstrap-admin.key"

// bootstrapAdmin seeds the first admin identity on a fresh install and
// writes its plaintext api key to <dataDir>/bootstrap-admin.key (0600).
// The file is written before the seed commits, so a failed write seeds
// nothing and the next start retries. Returns the key file path when it
// seeded one, "" otherwise.
func bootstrapAdmin(db *core.DB, dataDir string) string {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n > 0 {
		return ""
	}
	const uid = "usr_root"
	keyID, err := core.NewID("key_")
	if err != nil {
		log.Fatalf("aihub: bootstrap: %v", err)
	}
	plain, err := core.NewSecret(24)
	if err != nil {
		log.Fatalf("aihub: bootstrap: %v", err)
	}
	sum := sha256.Sum256([]byte(plain))
	tx, err := db.Begin()
	if err != nil {
		log.Fatalf("aihub: bootstrap: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO users(id, display_name, email, kind, role, created_at)
		VALUES (?1,'bootstrap admin','root@localhost','human','admin',?2)`, uid, core.Now()); err != nil {
		log.Fatalf("aihub: bootstrap: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO api_keys(id, user_id, project_id, name, key_hash, created_at)
		VALUES (?1,?2,NULL,'bootstrap',?3,?4)`, keyID, uid, hex.EncodeToString(sum[:]), core.Now()); err != nil {
		log.Fatalf("aihub: bootstrap: %v", err)
	}
	path := filepath.Join(dataDir, bootstrapKeyFile)
	if err := writeKeyFile(path, plain); err != nil {
		log.Fatalf("aihub: bootstrap: write %s: %v", path, err)
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(path)
		log.Fatalf("aihub: bootstrap: %v", err)
	}
	return path
}

// writeKeyFile writes key to a fresh 0600 file at path (a leftover file
// from an earlier, uncommitted bootstrap is replaced, never reused).
func writeKeyFile(path, key string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(key + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
