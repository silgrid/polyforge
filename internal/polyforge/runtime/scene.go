package runtime

// scene.go: the capsule scene on disk — the clone into the external
// git-dir layout, the quarantine of a previous scene, the materialized layout and work-item file, and the
// read-only seal of a closed scene.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// prepareCapsule makes CapsuleDir a git worktree whose git-dir is EXTERNAL
// (ruling #2: agents never reach hooks/config/index). The aihub bare repo is
// cloned straight into that layout (gitops.Clone: the git-dir at
// o.GitDir, a one-line .git pointer file in the worktree; an
// empty-history clone succeeds with a warning).
//
// A claim always starts from a fresh clone: any scene already at the
// capsule path (another session's, a closed one, the husk of a start
// that crashed before its record landed) is quarantined, never reused.
// The worktree and the external git-dir move aside as ONE unit, so the
// fresh clone can never meet a previous scene's HEAD/index (a stale
// git-dir reused under a fresh worktree made every later push
// non-fast-forward).
func (r *Runtime) prepareCapsule(ctx context.Context, j *job) error {
	o := j.git
	if err := quarantineCapsule(o.CapsuleDir, o.GitDir); err != nil {
		return err
	}
	if o.RemoteURL == "" {
		// A capsule whose origin cannot be resolved is never silently
		// init'ed locally and restarted from step 1 (there is no local-init
		// escape hatch: aihub always supplies the remote at claim). Loud and
		// typed: the caller abandons the claim.
		return &api.Error{Code: api.ErrDaemonCapsuleClone,
			Detail: fmt.Sprintf("capsule clone failed: no remote URL is configured for %s — aihub always supplies one at claim; refusing a local init", j.wi)}
	}
	if err := gitops.Clone(ctx, o); err != nil {
		// aihub creates the bare repo at claim, so a clone failure is
		// never "nothing published yet": refuse loudly (the caller
		// abandons the claim) rather than init locally and restart the
		// workflow from step 1.
		return &api.Error{Code: api.ErrDaemonCapsuleClone,
			Detail: fmt.Sprintf("capsule clone failed: %v", err)}
	}
	return nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// quarantineCapsule moves a stale/foreign/superseded capsule aside, never
// deleting it (v1.5 naming, reference.md §3.5: capsules/<name> →
// capsules/<name>.quarantined.<ts> — a sibling, so the scene sits next to
// whatever replaces it and the bytes always survive).
func quarantineCapsule(capsuleDir, gitDir string) error {
	ts := strconv.FormatInt(time.Now().UnixNano(), 36)
	// The external git-dir is part of the same unit: it moves under the
	// same timestamp, as a sibling of the original, never deleted. The
	// two moves are ALL-OR-NOTHING (v2-converge §6): a failed second move
	// puts the first back, so the scene is never split between a
	// quarantined git-dir and a live worktree pointing at it.
	movedGit := ""
	if gitDir != "" && dirExists(gitDir) {
		movedGit = gitDir + ".quarantined." + ts
		if err := renameFn(gitDir, movedGit); err != nil {
			return err
		}
	}
	if !dirExists(capsuleDir) {
		return nil // nothing to move (a capsule never materialized)
	}
	// The worktree's .git pointer names the git-dir by absolute path. It
	// follows the git-dir BEFORE the worktree moves: left in place it
	// would name the fresh clone's git-dir that takes the old path, and a
	// git run in the quarantined tree would commit that tree onto the
	// live chain.
	if err := repointWorktree(capsuleDir, gitDir, movedGit); err != nil {
		if movedGit != "" {
			if rerr := renameFn(movedGit, gitDir); rerr != nil {
				return fmt.Errorf("quarantine split the scene: repointing the worktree failed (%w) and restoring the git-dir from %s failed (%v)", err, movedGit, rerr)
			}
		}
		return fmt.Errorf("quarantine of the capsule failed, git-dir restored: %w", err)
	}
	crashpoint.At("quarantine.between")
	if err := renameFn(capsuleDir, capsuleDir+".quarantined."+ts); err != nil {
		if movedGit != "" {
			if rerr := renameFn(movedGit, gitDir); rerr != nil {
				return fmt.Errorf("quarantine split the scene: moving the worktree failed (%w) and restoring the git-dir from %s failed (%v)", err, movedGit, rerr)
			}
			_ = repointWorktree(capsuleDir, movedGit, gitDir)
		}
		return fmt.Errorf("quarantine of the capsule failed, git-dir restored: %w", err)
	}
	return nil
}

// quarantinedGitDirs lists the quarantined git-dirs of gitDir
// (<gitDir>.quarantined.<ts>, quarantineCapsule's naming), newest first.
func quarantinedGitDirs(gitDir string) []string {
	prefix := filepath.Base(gitDir) + ".quarantined."
	entries, _ := os.ReadDir(filepath.Dir(gitDir))
	type q struct {
		path string
		ts   int64
	}
	var qs []q
	for _, e := range entries {
		ts, err := strconv.ParseInt(strings.TrimPrefix(e.Name(), prefix), 36, 64)
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || err != nil {
			continue
		}
		qs = append(qs, q{filepath.Join(filepath.Dir(gitDir), e.Name()), ts})
	}
	sort.Slice(qs, func(a, b int) bool { return qs[a].ts > qs[b].ts })
	out := make([]string, len(qs))
	for i, e := range qs {
		out[i] = e.path
	}
	return out
}

// samePath compares two absolute paths whose last element may not exist
// (git records the git-dir with its parent's symlinks resolved).
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	resolve := func(p string) string {
		dir, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			return filepath.Clean(p)
		}
		return filepath.Join(dir, filepath.Base(p))
	}
	return resolve(a) == resolve(b)
}

