package components

// recover.go — the journal's recovery rules, the other half of AC1's
// interruption clause: whatever commit stage a kill interrupted, the
// next run starts from a defined state and converges.
//
// The rules, in the order they apply (the caller holds the lock):
//
//   - a journal whose switch already committed — current is on the
//     journal's new version — completes the switch: previous moves to
//     the journal's old version, and the journal is cleared. This is
//     the crash between the current swap and the previous update: the
//     reboot finishes what was interrupted, and previous is again
//     "the version current replaced".
//   - a journal whose switch never committed — current is still on
//     the old version — is discarded: nothing moved, so there is
//     nothing to complete. (The next install rewrites the journal and
//     finishes the switch on its own.)
//   - a malformed journal is an error, never a guess.
//   - staging leftovers never survive: an entry there was never
//     published and never will be.

import (
	"fmt"
	"path/filepath"
)

// recoverPending brings a root an interrupted run left behind back to
// a defined state. Install calls it first thing under the lock; a
// caller that only wants the state repaired may call it on its own
// (with the lock held).
func recoverPending(root string) error {
	j, err := readJournal(root)
	if err != nil {
		return err
	}
	if j != nil {
		target, err := CurrentTarget(root)
		if err != nil {
			return fmt.Errorf("recovering the interrupted switch: %w", err)
		}
		if target == filepath.Join(ReleasesDirname, j.NewVersion) {
			// the switch committed: finish it — previous names the
			// version current replaced.
			if j.OldVersion != "" {
				if err := swapLink(root, PreviousLinkname, filepath.Join(ReleasesDirname, j.OldVersion)); err != nil {
					return fmt.Errorf("completing previous after the interrupted switch: %w", err)
				}
			}
		}
		// committed or not, the journal's work is decided now: clear it.
		if err := removeJournal(root); err != nil {
			return fmt.Errorf("clearing the interrupted switch's journal: %w", err)
		}
	}
	return cleanStaging(root)
}
