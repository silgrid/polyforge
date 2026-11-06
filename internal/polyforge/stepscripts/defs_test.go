package stepscripts

// defs_test.go: the shapes these black-box tests read the workflow's step
// scripts by — the result file (schema pf-step-result/1, published as
// workflows/_shared/scripts/schema/pf-step-result-1.json), the container
// labels and defaults the scripts' container library uses
// (workflows/_shared/scripts/pfstep/container.py), and the few texts the
// scripts write that a test compares. They are the scripts' contract as a
// reader sees it; nothing here runs a step.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Schema is the result file's schema tag.
const Schema = "pf-step-result/1"

// The verdicts, by exit code: 0 pass, 1 fail, 2 error (not judged, or the
// invocation unusable), 3 partial (gate.py --mark-green: judged, the tag
// not landed).
const (
	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictError   = "error"
	VerdictPartial = "partial"
)

// Result is a script's result file.
type Result struct {
	Schema    string            `json:"schema"`
	Command   string            `json:"command"`
	Version   string            `json:"version"`
	Argv      []string          `json:"argv"`
	Round     string            `json:"round"`
	StartedAt string            `json:"started_at"`
	EndedAt   string            `json:"ended_at"`
	ExitCode  int               `json:"exit_code"`
	Verdict   string            `json:"verdict"`
	Heads     map[string]string `json:"heads"`
	Summary   string            `json:"summary"`
	Details   any               `json:"details"`
	Logs      []string          `json:"logs"`
}

// WriteResult lands a result file a test hands a script (an earlier
// step's), the parent directory created.
func WriteResult(path string, r *Result) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// The labels every verification container a script starts carries.
const (
	LabelDaemon  = "pf.verify.daemon"
	LabelBoot    = "pf.verify.boot"
	LabelWI      = "pf.verify.wi"
	LabelRun     = "pf.verify.run"
	LabelRepo    = "pf.verify.repo"
	LabelExpires = "pf.verify.expires"
)

// StepOwner is the pf.verify.daemon label of a container a script (other
// than gate.py) starts.
const StepOwner = "pf-step"

// DockerfileVerify is the file a repo's verification container is built
// from.
const DockerfileVerify = ".polyforge/Dockerfile.verify"

// KeepAliveGrace is how long a container outlives its run's deadline.
const KeepAliveGrace = 5 * time.Minute

// DefaultDeadline is a script's deadline outside a step (no tool window,
// no shell-call maximum in its environment).
const DefaultDeadline = 25 * time.Minute

// cacheVolume is a repo's named cache volume for the image of dockerfile
// (repo already tag-safe): the image tag's hash is the volume's.
func cacheVolume(repo, dockerfile string) string {
	sum := sha256.Sum256([]byte(dockerfile))
	return "pf-verify-cache-" + repo + "-" + hex.EncodeToString(sum[:16])
}

// containerSpec is one container a script started, as the fake docker's
// log records its `docker run`.
type containerSpec struct {
	Name   string
	Labels map[string]string
	Binds  []bind
}

// bind is one bind mount of a container.
type bind struct{ Path string }

// planLine is one entry of an evidence plan: a line run in a repo.
type planLine struct {
	Repo string `json:"repo"`
	Line string `json:"line"`
}

// logsGitignore is the .gitignore a script writes into its log directory.
const logsGitignore = "# pf step command logs are local, never capsule chain content\n*\n"

// orText answers s, else alt when s is blank.
func orText(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// laneIdentity is the identity the scripts commit and tag as (a green
// tag's tagger).
const laneIdentity = "polyforge daemon <daemon@polyforge.invalid>"