// repointWorktree rewrites a worktree's .git pointer that names gitDir
// from to the git-dir to; an empty to (the git-dir is gone) removes the
// pointer. A pointer naming anything else is left alone.
func repointWorktree(worktree, from, to string) error {
	pointer := filepath.Join(worktree, ".git")
	raw, err := os.ReadFile(pointer)
	if err != nil || from == "" || !samePath(strings.TrimSpace(strings.TrimPrefix(string(raw), "gitdir:")), from) {
		return nil
	}
	if to == "" {
		return os.Remove(pointer)
	}
	return atomicfile.Write(pointer, []byte("gitdir: "+to+"\n"), 0o644)
}

// renameFn is the filesystem move quarantineCapsule uses (a seam for the
// injected-failure test).
var renameFn = os.Rename

// materialize lays out the freshly cloned capsule: workflows/,
// capsule.json, .gitignore and the runtime-state/ tree. The bundle copy
// ends with a completion marker (F7, materialize.go; no digest
// verification — there is no digest chain, polyforge#149 B9). The claim's
// task-repo inventory is cloned into repos/<name> BEFORE the capsule
// identity is written, so the recorded baselines are the true claim-time
// SHAs.
func (r *Runtime) materialize(ctx context.Context, j *job) error {
	for _, d := range []string{
		capsule.DirArtifacts, capsule.DirLogs, capsule.DirRepos,
		capsule.DirRuntimeState, capsule.DirSession, capsule.DirWorkflows,
		filepath.Join(capsule.DirLogs, "session"),
	} {
		if err := os.MkdirAll(filepath.Join(j.capsuleDir, filepath.FromSlash(d)), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(j.capsuleDir, ".gitignore"),
		[]byte(capsule.GitignoreContent+gitignoreSyncLine), 0o644); err != nil {
		return err
	}
	// Task repos land before capsule.json so the identity below can
	// record their claim-time HEAD SHAs. The clones carry their own
	// independent .git directories and repos/ is capsule-gitignored,
	// so the capsule's history never absorbs them.
	repoSHAs, err := r.cloneTaskRepos(ctx, j)
	if err != nil {
		return err
	}
	idn := capsule.Identity{
		WorkItemID: j.wi,
		CapsuleID:  "cap_" + j.wi,
		Project:    j.project,
		BundleName: j.bundleName,
		Repos:      repoSHAs,
		CreatedAt:  nowStamp(),
	}
	// polyforge#79: the claim-time audit record — the parsed yaml
	// verbatim and the resolved repo sources. A no-yaml claim records
	// NOTHING (omitempty, never fabricated) and its legacy fields are
	// unchanged. (No per-step effective-candidate list is recorded: the
	// workspace yaml is the recovery re-derivation's own governing source.)
	blob, _ := json.MarshalIndent(idn, "", "  ")
	if err := atomicfile.Write(filepath.Join(j.capsuleDir, capsule.CapsuleJSON), blob, 0o644); err != nil {
		return err
	}
	// Task-repo inventory and the F1 task-definition file land before
	// the bundle copy; the materialized work-item.json is the ONE
	// authoritative statement of the ask inside the capsule (v1 has no
	// agent→server read path, so without this file the agent cannot even
	// know its goal).
	if err := r.writeWorkItemDoc(j, j.item); err != nil {
		return err
	}
	// F6-A: the claim-time memory snapshot — a static recall answer the
	// agent reads like any other capsule input. Best-effort by design:
	// an unreachable aihub or a zero-hit query degrades to an empty list
	// plus a notice, never a failed spawn (see recall.go).
	r.writeRecalledMemories(j)
	r.writeMemoryPolicy(j)
	// F7: the bundle lands under workflows/<bundle> and
	// sync/materialized.json is written last (materialize.go).
	return r.materializeBundle(j)
}

// ensureWorkItemFile repairs a capsule whose work-item.json is missing
// (a pre-F1 capsule, or a torn scene): the in-memory claim
// snapshot answers when present, aihub does otherwise. Best-effort — a
// repair failure is loud but never blocks recovery.
func (r *Runtime) ensureWorkItemFile(j *job) {
	path := filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.WorkItemJSON))
	if _, err := os.Stat(path); err == nil {
		return
	}
	item := j.item
	if item == nil {
		fetched, err := r.hub.GetWorkItem(j.wi)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runtime: %s: work-item.json missing and aihub lookup failed: %v\n", j.wi, err)
			return
		}
		item = fetched
	}
	if err := r.writeWorkItemDoc(j, item); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: work-item.json repair failed: %v\n", j.wi, err)
	}
}

