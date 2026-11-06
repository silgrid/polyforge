package test

// toolchain_pin_test.go: the repository pins its Go toolchain to one exact
// patch release in two places, and they agree.
//
//   - .polyforge/Dockerfile.verify `FROM golang:<version>-bookworm AS go`:
//     the toolchain the verification bar and the dependency-audit gate
//     (workflows/_shared/scripts/audit-deps, govulncheck) run with — govulncheck reports the
//     standard-library advisories of exactly that release;
//   - go.mod `toolchain go<version>`: with the default GOTOOLCHAIN=auto a host
//     whose go is older switches to it, so the release binaries (`make
//     release`, run on the release host, not in the verify image) and CI
//     (actions/setup-go reads only go.mod's `go` line) are built by the
//     release the audit judged.
//
// A bump of one without the other would ship binaries built by a toolchain
// the audit never judged (GO-2026-6859: the audit ran on go1.26.10 while
// go.mod named no toolchain at all).
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestToolchainPins' -count=1

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var (
	toolchainDockerGo = regexp.MustCompile(`(?m)^FROM golang:(1\.\d+\.\d+)-bookworm AS go$`)
	toolchainGoModGo  = regexp.MustCompile(`(?m)^go (\d+\.\d+(?:\.\d+)?)$`)
	toolchainGoModTC  = regexp.MustCompile(`(?m)^toolchain go(1\.\d+\.\d+)$`)
)

func TestToolchainPinsAgree(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, ".polyforge", "Dockerfile.verify"))
	if err != nil {
		t.Fatalf("the repository commits its verify image: %v", err)
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	img := toolchainDockerGo.FindAllSubmatch(dockerfile, -1)
	if len(img) != 1 {
		t.Fatalf(".polyforge/Dockerfile.verify must take Go from exactly one `FROM golang:<1.N.P>-bookworm AS go` stage (an exact patch release), found %d", len(img))
	}
	tc := toolchainGoModTC.FindAllSubmatch(gomod, -1)
	if len(tc) != 1 {
		t.Fatalf("go.mod must carry exactly one `toolchain go<1.N.P>` line (an exact patch release), found %d", len(tc))
	}
	if got, want := string(tc[0][1]), string(img[0][1]); got != want {
		t.Fatalf("go.mod names toolchain go%s but .polyforge/Dockerfile.verify runs the bar and the audit on golang:%s: bump both together", got, want)
	}
	lang := toolchainGoModGo.FindSubmatch(gomod)
	if lang == nil {
		t.Fatal("go.mod carries no `go` line")
	}
	if re := regexp.MustCompile(`^` + regexp.QuoteMeta(string(lang[1])) + `(\.|$)`); !re.MatchString(string(tc[0][1])) {
		t.Fatalf("go.mod's toolchain go%s is not a release of its language version go %s", tc[0][1], lang[1])
	}
}
