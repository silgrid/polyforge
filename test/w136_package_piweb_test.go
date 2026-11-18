package test

// w136_package_piweb_test.go (polyforge#136): the ONE Pi Web packaging
// path, shared by the formal release (.polyforge/release.sh) and the
// installer's development channel — the ticket's Q5=A ruling: only one
// implementation, so the two channels cannot drift. Pins:
//
//   - tools/package-pi-web.sh carries the packaging recipe the release
//     ran inline until now (export the released commit's pi-web/ tree,
//     npm ci, build with PI_WEB_RELEASE_VERSION, stamp the package
//     version, npm pack) and verifies the packed tarball's version —
//     and publishes nothing (no gh, no docker, no git push): the
//     channel that publishes is the caller's;
//   - .polyforge/release.sh calls the shared script and carries no
//     inline copy of the recipe any more (the second copy is the drift
//     this ticket closes).
//
// The behaviour itself is pinned where it always was: the release
// script's own suite (release_script_test.go) runs the whole flow
// through the shared script with its stub world — its expected values
// change with nothing here — and the dev channel's use of the script is
// pinned by TestW136InstallDevWithPiWeb.
//
// carries: tools/package-pi-web.sh, .polyforge/release.sh, test/w136_package_piweb_test.go

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW136PiWebPackagingShared(t *testing.T) {
	root := w136Root(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "tools", "package-pi-web.sh"))
	if err != nil {
		t.Fatalf("tools/package-pi-web.sh is missing: the formal release and the dev channel must share one packaging script (Q5=A): %v", err)
	}
	s := string(scriptBytes)
	// the packaging recipe, the steps the release ran inline until #136
	for _, want := range []string{
		"git archive",                 // the released commit's pi-web/ tree, exported
		"pi-web",                      // the exported subtree
		"npm ci",                      // the locked dependencies, in the export
		"PI_WEB_RELEASE_VERSION",      // the build-time version the package embeds
		"npm run build",               // the production build (the release path's)
		"--no-git-tag-version",        // the package version stamped without a commit
		"npm pack --pack-destination", // the tarball, packed
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("tools/package-pi-web.sh lacks %q: the shared recipe must carry the packaging steps the release ran, so the two channels cannot drift", want)
		}
	}
	// the packed tarball: silgrid-pi-web-<tag>.tgz into the out dir, its
	// version verified — a pack that lost the tag's version refuses
	if !strings.Contains(s, "silgrid-pi-web-$TAG.tgz") {
		t.Fatal("tools/package-pi-web.sh must write silgrid-pi-web-<tag>.tgz into the out dir it is given")
	}
	// the script publishes nothing: no gh, no docker, no push — the
	// caller owns the channel
	for _, forbidden := range []string{"gh ", "docker", "git push"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("tools/package-pi-web.sh contains %q: the packaging script never publishes — the release and the dev channel each own their channel", forbidden)
		}
	}

	// the release calls the shared script and keeps no inline copy
	relBytes, err := os.ReadFile(filepath.Join(root, ".polyforge", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	rel := string(relBytes)
	if !strings.Contains(rel, "tools/package-pi-web.sh") {
		t.Fatal(".polyforge/release.sh does not call tools/package-pi-web.sh: the release must use the one shared packaging path")
	}
	for _, leftover := range []string{"npm pack", "PI_WEB_RELEASE_VERSION", "npm ci"} {
		if strings.Contains(rel, leftover) {
			t.Fatalf(".polyforge/release.sh still carries the inline packaging step %q: the second copy is the drift the shared script closes", leftover)
		}
	}
}
