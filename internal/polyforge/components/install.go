package components

// install.go — the local install transaction every component switch
// goes through (polyforge#162): the workstation's first boot and #92's
// in-container upgrade verb both call Install and hold the same lock,
// so there is one version comparator, one staging discipline and one
// chain-switching rule, never two.
//
// The transaction's shape is the ticket's settled one: stage the seed
// under staging/, verify the staged copy against the manifest's
// digests, publish it by one rename into releases/<ver>, then swap the
// root's current symlink — the single commit point. previous follows
// current; a journal records the switch in progress and recover.go
// completes or discards it, so an interruption at any point leaves
// current on a complete version and previous explainable.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
)

// The sentinel errors a caller matches on. Every one of them leaves
// current, previous and the published versions exactly as they were.
var (
	// ErrLockHeld: another process holds the install lock.
	ErrLockHeld = errors.New("the components install lock is held by another process")

	// ErrLegacyLayout: the components root carries the #161-era shape
	// (a release/ directory with a current symlink). No auto-import,
	// no bridge: the migration is by hand or a fresh volume.
	ErrLegacyLayout = errors.New("the components root carries the pre-#162 release layout")

	// ErrDowngrade: the seed is older than current.
	ErrDowngrade = errors.New("refusing the downgrade: the seed is older than the current version")

	// ErrChannelMismatch: the seed and current are of different
	// channels (kind or repository) — an implicit cross-channel switch.
	ErrChannelMismatch = errors.New("refusing the cross-channel switch")

	// ErrSameVersionDifferentDigest: the seed and current carry the
	// same version with different content.
	ErrSameVersionDifferentDigest = errors.New("the same version carries a different digest")

	// ErrDigestMismatch: the seed's (or the staged copy's) content does
	// not match its manifest's digests.
	ErrDigestMismatch = errors.New("the content does not match the manifest's digests")

	// ErrCurrentCorrupt: current points at a version directory whose
	// manifest cannot be read — the install refuses to guess.
	ErrCurrentCorrupt = errors.New("the current version is corrupt")

	// ErrPlatformMismatch: the seed was built for another platform.
	ErrPlatformMismatch = errors.New("the seed was built for another platform")

	// ErrBaseTooOld: the running base does not meet the seed's minimum
	// base requirement.
	ErrBaseTooOld = errors.New("the running base is older than the seed's minimum base requirement")
)

// Options is one install's inputs.
type Options struct {
	// Root is the components volume's root (the /components volume in
	// the workstation): releases/, current, previous and the
	// transaction internals live directly in it.
	Root string
	// Seed is the seed's version directory (the image's
	// /opt/polyforge/seed/releases/<ver>, or any directory of the same
	// shape).
	Seed string
	// BaseVersion is the running base's (bootstrap pf's) version, for
	// the manifest's minimum base requirement.
	BaseVersion string
	// Platform is the running platform (os/arch, e.g. linux/amd64).
	Platform string
}

// Result is what one install did.
type Result struct {
	// Action: "installed" (no current before), "upgraded" (current
	// moved) or "noop" (the same version and digest were already
	// current — nothing was written).
	Action string
	// Version is the version that is current after the install.
	Version string
	// From is the version that was current before ("" on a first
	// install).
	From string
}

// The action names a Result carries.
const (
	ActionInstalled = "installed"
	ActionUpgraded  = "upgraded"
	ActionNoop      = "noop"
)

// The crash points of the transaction, in order. PF_CRASH_AT kills the
// process at the named point; recovery_test.go drives them one by one.
const (
	crashStaged         = "components.staged"          // staged copy verified, not yet published
	crashPublished      = "components.published"       // published into releases/, current not yet swapped
	crashJournaled      = "components.journaled"       // journal written, current not yet swapped
	crashCurrentSwapped = "components.current-swapped" // current on the new version, previous not yet updated
)

// journal is the switch-in-progress record. Its recovery rule is
// recover.go's: journal present and current already on newVersion →
// complete previous; journal present and current still on oldVersion →
// the switch never committed, discard.
type journal struct {
	SchemaVersion int    `json:"schemaVersion"`
	NewVersion    string `json:"newVersion"`
	OldVersion    string `json:"oldVersion"`
}

// journalSchemaVersion is the journal shape this package reads.
const journalSchemaVersion = 1

