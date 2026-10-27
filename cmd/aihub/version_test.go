package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAihubBinaryVersionFlag pins the --version flag on the aihub binary's
// only real entry surface: the built executable (cmd/aihub/main.go parses
// global flags directly in main). The goal (wi_DBOTR7Q6Kcr3) requires aihub
// to accept --version/-version, print the build-injected version string
// (ldflags -X, git tag at build) and exit 0.
func TestAihubBinaryVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("build+exec integration test")
	}
	bin := filepath.Join(t.TempDir(), "aihub-test")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/aihub failed: %v\n%s", err, out)
	}
	for _, spelling := range []string{"--version", "-version"} {
		t.Run(spelling, func(t *testing.T) {
			out, err := exec.Command(bin, spelling).CombinedOutput()
			got := strings.TrimSpace(string(out))
			if err != nil {
				t.Fatalf("aihub %s failed: %v; it must exit 0 and print the version.\noutput:\n%s",
					spelling, err, got)
			}
			if got == "" {
				t.Fatalf("aihub %s printed nothing; it must print the build-injected version string", spelling)
			}
			if strings.Contains(got, "flag provided but not defined") || strings.Contains(got, "Usage of") {
				t.Fatalf("aihub %s did not recognize the flag; it must print the version, not usage:\n%s",
					spelling, got)
			}
		})
	}
}
