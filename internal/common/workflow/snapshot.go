package workflow

// snapshot.go: the capsule's snapshot of a lane. A claim copies the lane
// it resolved (and the shared `_shared/` tree beside it) into the capsule
// and records, in the snapshot's completion marker, the contract version
// the lane targets, where it came from and a hash of its content; every
// later read of the work item's lane reads that snapshot, never the
// daemon's current tree. A snapshot whose contract version is not the
// daemon's cannot run (the workflow blocks with kind `contract`).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SourceBuiltin is the snapshot source of a lane the daemon's release
// ships (its workflows dir).
const SourceBuiltin = "builtin"

// ContentHash answers the sha256 of a snapshot's content: its files'
// root-relative slash paths, sorted, each followed by its bytes, every
// field length-prefixed so no two file sets hash alike.
func ContentHash(root string, rels []string) (string, error) {
	sorted := append([]string(nil), rels...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, rel := range sorted {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s\x00%d:", len(rel), rel, len(raw))
		h.Write(raw)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
