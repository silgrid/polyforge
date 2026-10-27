package runtime

// procargs_test.go: synthetic-blob tests for the kern.procargs2 parser
// (polyforge#218). The parser is build-tag-free so these run on Linux,
// where the real sysctl caller (procinfo_darwin.go) cannot; the layout
// it parses is XNU's documented kern.procargs2 shape.

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// blob builds a kern.procargs2-shaped buffer: the native-endian argc,
// then the strings — the exec path first, then the argv strings, then
// the environment — each NUL-terminated.
func blob(path string, argv []string, env []string) []byte {
	out := make([]byte, 4, 64)
	argc := uint32(len(argv))
	binary.LittleEndian.PutUint32(out, argc)
	sections := append([]string{path}, argv...)
	sections = append(sections, env...)
	for _, s := range sections {
		out = append(out, s...)
		out = append(out, 0)
	}
	return out
}

func TestProcargsEnvironParsesMarkerTokens(t *testing.T) {
	raw := blob("/usr/bin/pf", []string{"pf", "daemon"}, []string{
		"PATH=/usr/bin",
		"PF_WI_ID=wi_abc",
		"PF_DAEMON_ID=m-host",
	})
	got := procargsEnviron(raw)
	want := []string{"PATH=/usr/bin", "PF_WI_ID=wi_abc", "PF_DAEMON_ID=m-host"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("procargsEnviron = %q, want %q", got, want)
	}
	if !envHasMarkers(got, "wi_abc", "m-host") {
		t.Fatal("envHasMarkers is false for the parsed blob's environment")
	}
	if envHasMarkers(got, "wi_other", "m-host") {
		t.Fatal("envHasMarkers matches a different WI")
	}
}

func TestProcargsEnvironSkipsExePathAndPadNULs(t *testing.T) {
	// XNU: the first string after argc is the exec path; pad NULs may
	// follow it before argv; trailing pad NULs end the environment.
	raw := []byte{}
	raw = binary.LittleEndian.AppendUint32(raw, 2)
	for _, s := range []string{"/usr/pf", "", "pf", "daemon", "PF_WI_ID=wi_x", "PF_DAEMON_ID=m", "", ""} {
		raw = append(raw, s...)
		raw = append(raw, 0)
	}
	got := procargsEnviron(raw)
	want := []string{"PF_WI_ID=wi_x", "PF_DAEMON_ID=m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("procargsEnviron = %q, want %q (exe path and pad NULs skipped)", got, want)
	}
}

func TestProcargsEnvironAfterArgvRewrite(t *testing.T) {
	// A live node process (pi) rewrites its exec-time argument block:
	// argv[0] survives at the head, the original argv+env strings are
	// zeroed, and the real environment reappears behind a stretch of
	// NULs with the kernel's platform strings (ptr_munge=, main_stack=)
	// after it. The parser must skip the pad NULs and still find the
	// markers; the exact-token consumers ignore the leftover
	// argv/platform strings (captured-blob regression, macOS 15/XNU:
	// step agents could never deliver results — the daemon's origin
	// guard saw no markers and refused /step/result with DAEMON-403).
	raw := []byte{}
	raw = binary.LittleEndian.AppendUint32(raw, 13)
	for _, s := range []string{"/usr/local/bin/node", "", "", "", ""} {
		raw = append(raw, s...)
		raw = append(raw, 0)
	}
	raw = append(raw, "pi"...)
	raw = append(raw, make([]byte, 0x100)...) // the zeroed original block
	for _, s := range []string{"SHELL=/bin/zsh", "PF_WI_ID=wi_r", "PF_DAEMON_ID=m", "PI_CODING_AGENT_DIR=/x"} {
		raw = append(raw, s...)
		raw = append(raw, 0)
	}
	raw = append(raw, make([]byte, 16)...)
	for _, s := range []string{"ptr_munge=", "main_stack="} {
		raw = append(raw, s...)
		raw = append(raw, 0)
	}
	envs := procargsEnviron(raw)
	if !envHasMarkers(envs, "wi_r", "m") {
		t.Fatalf("markers must survive an argv-rewritten blob: %q", envs)
	}
	if envHasMarkers(envs, "wi_other", "m") {
		t.Fatal("a rewritten blob must still match only its own markers")
	}
}

func TestProcargsEnvironTruncatedAndTinyBlobs(t *testing.T) {
	for name, raw := range map[string][]byte{
		"too short":  []byte{0, 1},
		"argc only":  {1, 0, 0, 0},
		"empty":      {},
		"huge argc":  {0xFF, 0xFF, 0xFF, 0x7F},
		"no env nul": []byte{1, 0, 0, 0, 'p', 'f', 0},
	} {
		if got := procargsEnviron(raw); len(got) != 0 {
			t.Fatalf("%s blob: procargsEnviron = %q, want nothing", name, got)
		}
	}
}

func TestProcargsEnvironFindsMarkersInRealisticOrder(t *testing.T) {
	// A step process's env among other variables: exact-token matching,
	// never substring (PF_WI_ID_X must not match PF_WI_ID).
	raw := blob("/bin/sh", []string{"sh"}, []string{
		"HOME=/root", "PF_WI_ID_PREFIX=noise", "PF_WI_ID=wi_z", "PF_DAEMON_ID=d1", "PF_DAEMON_ID_OTHER=x",
	})
	envs := procargsEnviron(raw)
	if !envHasMarkers(envs, "wi_z", "d1") {
		t.Fatalf("exact-token markers must match: %q", envs)
	}
	for _, bad := range [][2]string{{"wi_z_PREFIX", "d1"}, {"wi_z", "d1_OTHER"}} {
		if envHasMarkers(envs, bad[0], bad[1]) {
			t.Fatalf("substring marker %v must not match: %q", bad, envs)
		}
	}
}
