package runtime

// materialize.go: the F7 lane — the bundle copy into a freshly cloned
// capsule:
//
//   - the full bundle tree lands under workflows/<bundle>/ (the driver
//     reads step definitions from workflows/<bundle>/steps/ directly);
//     the shared step definitions (workflows/_shared/, when the checkout
//     has them) land beside it under workflows/_shared/;
//   - the completion marker sync/materialized.json records the pinned
//     bundle identity (name) and the full targets list, written LAST;
//     recovery relaunches only over a snapshot the marker vouches for
//     (materializationComplete).
//
// Materialize runs once per claim, on a fresh clone, before any agent
// starts (prepareCapsule never reuses a previous scene), so the copy
// needs no staging: whatever an earlier claim committed under the two
// targets is removed and the checkout's copy replaces it.
//
// sync/ is daemon-private state: it never enters the capsule's revision
// chain — the .gitignore carries /sync/ for exactly that reason.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

const (
	// RelMaterializeMarker is the completion marker: sync/materialized.json.
	RelMaterializeMarker = "sync/materialized.json"
	// gitignoreSyncLine is appended to the capsule .gitignore: sync/ is
	// daemon-private marker state and never enters the chain.
	gitignoreSyncLine = "/sync/\n"
)

// materializeMarkerRecord is the content of the completion marker: the
// pinned bundle identity, every materialized file and the completion
// timestamp.
type materializeMarkerRecord struct {
	BundleName string `json:"bundle_name"`
	// Targets lists every materialized file's capsule-relative path
	// (slash-separated, sorted).
	Targets     []string `json:"targets"`
	CompletedAt string   `json:"completed_at"`
}

// materializationComplete is the recovery trust gate: the capsule's
// snapshot is complete for THIS job's pinned bundle — the marker parses,
// names the job's bundle, records a target set, and every recorded
// target is a regular file on disk. Anything else answers false (fail
// closed).
func (r *Runtime) materializationComplete(j *job) bool {
	if j.bundleName == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(j.capsuleDir, filepath.FromSlash(RelMaterializeMarker)))
	if err != nil {
		return false
	}
	var m materializeMarkerRecord
	if json.Unmarshal(raw, &m) != nil || m.BundleName != j.bundleName || len(m.Targets) == 0 {
		return false
	}
	for _, rel := range m.Targets {
		info, serr := os.Stat(filepath.Join(j.capsuleDir, filepath.FromSlash(rel)))
		if serr != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// materializeBundle copies the pinned bundle (and the shared step
// definitions beside it) into the capsule and writes the completion
// marker last. Claim-time entry.
func (r *Runtime) materializeBundle(j *job) error {
	if r.cfg.WorkflowsDir == "" || j.bundleName == "" {
		return nil
	}
	if j.bundleName == workflow.SharedDir {
		return fmt.Errorf("runtime: %s: %q holds the shared step definitions and is not a workflow bundle", j.wi, workflow.SharedDir)
	}
	src := filepath.Join(r.cfg.WorkflowsDir, j.bundleName)
	if !dirExists(src) {
		fmt.Fprintf(os.Stderr, "runtime: %s: bundle %s not in checkout; capsule materialized without it\n", j.wi, j.bundleName)
		return nil
	}
	pieces := map[string]string{capsule.DirWorkflows + "/" + j.bundleName: src}
	// The driver falls back to the shared step definitions for any step
	// the bundle carries no own steps/<id>.md for.
	if shared := filepath.Join(filepath.Dir(src), workflow.SharedDir); dirExists(shared) {
		pieces[capsule.DirWorkflows+"/"+workflow.SharedDir] = shared
	}
	var targets []string
	for rel, from := range pieces {
		live := filepath.Join(j.capsuleDir, filepath.FromSlash(rel))
		if err := os.RemoveAll(live); err != nil {
			return err
		}
		if err := copyTree(from, live); err != nil {
			return err
		}
		files, err := regularFiles(j.capsuleDir, live)
		if err != nil {
			return err
		}
		targets = append(targets, files...)
	}
	sort.Strings(targets)
	blob, err := json.MarshalIndent(materializeMarkerRecord{
		BundleName:  j.bundleName,
		Targets:     targets,
		CompletedAt: nowStamp(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(RelMaterializeMarker)), blob, 0o444)
}

// regularFiles answers every regular file under dir as a path relative
// to base, slash-separated.
func regularFiles(base, dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || !info.Mode().IsRegular() {
			return werr
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
