package cellcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"http://Hub.example:8420/":   "http://hub.example:8420",
		"http://hub.example":         "http://hub.example:80",
		"https://hub.example/api/v1": "https://hub.example:443",
	} {
		got, err := NormalizeServer(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "hub.example", "ftp://x"} {
		if _, err := NormalizeServer(bad); err == nil {
			t.Errorf("NormalizeServer(%q) accepted", bad)
		}
	}
}

func TestConflictsAreLoadOrderIndependent(t *testing.T) {
	def := &Party{Profile: "default", Server: "http://a:1"}
	alpha := Party{Profile: "alpha", Server: "http://shared:2/"}
	beta := Party{Profile: "beta", Server: "http://SHARED:2"}
	gamma := Party{Profile: "gamma", Server: "http://c:3"}
	for _, order := range [][]Party{{alpha, beta, gamma}, {gamma, beta, alpha}} {
		got := Conflicts(def, order)
		if got["alpha"] == "" || got["beta"] == "" {
			t.Fatalf("both conflicting Cells must be invalid: %v", got)
		}
		if _, bad := got["gamma"]; bad {
			t.Fatalf("an unrelated Cell was invalidated: %v", got)
		}
	}
	got := Conflicts(def, []Party{{Profile: "x", Server: "http://A:1/"}})
	if got["x"] == "" {
		t.Fatalf("a named Cell sharing the default's server must be invalid: %v", got)
	}
}

func TestWorkspaceOverlapResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	def := &Party{Profile: "default", Server: "http://a:1", Workspaces: []string{ws}}
	got := Conflicts(def, []Party{
		{Profile: "alias", Server: "http://b:1", Workspaces: []string{link}},
		{Profile: "child", Server: "http://c:1", Workspaces: []string{filepath.Join(link, "sub")}},
		{Profile: "free", Server: "http://d:1", Workspaces: []string{t.TempDir()}},
	})
	if got["alias"] == "" || got["child"] == "" {
		t.Fatalf("a symlink alias and a descendant must both conflict: %v", got)
	}
	if got["free"] != "" {
		t.Fatalf("a disjoint workspace was invalidated: %v", got)
	}
}
