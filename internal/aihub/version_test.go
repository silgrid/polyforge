package aihub

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPfAihubVersionFlag pins the --version flag on the server's only real
// entry surface: the built pf executable, which intercepts `pf aihub`
// before the CLI dispatch (cmd/pf/main.go → Main). The output is the
// build-injected version string (ldflags -X, the git tag at build) and one
// newline, byte for byte: deploy.sh and the rollback checks read it.
func TestPfAihubVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("build+exec integration test")
	}
	bin := filepath.Join(t.TempDir(), "pf-test")
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags", "-X github.com/silgrid/polyforge/internal/version.Version=v9.9.9", "./cmd/pf")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/pf failed: %v\n%s", err, out)
	}
	for _, spelling := range []string{"--version", "-version"} {
		t.Run(spelling, func(t *testing.T) {
			cmd := exec.Command(bin, "aihub", spelling)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("pf aihub %s failed: %v; it must exit 0 and print the version", spelling, err)
			}
			if string(out) != "v9.9.9\n" {
				t.Fatalf("pf aihub %s stdout = %q, want exactly %q", spelling, out, "v9.9.9\n")
			}
		})
	}
}
