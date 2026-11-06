package driver

// gitworld_test.go: the git fixtures of the driver's tests (git without
// the machine's config, a fixed identity).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// emptyGitConfig answers an empty file under t.TempDir() for
// GIT_CONFIG_GLOBAL: git reads no global config, and the fixture never
// depends on the null device being intact.
func emptyGitConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "empty.gitconfig")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tgit runs git in dir for test fixtures: no global/system config, a
// fixed identity.
func tgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+emptyGitConfig(t), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func dwrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
