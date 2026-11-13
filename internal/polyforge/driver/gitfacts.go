package driver

// gitfacts.go: the driver's read-only git reads of the task repos (the
// heads a gate's round judged). The task repos are
// agent-controlled and agents are untrusted, so every read is the taskrepo
// package's hardened one (repo-local fsmonitor and hooks disabled on the
// command line, system config ignored, inherited GIT_* stripped, a hard
// timeout).

import (
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// taskRepoNames answers the task repos under reposDir (sorted).
func taskRepoNames(reposDir string) []string { return taskrepo.Names(reposDir) }

// repoGit runs one hardened git read in dir (taskrepo.Git).
func repoGit(dir string, args ...string) (string, error) { return taskrepo.Git(dir, args...) }

// repoHeads answers each task repo's HEAD (repos with an unborn or
// unreadable HEAD omitted).
func repoHeads(reposDir string) map[string]any {
	heads := map[string]any{}
	for _, name := range taskRepoNames(reposDir) {
		out, err := repoGit(filepath.Join(reposDir, name), "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
		if sha := strings.TrimSpace(out); err == nil && sha != "" {
			heads[name] = sha
		}
	}
	return heads
}