// Install installs the seed into the components root and switches
// current to it under the install lock. A refusal — a downgrade, a
// cross-channel seed, the same version with a different digest, a
// corrupt seed or current, another platform, an old base, a legacy
// layout or a held lock — answers a sentinel error and writes nothing.
func Install(opts Options) (*Result, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("components install: Root is required")
	}
	if opts.Seed == "" {
		return nil, fmt.Errorf("components install: Seed is required")
	}
	if err := os.MkdirAll(opts.Root, 0o755); err != nil {
		return nil, fmt.Errorf("components root %s: %w", opts.Root, err)
	}

	// the #161-era shape is refused before anything else: it carries
	// no manifests and no digests, and it is never imported.
	if _, err := os.Lstat(filepath.Join(opts.Root, LegacyReleaseDirname)); err == nil {
		return nil, fmt.Errorf("%w: %s carries a %s/ directory (the pre-#162 data-dir layout, %s). This layout has no manifests and is not imported automatically: migrate by hand — move or copy each complete version into %s/<ver> of a fresh components root (the #162 layout keeps every version in %s/<ver> with the %s and %s symlinks beside them at the root) — or start the volume empty and let the seed install",
			ErrLegacyLayout, opts.Root, LegacyReleaseDirname,
			filepath.Join(LegacyReleaseDirname, CurrentLinkname),
			ReleasesDirname, ReleasesDirname, CurrentLinkname, PreviousLinkname)
	}

	lock, err := holdLock(opts.Root)
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	// complete or discard a switch an earlier run left in progress, and
	// clear staging leftovers (recover.go owns the rules).
	if err := recoverPending(opts.Root); err != nil {
		return nil, err
	}

	seedManifest, err := ReadManifest(opts.Seed)
	if err != nil {
		return nil, fmt.Errorf("the seed: %w", err)
	}
	if seedManifest.Platform != opts.Platform {
		return nil, fmt.Errorf("%w: the seed is %s, the volume runs %s",
			ErrPlatformMismatch, seedManifest.Platform, opts.Platform)
	}
	if ok, err := BaseSatisfies(opts.BaseVersion, seedManifest.MinBase); err != nil {
		return nil, fmt.Errorf("the seed's minimum base requirement: %w", err)
	} else if !ok {
		return nil, fmt.Errorf("%w: the base runs %s, the seed requires %s",
			ErrBaseTooOld, opts.BaseVersion, seedManifest.MinBase)
	}
	// the seed is judged in place before anything is staged: a bad
	// digest never reaches the volume.
	if err := seedManifest.VerifyTree(opts.Seed); err != nil {
		return nil, fmt.Errorf("%w: the seed at %s: %w", ErrDigestMismatch, opts.Seed, err)
	}
	seedVersion, err := ParseVersion(seedManifest.Version)
	if err != nil {
		return nil, fmt.Errorf("the seed: %w", err)
	}

	// compare with current: the one place the switch is decided.
	res := &Result{Action: ActionInstalled, Version: seedManifest.Version}
	final := filepath.Join(opts.Root, ReleasesDirname, seedManifest.Version)
	if target, err := CurrentTarget(opts.Root); err != nil {
		return nil, err
	} else if target != "" {
		curDir := filepath.Join(opts.Root, target)
		curManifest, err := ReadManifest(curDir)
		if err != nil {
			return nil, fmt.Errorf("%w: %s points at %s and its manifest cannot be read (%v). To recover: point %s at a complete %s/<ver> by hand (or restore that version's directory from a complete source), then re-run the install — it refuses to guess",
				ErrCurrentCorrupt, CurrentLinkname, target, err, CurrentLinkname, ReleasesDirname)
		}
		curVersion, err := ParseVersion(curManifest.Version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s points at %s and its version %q is of no accepted form (%v)",
				ErrCurrentCorrupt, CurrentLinkname, target, curManifest.Version, err)
		}
		if curVersion.Channel != seedVersion.Channel || curManifest.ChannelRepo != seedManifest.ChannelRepo {
			return nil, fmt.Errorf("%w: current is %s of %s, the seed is %s of %s — switching channels is an explicit act, never an install side effect",
				ErrChannelMismatch, curVersion.String(), curManifest.ChannelRepo,
				seedVersion.String(), seedManifest.ChannelRepo)
		}
		cmp, err := curVersion.Compare(seedVersion)
		if err != nil {
			return nil, err
		}
		switch {
		case cmp == 0:
			if !sameContent(curManifest, seedManifest) {
				return nil, fmt.Errorf("%w: current and the seed are both %s but their manifests' digests differ",
					ErrSameVersionDifferentDigest, seedManifest.Version)
			}
			// the same version with the same content: nothing to do,
			// and nothing is written.
			res.Action = ActionNoop
			res.From = curManifest.Version
			return res, nil
		case cmp > 0:
			return nil, fmt.Errorf("%w: current is %s, the seed is %s — use a newer seed, or switch by hand if an older version is really wanted",
				ErrDowngrade, curVersion.String(), seedVersion.String())
		default:
			res.Action = ActionUpgraded
			res.From = curManifest.Version
		}
	}

	if err := os.MkdirAll(filepath.Join(opts.Root, ReleasesDirname), 0o755); err != nil {
		return nil, err
	}
	// publish: an interrupted earlier run may have published this
	// version already — a complete copy with the same digests is
	// reused, a different one is the same-version-different-digest
	// refusal.
	if _, err := os.Lstat(final); err == nil {
		if err := seedManifest.VerifyTree(final); err != nil {
			return nil, fmt.Errorf("%w: %s already exists with different content: %w",
				ErrSameVersionDifferentDigest, final, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		stage, err := stageSeed(opts.Root, opts.Seed, seedManifest)
		if err != nil {
			return nil, err
		}
		if err := seedManifest.VerifyTree(stage); err != nil {
			_ = os.RemoveAll(stage)
			return nil, fmt.Errorf("%w: the staged copy: %w", ErrDigestMismatch, err)
		}
		crashpoint.At(crashStaged)
		if err := os.Rename(stage, final); err != nil {
			_ = os.RemoveAll(stage)
			return nil, fmt.Errorf("publish %s: %w", final, err)
		}
		if err := dirSync(filepath.Join(opts.Root, ReleasesDirname)); err != nil {
			return nil, err
		}
		crashpoint.At(crashPublished)
	}

	// journal, then the one commit point, then previous.
	if res.From != "" {
		if err := writeJournal(opts.Root, &journal{
			SchemaVersion: journalSchemaVersion,
			NewVersion:    seedManifest.Version,
			OldVersion:    res.From,
		}); err != nil {
			return nil, err
		}
		crashpoint.At(crashJournaled)
	}
	if err := swapLink(opts.Root, CurrentLinkname, filepath.Join(ReleasesDirname, seedManifest.Version)); err != nil {
		return nil, err
	}
	crashpoint.At(crashCurrentSwapped)
	if res.From != "" {
		if err := swapLink(opts.Root, PreviousLinkname, filepath.Join(ReleasesDirname, res.From)); err != nil {
			return nil, err
		}
		if err := removeJournal(opts.Root); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// sameContent reports whether two manifests of the same version carry
// the same digests (and component versions) — the noop test.
func sameContent(a, b *Manifest) bool {
	if len(a.Components) != len(b.Components) {
		return false
	}
	for i := range a.Components {
		ca, cb := &a.Components[i], &b.Components[i]
		if ca.Name != cb.Name || ca.Digest != cb.Digest || ca.Version != cb.Version {
			return false
		}
	}
	return true
}

// holdLock takes the root's install lock (LOCK_EX|LOCK_NB, creating
// the lock file). The kernel drops it with the holder's process:
// crash equals shutdown.
func holdLock(root string) (*os.File, error) {
	path := filepath.Join(root, LockFilename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s", ErrLockHeld, path)
	}
	return f, nil
}

// stageSeed copies the seed into a fresh staging entry and answers the
// copy's path. Staging lives in the root, so the publish is one rename
// on the same filesystem.
func stageSeed(root, seed string, m *Manifest) (string, error) {
	if err := os.MkdirAll(filepath.Join(root, StagingDirname), 0o755); err != nil {
		return "", err
	}
	stage := filepath.Join(root, StagingDirname, fmt.Sprintf("%s.%d", m.Version, os.Getpid()))
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := copyTree(seed, stage); err != nil {
		_ = os.RemoveAll(stage)
		return "", fmt.Errorf("stage the seed: %w", err)
	}
	return stage, nil
}

// copyTree copies the tree at src to dst (which must not exist):
// directories, regular files with their permission bits and symlinks
// with their targets, verbatim. Nothing else is accepted.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := copyFile(p, target, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode()&fs.ModeSymlink != 0:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: a %s entry is not part of a component tree", p, info.Mode())
		}
		return nil
	})
}

