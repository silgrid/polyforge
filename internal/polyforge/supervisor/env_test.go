package supervisor

import (
	"slices"
	"strings"
	"testing"
)

func TestDaemonEnvAllowList(t *testing.T) {
	in := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "https_proxy=x", "NO_PROXY=y", "POLYFORGE_DATA_DIR=/d",
		"ANTHROPIC_AUTH_TOKEN=junk", "FOO_TOKEN=junk", "POLYFORGE_V1_API_KEY=junk", "TMUX=/t", "CLAUDECODE=1",
		"PF_CHAT_ID=c", MarkerEnv + "=caller", "PATH=/dup", "malformed", "=x",
		"LC_$(touch X)=x", "LC_A B=x", "LC_X-Y=x", "TZ=", "GOPATH=/g", "GOMODCACHE=/m", "GOFLAGS=-mod=mod"}
	var names []string
	for _, kv := range DaemonEnv(in) {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	want := []string{"GOFLAGS", "GOMODCACHE", "GOPATH", "HOME", "LC_ALL", "NO_PROXY", "PATH", "POLYFORGE_DATA_DIR", "TZ", "https_proxy"}
	if !slices.Equal(names, want) {
		t.Fatalf("DaemonEnv names = %v, want %v", names, want)
	}
	if got := DaemonEnv(in); !slices.Contains(got, "PATH=/bin") || !slices.Contains(got, "TZ=") {
		t.Fatal("the first PATH entry must win and an empty value is kept")
	}
	for _, bad := range []string{"LC_$(touch X)", "LC_A B", "LC_X-Y", "", "1A"} {
		if DaemonEnvAllowed(bad) {
			t.Fatalf("a non-shell name %q is allowed", bad)
		}
	}
	for _, k := range []string{"GOPATH", "GOROOT", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GONOPROXY", "GOFLAGS", "GOTOOLCHAIN", "GO111MODULE", "GOCACHE", "GOMODCACHE"} {
		if !DaemonEnvAllowed(k) {
			t.Fatalf("the Go toolchain variable %s is not allowed", k)
		}
	}
}

func TestSuspectDaemonEnv(t *testing.T) {
	got := SuspectDaemonEnv([]string{"PATH", "HOME", "ANTHROPIC_MODEL", "anthropic_x", "FOO_TOKEN", "API_KEY", "MY_SECRET",
		"DB_PASSWORD", "KEYBOARD", "TOKENIZER_DIR", "POLYFORGE_SUPERVISOR", "FOO_TOKEN"})
	want := []string{"ANTHROPIC_MODEL", "API_KEY", "DB_PASSWORD", "FOO_TOKEN", "MY_SECRET", "anthropic_x"}
	if !slices.Equal(got, want) {
		t.Fatalf("SuspectDaemonEnv = %v, want %v", got, want)
	}
	if got := EnvNames([]string{"B=1", "A=2", "B=3"}); !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("EnvNames = %v", got)
	}
}
