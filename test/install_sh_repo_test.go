package test

// tools/install.sh takes its release channel from PF_INSTALL_REPO (unset
// or empty = the internal release repo every asset is published to,
// — the tools/install.sh twin of the deploy.sh
// fix) and prints the channel it uses in step 1.
//
// Pinned, on the hermetic w174 install harness with its gh stub wrapped
// to journal every call: every repo-dependent gh call (latest-release
// pick, asset + SHA256SUMS download, release-checkout clone) names the
// channel repo and no other, the pi old-entry match replaces the channel
// repo's git entry while a foreign repo's entry is kept, and the script's
// own default line and header name the internal repo. The internal literal
// stays on the REWRITE path (the ruling): in the public
// projection REWRITE maps both repo constants to one value, so the
// "never the public mirror" checks are guarded by their inequality.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// installShInternalRepo is the internal release repo every asset is
	// published to — the default the installer must use.
	installShInternalRepo = "silgrid/polyforge"
	// installShPublicRepo is the public mirror the old default named.
	installShPublicRepo = "silgrid/polyforge"
)

func TestInstallShRepoReleaseChannel(t *testing.T) {
	const fork, foreign = "acme/fork", "acme/ops-lib"
	for _, tc := range []struct {
		name, env, want string
		never           []string // repos no gh call may name
	}{
		{"default", "", installShInternalRepo, []string{installShPublicRepo, fork}},
		{"empty", "PF_INSTALL_REPO=", installShInternalRepo, []string{installShPublicRepo, fork}},
		{"override", "PF_INSTALL_REPO=" + fork, fork, []string{installShInternalRepo, installShPublicRepo}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := w174NewInstall(t)
			in.release(t, "v0.0.1-chan")
			if tc.env != "" {
				in.extraEnv = append(in.extraEnv, tc.env)
			}
			// journal every gh call, then serve it from the w174 stub
			stub := filepath.Join(in.shim, "gh-stub")
			if err := os.Rename(filepath.Join(in.shim, "gh"), stub); err != nil {
				t.Fatal(err)
			}
			wrap := "#!/bin/sh\necho \"gh $*\" >> \"$W174_JOURNAL\"\nexec " + stub + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(in.shim, "gh"), []byte(wrap), 0o755); err != nil {
				t.Fatal(err)
			}
			piSettings := filepath.Join(in.home, ".pi", "agent", "settings.json")
			oldPi := `{"packages":[{"source":"git:github.com/` + tc.want + `@v2.18.0"},{"source":"git:github.com/` + foreign + `@v1.0.0"}]}`
			if err := os.WriteFile(piSettings, []byte(oldPi), 0o600); err != nil {
				t.Fatal(err)
			}

			out, code := in.run(t)
			if code != 0 {
				t.Fatalf("install.sh: exit %d\n%s", code, out)
			}
			if !strings.Contains(out, "   release channel: "+tc.want+"\n") {
				t.Errorf("step 1 must print the channel %s:\n%s", tc.want, out)
			}
			var gh []string
			for _, l := range in.journalLines() {
				if strings.HasPrefix(l, "gh ") {
					gh = append(gh, l)
				}
			}
			journal := strings.Join(gh, "\n")
			for _, want := range []string{
				"gh release list --repo " + tc.want + " ",
				"gh release download v0.0.1-chan --repo " + tc.want + " --pattern pf-linux-amd64 ",
				"gh release download v0.0.1-chan --repo " + tc.want + " --pattern SHA256SUMS ",
				"gh repo clone " + tc.want + " ",
			} {
				if journalIndex(gh, want) < 0 {
					t.Errorf("no gh call starting %q; gh calls:\n%s", want, journal)
				}
			}
			for _, other := range tc.never {
				// in the public projection the two repo constants are one
				// value: only a repo distinct from the channel is checked
				if other != tc.want && strings.Contains(journal, other) {
					t.Errorf("a gh call names %s, want only %s; gh calls:\n%s", other, tc.want, journal)
				}
			}
			var pi struct {
				Packages []any `json:"packages"`
			}
			if err := json.Unmarshal([]byte(w174ReadFile(t, piSettings)), &pi); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(pi.Packages)
			relCur := filepath.Join(in.data, "release", "current")
			wantPi, _ := json.Marshal([]any{map[string]any{"source": relCur}, map[string]any{"source": "git:github.com/" + foreign + "@v1.0.0"}})
			if string(got) != string(wantPi) {
				t.Errorf("pi packages = %s, want %s (the %s entry replaced, the %s one kept)", got, wantPi, tc.want, foreign)
			}
		})
	}
	src, err := os.ReadFile(filepath.Join("..", "tools", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`REPO="${PF_INSTALL_REPO:-` + installShInternalRepo + `}"`,
		"# The release channel is PF_INSTALL_REPO (owner/name; unset or empty =\n# " + installShInternalRepo + ",",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("tools/install.sh must carry %q (the internal default and its documented channel)", want)
		}
	}
}