// copyFile copies one regular file, preserving its permission bits.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}

// swapLink replaces (or creates) the root's symlink name so it points
// at target: a fresh symlink beside it, then one rename over the name.
// The rename is the commit; no consumer ever sees a missing link.
func swapLink(root, name, target string) error {
	link := filepath.Join(root, name)
	tmp := filepath.Join(root, fmt.Sprintf(".%s.tmp.%d", name, os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return dirSync(root)
}

// dirSync fsyncs one directory so a rename in it is durable.
func dirSync(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return err
	}
	return nil
}

// writeJournal crash-atomically records the switch in progress.
func writeJournal(root string, j *journal) error {
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(root, JournalFilename), body, 0o644)
}

// readJournal answers the switch-in-progress record, or nil when none
// is in progress.
func readJournal(root string) (*journal, error) {
	raw, err := os.ReadFile(filepath.Join(root, JournalFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var j journal
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(root, JournalFilename), err)
	}
	if j.SchemaVersion != journalSchemaVersion {
		return nil, fmt.Errorf("%s: schema version %d, this layout reads %d",
			filepath.Join(root, JournalFilename), j.SchemaVersion, journalSchemaVersion)
	}
	return &j, nil
}

// removeJournal deletes the switch record (the switch is complete).
func removeJournal(root string) error {
	if err := os.Remove(filepath.Join(root, JournalFilename)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return dirSync(root)
}

// recoverPending brings a root an interrupted run left behind back to a
// defined state. The full rules are recover.go's.
func recoverPending(root string) error { return cleanStaging(root) }

// cleanStaging removes every staging entry — an entry there was never
// published and never will be (the caller holds the lock).
func cleanStaging(root string) error {
	dir := filepath.Join(root, StagingDirname)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
