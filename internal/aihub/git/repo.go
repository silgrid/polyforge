// Package git is the aihub git surface: per-WI bare-repo lifecycle,
// the git-http-backend smart-protocol proxy, session-scoped credential
// auth (fencing-on-use, reference.md §3.4) and the pre-receive hook
// that enforces the capsule-chain invariants.
//
// All git access goes through the real git binary (no embedded library).
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// CredentialChecker validates a Basic pair (username = WI id) against
// the WI's live ownership: the password must be the current session
// secret. Implemented by *core.OwnerManager.CheckSessionCredential.
type CredentialChecker interface {
	CheckSessionCredential(wiID, username, secret string) error
}

// Manager owns the bare-repo tree <data>/git/wi_xxx.git.
type Manager struct {
	DataDir      string
	Root         string // <data>/git
	MaxBlobBytes int64  // pre-receive blob limit (default capsule.MaxBlobBytes)
	// MaxTaskBlobBytes is the blob limit for the refs/pf-task/* carrier
	// refs (default capsule.MaxTaskBlobBytes).
	MaxTaskBlobBytes int64

	checker CredentialChecker

	mu    sync.Mutex
	known map[string]bool // WIs whose bare repo + hook we installed
}

// NewManager creates the manager and its git root directory.
func NewManager(dataDir string, checker CredentialChecker) (*Manager, error) {
	root := filepath.Join(dataDir, "git")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("git: create git root: %w", err)
	}
	return &Manager{
		DataDir:          dataDir,
		Root:             root,
		MaxBlobBytes:     capsule.MaxBlobBytes,
		MaxTaskBlobBytes: capsule.MaxTaskBlobBytes,
		checker:          checker,
		known:            map[string]bool{},
	}, nil
}

// RepoDir is the bare-repo path for a work item.
func (m *Manager) RepoDir(wiID string) string {
	return filepath.Join(m.Root, wiID+".git")
}

// git runs git once with the given environment.
func git(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

// EnsureBare creates the bare repo for a WI (idempotent) and installs the
// pre-receive hook. Called at claim time — the WI's chain begins empty.
// Every step re-runs on a repo the process has not ensured yet (a
// pre-existing one after a restart, or one a crash left half-initialized):
// each is idempotent, so the repo always ends with this build's
// configuration and hook.
func (m *Manager) EnsureBare(wiID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.known[wiID] {
		return nil
	}
	dir := m.RepoDir(wiID)
	if _, err := git(m.Root, nil, "init", "--bare", dir); err != nil {
		return err
	}
	if _, err := git(dir, nil, "config", "core.hooksPath", "hooks"); err != nil {
		return err
	}
	// git's protocol rule: a server that does not advertise push options
	// REJECTS any push that carries them. Transition: daemons older than
	// this hub still send --push-option=session-fingerprint=<fp> (ignored);
	// delete this once no such daemon is deployed.
	if _, err := git(dir, nil, "config", "receive.advertisePushOptions", "true"); err != nil {
		return err
	}
	// The carrier lane pushes each task repo's WI-owned commits with the
	// upstream history cut off (a shallow push): without this the receive
	// side refuses every such push ("shallow update not allowed"), so the
	// hub setting is part of the shallow-carrier contract (workbench#39).
	if _, err := git(dir, nil, "config", "receive.shallowUpdate", "true"); err != nil {
		return err
	}
	if _, err := git(dir, nil, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return err
	}
	if err := writeHook(filepath.Join(dir, "hooks", "pre-receive"), m.MaxBlobBytes, m.MaxTaskBlobBytes); err != nil {
		return err
	}
	m.known[wiID] = true
	return nil
}
