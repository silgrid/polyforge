package daemon

// query.go: the holder half of the cross-machine query face
// (workbench#11). Every registered daemon — runner included — long-polls
// the signed inbox and answers progress/artifact queries from its own
// local state. Nothing is stored; the answer is relayed by aihub.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

const artifactMaxBytes = 4 << 20

func (d *Daemon) queryLoop(ctx context.Context) {
	client := d.faceClient()
	if client == nil {
		return
	}
	failStreak := 0
	for ctx.Err() == nil {
		inbox, err := client.Inbox("queries", inboxWait)
		if err != nil {
			failStreak++
			if failStreak == 1 {
				fmt.Fprintf(os.Stderr, "daemon: machine query inbox: %v (retrying)\n", err)
			}
			if !faceSleep(ctx, time.Second) {
				return
			}
			continue
		}
		if failStreak > 0 {
			failStreak = 0
		}
		for _, q := range inbox.Queries {
			body := d.answerQuery(q)
			if err := client.AnswerQuery(q.ID, body); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: answer query %s: %v\n", q.ID, err)
			}
		}
	}
}

const inboxWait = 25 * time.Second

func (d *Daemon) answerQuery(q api.MachineQuery) api.QueryAnswerBody {
	body := api.QueryAnswerBody{HolderMachine: d.cfg.MachineID}
	switch q.Kind {
	case "artifact":
		ans, err := d.readArtifact(q.WorkItem, q.ArtifactPath)
		if err != nil {
			body.Error = asAPIError(err)
			return body
		}
		body.Artifact = ans
	default:
		snap, err := d.progressSnapshot(q.WorkItem)
		if err != nil {
			body.Error = asAPIError(err)
			return body
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			body.Error = asAPIError(err)
			return body
		}
		body.Snapshot = raw
	}
	return body
}

// progressSnapshot builds exactly the local snapshot the holder's own
// `pf watch` route serves. It answers the current state immediately: a
// blocked/needs_input/paused held WI is queryable and must never be
// delayed waiting for some other state.
func (d *Daemon) progressSnapshot(wi string) (*protocol.WatchSnapshot, error) {
	canonical, item := d.resolveWI(wi)
	hubRead := item != nil || canonical == ""
	if canonical == "" {
		canonical = wi
	}
	if !d.holdsWI(canonical) {
		return nil, &api.Error{Code: api.ErrCoreNotFound, Detail: fmt.Sprintf("work item %s is not held by machine %s", canonical, d.cfg.MachineID)}
	}
	snap := d.watchSnapshot(canonical, item, hubRead, 0)
	return &snap, nil
}

// holdsWI reports whether THIS daemon actually holds the WI: a local job
// or a local queue entry. An aihub record alone never counts (it would
// make the holder-side "not held here" refusal unreachable).
func (d *Daemon) holdsWI(wi string) bool {
	if d.q != nil && d.q.IsPending(wi) {
		return true
	}
	if _, ok := d.rt.Info(wi); ok {
		return true
	}
	return false
}

func (d *Daemon) readArtifact(wi, rel string) (*api.ArtifactAnswer, error) {
	canonical, item := d.resolveWI(wi)
	if canonical == "" {
		canonical = wi
	}
	if item == nil {
		if it, err := d.hub.GetWorkItem(canonical); err == nil {
			item = it
			if it.ID != "" {
				canonical = it.ID
			}
		}
	}
	if !d.holdsWI(canonical) {
		return nil, &api.Error{Code: api.ErrCoreNotFound, Detail: fmt.Sprintf("work item %s is not held by machine %s", canonical, d.cfg.MachineID)}
	}
	dir := d.capsuleDirOf(canonical, item)
	if dir == "" {
		return nil, &api.Error{Code: api.ErrCoreNotFound, Detail: fmt.Sprintf("no capsule directory for work item %s", canonical)}
	}
	target, err := safeArtifactPath(dir, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, &api.Error{Code: api.ErrCoreNotFound, Detail: fmt.Sprintf("artifact %q not found", rel)}
	}
	if !info.Mode().IsRegular() {
		return nil, &api.Error{Code: api.ErrCoreBadInput, Detail: fmt.Sprintf("artifact %q is not a regular file", rel)}
	}
	if info.Size() > artifactMaxBytes {
		return nil, &api.Error{Code: api.ErrCoreTooLarge, Detail: fmt.Sprintf("artifact %q is %d bytes; the read bound is %d", rel, info.Size(), artifactMaxBytes)}
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	slug, status := "", ""
	if item != nil {
		slug, status = item.Slug, item.Status
	}
	return &api.ArtifactAnswer{WorkItem: canonical, Slug: slug, Status: status, Path: rel, Content: content}, nil
}

// safeArtifactPath validates a capsule-relative artifact path: relative,
// cleaned, no .., no absolute, resolved under <capsule>/artifacts/ and
// (when it exists) outside any symlink escape.
func safeArtifactPath(capsuleDir, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", &api.Error{Code: api.ErrCoreBadInput, Detail: "artifact path is required"}
	}
	if filepath.IsAbs(rel) {
		return "", &api.Error{Code: api.ErrCoreBadInput, Detail: "artifact path must be capsule-relative"}
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", &api.Error{Code: api.ErrCoreBadInput, Detail: "artifact path must stay under artifacts/ (no .. components)"}
	}
	root := filepath.Join(capsuleDir, "artifacts")
	target := filepath.Join(root, clean)
	if !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", &api.Error{Code: api.ErrCoreBadInput, Detail: "artifact path escapes artifacts/"}
	}
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", &api.Error{Code: api.ErrCoreNotFound, Detail: "capsule has no artifacts/ directory"}
	}
	targetEval, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", &api.Error{Code: api.ErrCoreNotFound, Detail: fmt.Sprintf("artifact %q not found", rel)}
	}
	if !strings.HasPrefix(targetEval, rootEval+string(filepath.Separator)) {
		return "", &api.Error{Code: api.ErrCoreBadInput, Detail: "artifact path follows a symlink outside artifacts/"}
	}
	return targetEval, nil
}

func (d *Daemon) capsuleDirOf(wi string, item *api.WorkItem) string {
	if d.rt != nil {
		if ji, ok := d.rt.Info(wi); ok && ji.CapsuleDir != "" {
			return ji.CapsuleDir
		}
	}
	if item != nil && d.cfg.Mapping != nil {
		if ws, err := d.cfg.Mapping.WorkspaceFor(item.ProjectName); err == nil {
			return filepath.Join(ws, "capsules", api.PathName(item.Slug, wi))
		}
	}
	return ""
}

func asAPIError(err error) *api.Error {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return &api.Error{Code: api.ErrDaemonInternal, Detail: err.Error()}
}
