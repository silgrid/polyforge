package version

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionDefault pins the un-injected default: a plain `go build` (no
// ldflags) must still yield a non-empty version string, so `pf --version`
// and `aihub --version` never print nothing.
func TestVersionDefault(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must never be the empty string; un-injected builds default to \"dev\"")
	}
}

// TestLdflagsInjection pins the release plumbing itself: building cmd/pf
// with -ldflags "-X .../internal/version.Version=<tag>" must stamp the
// tag into the binary, and `pf --version` must print exactly it. This is
// the ldflags -X injection `make release` performs with the git tag.
func TestLdflagsInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("build+exec integration test")
	}
	const injected = "v9.9.9-inject-test"
	bin := filepath.Join(t.TempDir(), "pf-inject-test")
	out, err := exec.Command("go", "build",
		"-ldflags", "-X github.com/silgrid/polyforge/internal/version.Version="+injected,
		"-o", bin, "github.com/silgrid/polyforge/cmd/pf").CombinedOutput()
	if err != nil {
		t.Fatalf("go build with -ldflags -X failed: %v\n%s", err, out)
	}
	got, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("injected pf --version exited non-zero: %v\n%s", err, got)
	}
	if strings.TrimSpace(string(got)) != injected {
		t.Fatalf("pf --version after ldflags injection printed %q, want %q", got, injected)
	}
}
