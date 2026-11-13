package cli

// w350_local_repair_env_test.go (D6): the local repair verbs
// (config, profile, doctor, init, migrate, version) run under an inherited
// PF_SERVER/PF_API_KEY with exactly one value-free stderr notice and their
// normal exit code; an explicit --server/--api-key is refused on every verb
// (version included), and every other verb keeps refusing the variables.

import (
	"bytes"
	"strings"
	"testing"
)

func w350RunEnv(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PF_SERVER", "http://inherited.invalid")
	t.Setenv("PF_API_KEY", "inherited-secret-value")
	var stdout, stderr bytes.Buffer
	code := Run(args, bytes.NewReader(nil), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestW350LocalRepairVerbsIgnoreInheritedEnv(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"config", "list"}, {"profile"}} {
		code, stdout, stderr := w350RunEnv(t, args...)
		if code != 0 {
			t.Fatalf("pf %v under an inherited PF_SERVER: exit %d (%q / %q)", args, code, stdout, stderr)
		}
		if n := strings.Count(stderr, "PF_SERVER"); n != 1 || !strings.Contains(stderr, "PF_API_KEY") {
			t.Fatalf("pf %v: want one notice naming both variables, got %q", args, stderr)
		}
		if strings.Contains(stdout+stderr, "inherited.invalid") || strings.Contains(stdout+stderr, "inherited-secret-value") {
			t.Fatalf("pf %v printed an inherited value: %q / %q", args, stdout, stderr)
		}
	}
}

func TestW350ExplicitFlagsAndOtherVerbsStillRefuse(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--server", "http://x.invalid", "version"}, "--server"},
		{[]string{"config", "list", "--api-key", "k"}, "--api-key"},
		{[]string{"cell", "list"}, "PF_SERVER"},
		{[]string{"projects", "list"}, "PF_API_KEY"},
	} {
		code, stdout, stderr := w350RunEnv(t, tc.args...)
		if code == 0 || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "refused") {
			t.Fatalf("pf %v must refuse naming %s: exit %d (%q / %q)", tc.args, tc.want, code, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "inherited-secret-value") {
			t.Fatalf("pf %v printed the inherited key: %q", tc.args, stderr)
		}
	}
}
