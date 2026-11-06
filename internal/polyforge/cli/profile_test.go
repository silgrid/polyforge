package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// fakeProfilesHost serves GET /profiles with the given effective index on a
// unix socket and answers its path.
func fakeProfilesHost(t *testing.T, idx []cellcfg.IndexEntry) string {
	t.Helper()
	sock := filepath.Join(shortTempDir(t), "h.sock")
	lst, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /profiles", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ProfilesResponse{Index: idx})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(lst)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "pfp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func profileOf(t *testing.T, dir string, argv ...string) Resolution {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var stdout, stderr bytes.Buffer
	if code := Run(append(argv, "--json", "profile"), nil, &stdout, &stderr); code != 0 {
		t.Fatalf("pf %v profile: exit %d (%s)", argv, code, stderr.String())
	}
	var r Resolution
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("%v: %q", err, stdout.String())
	}
	return r
}

// TestProfileResolverOrder: --profile > PF_PROFILE > the unique cwd match in
// the Host's effective index > default.
func TestProfileResolverOrder(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a named Cell file exists (its effective mapping is the Host's answer)
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli-alpha.toml"), []byte("server = \"http://a:1\"\napi_key = \"k\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wsA, wsD, neutral := t.TempDir(), t.TempDir(), t.TempDir()
	sock := fakeProfilesHost(t, []cellcfg.IndexEntry{{Profile: "default", Workspace: wsD}, {Profile: "alpha", Workspace: wsA}})
	t.Setenv("PF_DAEMON_SOCKET", sock)
	t.Setenv("PF_PROFILE", "alpha")
	if r := profileOf(t, wsA, "--profile", "default"); r.Profile != "default" || r.Source != "flag" {
		t.Fatalf("flag must win: %+v", r)
	}
	if r := profileOf(t, wsD); r.Profile != "alpha" || r.Source != "env" {
		t.Fatalf("env must win over cwd: %+v", r)
	}
	t.Setenv("PF_PROFILE", "")
	if r := profileOf(t, filepath.Join(wsA)); r.Profile != "alpha" || r.Source != "cwd" {
		t.Fatalf("cwd must match alpha: %+v", r)
	}
	if r := profileOf(t, neutral); r.Profile != "default" || r.Source != "default" {
		t.Fatalf("an unmatched cwd is default: %+v", r)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--profile", "Bad_Name", "profile"}, nil, &stdout, &stderr); code == 0 {
		t.Fatalf("an invalid profile name was accepted: %s", stdout.String())
	}
}

// TestProfileResolverDiskSource: with no Host answering, the local verb
// matches the cwd against the files on disk and labels the source disk.
func TestProfileResolverDiskSource(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PF_DAEMON_SOCKET", filepath.Join(shortTempDir(t), "none.sock"))
	t.Setenv("PF_PROFILE", "")
	wsA := t.TempDir()
	dir := filepath.Join(xdg, "polyforge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli-alpha.toml"), []byte("server = \"http://a:1\"\napi_key = \"k\"\n\n[workspace.\""+wsA+"\"]\nprojects = [\"p\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := profileOf(t, wsA); r.Profile != "alpha" || r.Source != "disk" {
		t.Fatalf("disk source: %+v", r)
	}
}

// TestProfileRefusedInStepMode: a step agent never resolves a Cell itself.
func TestProfileRefusedInStepMode(t *testing.T) {
	t.Setenv("PF_WI_ID", "wi_x")
	t.Setenv("PF_DAEMON_ID", "m-x")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"profile"}, nil, &stdout, &stderr); code == 0 || !bytes.Contains(stderr.Bytes(), []byte("DAEMON-403")) {
		t.Fatalf("pf profile in a step: exit %d (%s)", code, stderr.String())
	}
}