func (r *Runtime) writeWorkItemDoc(j *job, item *api.WorkItem) error {
	if item == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(j.capsuleDir, capsule.DirRuntimeState), 0o755); err != nil {
		return err
	}
	doc := &capsule.WorkItemDoc{
		ID:         j.wi,
		Slug:       item.Slug,
		Project:    j.project,
		Goal:       item.Goal,
		Content:    item.Content,
		Labels:     item.Labels,
		Priority:   item.Priority,
		Milestone:  item.Milestone,
		BundleName: j.bundleName,
		UpdatedAt:  nowStamp(),
	}
	if doc.Labels == nil {
		doc.Labels = []string{}
	}
	if doc.Priority == "" {
		doc.Priority = api.PriorityNormal
	}
	blob, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.WorkItemJSON)), blob, 0o644)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, ierr := os.ReadFile(p)
		if ierr != nil {
			return ierr
		}
		return atomicfile.Write(target, data, 0o444)
	})
}

// sealReadOnly makes the preserved scene read-only (files 0444, dirs 0555;
// symlinks untouched — chmod never follows a link out of the scene).
// Best-effort per entry: the walk reports the first hard error.
func sealReadOnly(root string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return os.Chmod(p, 0o555)
		}
		if info.Mode().IsRegular() {
			return os.Chmod(p, 0o444)
		}
		return nil
	})
}
