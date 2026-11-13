// Package taskrepo is polyforge's one way to start git (Command) and reads
// a capsule's task repos with it: the task repos under a repos/ directory
// (Names) and one hardened read in one of them (Git). A task repo is
// agent-controlled, so no git polyforge runs starts a program the
// repository's own config names: fsmonitor, hooks, ssh command and external
// diff are pinned on the command line, which outranks every config file.
// Git adds the read's own rules: system config ignored, inherited GIT_*
// stripped, a hard timeout. The driver's and the daemon's reads of the task
// repos use Git (it was the facts package's, whose computations moved to
// the workflow's facts.py); the step scripts' pfstep/git.py
// pins only the fsmonitor and the hooks.
//
// Reachability: no daemon, no aihub, no credential; the git on PATH.
package taskrepo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// readTimeout bounds one git read.
const readTimeout = 10 * time.Second

// hardening is the argv every git starts with: each program a repository's
// config can name is pinned to none (or to git's own default ssh). An empty
// diff.external makes a patch-form git diff fail closed instead of running
// anything; a caller that needs the patch passes --no-ext-diff.
var hardening = []string{
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.sshCommand=ssh",
	"-c", "diff.external=",
}

// Command answers one hardened git command in dir under ctx (not started).
// env is the command's environment (nil = Env(nil)); git is the first
// executable regular file named git in an absolute directory of env's PATH
// (its last PATH= entry), else the git on the process's PATH.
func Command(ctx context.Context, env []string, dir string, args ...string) *exec.Cmd {
	if env == nil {
		env = Env(nil)
	}
	cmd := exec.CommandContext(ctx, lookPath("git", env), append(slices.Clone(hardening), args...)...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// Env answers base (nil = os.Environ()) minus every GIT_* and LC_ALL, plus
// GIT_CONFIG_NOSYSTEM=1, GIT_TERMINAL_PROMPT=0 and LC_ALL=C.
func Env(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+3)
	for _, kv := range base {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

// lookPath answers the first executable regular file named name in an
// absolute directory of env's PATH (its last PATH= entry, as exec reads a
// duplicated name), else name.
func lookPath(name string, env []string) string {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, d := range filepath.SplitList(path) {
		if !filepath.IsAbs(d) {
			continue
		}
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return name
}

// Git runs one hardened git read in dir and answers its stdout (with the
// error naming git's stderr when it fails).
func Git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	cmd := Command(ctx, nil, dir, append([]string{"--no-optional-locks", "--literal-pathspecs"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// Names answers the task repos under reposDir (sorted): every directory
// not starting with a dot.
func Names(reposDir string) []string {
	ents, err := os.ReadDir(reposDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
