// carries: dist/workstation/Containerfile, dist/workstation/build-seed.sh,
//
//	.dockerignore, dist/workstation/README.md, docs/workstation.md
package test

// w536_workstation_seed_test.go — polyforge#162 (84-components-A):
// the workstation image's component seed, the pinned facts. The
// image's final layer carries the Node runtime beside podman and the
// complete seed — the four component categories (pf, the release
// checkout, Pi Web, the two harnesses) — assembled by
// dist/workstation/build-seed.sh and pinned by the manifest the
// components package reads: product version, source commit, platform,
// channel repository and the harness packages' own versions. The
// version and TARGETARCH build arguments are the injection points the
// #86 release pipeline fills. Authored test-first against the ticket's
// settled decisions: every assertion's expectation comes from the
// ticket and the components package's manifest contract, never from
// the implementation's own output.

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/components"
)

// w536SeedArg answers the seed stage's default for one build argument
// (e.g. SEED_VERSION), from the Containerfile's ARG line.
func w536SeedArg(t *testing.T, name string) string {
	t.Helper()
	body := w535Deliverable(t, "dist/workstation/Containerfile")
	re := regexp.MustCompile(`(?m)^ARG ` + name + `=(\S+)`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("polyforge#162: the Containerfile must declare ARG %s=<default> (the #86 release pipeline's injection point)", name)
	}
	return m[1]
}

// w536Stage answers one build stage's body (the text after its FROM
// line up to the next FROM).
func w536Stage(t *testing.T, stage string) string {
	t.Helper()
	body := w535Deliverable(t, "dist/workstation/Containerfile")
	re := regexp.MustCompile(`(?s)FROM \S+ AS ` + stage + `\b(.*?)(?:\nFROM |\z)`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("polyforge#162: the Containerfile must have an %s stage (the seed assembly)", stage)
	}
	return m[1]
}

// TestWorkstationSeed — the static half: the seed's build facts, pinned
// with no engine.
func TestWorkstationSeed(t *testing.T) {
	t.Run("the final layer carries the Node runtime beside podman", func(t *testing.T) {
		final := w536Stage(t, "final") // the last stage: FROM ubuntu:24.04
		if !regexp.MustCompile(`(?m)^COPY --from=node /usr/local/ /usr/local/$`).MatchString(final) {
			t.Fatal("polyforge#162: the final layer must carry Node (COPY --from=node /usr/local/ /usr/local/): Pi Web and the two harness CLIs run on it, and the workstation boots without installing anything")
		}
		if !regexp.MustCompile(`(?m)^COPY --from=go /out/pf /usr/local/libexec/polyforge/pf$`).MatchString(final) {
			t.Fatal("polyforge#175: the base pf lives at /usr/local/libexec/polyforge/pf — the bootstrap the entry point execs by absolute path, never a PATH lookup")
		}
		// review r1#2: the base pf's move must not take pf off the image's
		// PATH — the in-container operator commands (`pf daemon ensure`,
		// `pf doctor`) and the smoke's docker exec resolve `pf` on PATH, and
		// the link targets the components volume's current, so an upgrade
		// that switches current is live on PATH too (dangling until the
		// first boot installs the volume, 84-components-C).
		if !regexp.MustCompile(`(?m)^RUN ln -s /components/current/bin/pf /usr/local/bin/pf$`).MatchString(final) {
			t.Fatal("polyforge#175 r1#2: the final layer must link /usr/local/bin/pf to the components volume's current pf — the image's PATH pf and the smoke's docker exec resolve it")
		}
	})

	t.Run("the seed stage assembles the four component categories", func(t *testing.T) {
		stage := w536Stage(t, "seed")
		for _, need := range []string{
			`COPY dist/workstation/build-seed.sh`, // the assembler
			`COPY --from=go /out/pf ./bin/pf`,     // category 1: pf
			`COPY package.json ./`,                // category 2: the release checkout
			`COPY skills ./skills/`,
			`COPY workflows ./workflows/`,
			`COPY pi-web ./pi-web/`, // category 3: Pi Web
			`workstation-build-seed.sh "$SEED_VERSION" "$SEED_COMMIT" "$SEED_CHANNEL_REPO" "$PI_HARNESS_VERSION" "$CLAUDE_HARNESS_VERSION" "$TARGETARCH"`, // categories 4+5: the harnesses, pinned
		} {
			if !strings.Contains(stage, need) {
				t.Errorf("polyforge#162: the seed stage must carry %q:\n%s", need, stage)
			}
		}
	})

	t.Run("the pf binary and the seed carry the same injected version", func(t *testing.T) {
		goStage := w536Stage(t, "go")
		if !strings.Contains(goStage, `internal/version.Version=${SEED_VERSION}`) {
			t.Error("polyforge#162: the go stage must stamp the pf binary with SEED_VERSION (ldflags -X internal/version.Version): the base's version answers the seed's minBase check")
		}
		if !strings.Contains(goStage, `internal/version.Commit=${SEED_COMMIT}`) {
			t.Error("polyforge#162: the go stage must stamp the pf binary's source commit (internal/version.Commit)")
		}
		if !strings.Contains(w536Stage(t, "seed"), `COPY --from=go /out/pf ./bin/pf`) {
			t.Error("polyforge#162: the seed's pf must be the same stamped build as the base's")
		}
	})

	t.Run("the seed lands read-only at /opt/polyforge/seed/releases/<ver>", func(t *testing.T) {
		final := w536Stage(t, "final")
		if !regexp.MustCompile(`(?m)^COPY --from=seed /seed /opt/polyforge/seed/releases/\$\{SEED_VERSION\}$`).MatchString(final) {
			t.Fatal("polyforge#162: the final layer must place the seed at /opt/polyforge/seed/releases/${SEED_VERSION} — the settled read-only seed path the first boot installs from")
		}
	})

	t.Run("the injection points exist and their defaults are legal", func(t *testing.T) {
		version := w536SeedArg(t, "SEED_VERSION")
		if _, err := components.ParseVersion(version); err != nil {
			t.Errorf("polyforge#162: the Containerfile's default SEED_VERSION %q is of no accepted form: %v (an illegal version can never be installed)", version, err)
		}
		if w536SeedArg(t, "SEED_COMMIT") == "" {
			t.Error("polyforge#162: SEED_COMMIT needs a default (a repo-root build context carries no .git; the release pipeline injects the real commit)")
		}
		w536SeedArg(t, "SEED_CHANNEL_REPO")
		if w536SeedArg(t, "PI_HARNESS_VERSION") == "" || w536SeedArg(t, "CLAUDE_HARNESS_VERSION") == "" {
			t.Error("polyforge#162: the two harness pins need defaults (the release pipeline may override them)")
		}
	})

	t.Run("build-seed.sh assembles, digests and verifies", func(t *testing.T) {
		body := w535Deliverable(t, "dist/workstation/build-seed.sh")
		for _, need := range []string{
			`npm ci`,                           // Pi Web's dependencies, at build time
			`npm run build`,                    // Pi Web's production build
			`npm prune --omit=dev`,             // no dev dependencies ship in the seed
			`npm install --prefix`,             // the harness packages, pinned
			`@earendil-works/pi-coding-agent@`, // the pi harness pin
			`@anthropic-ai/claude-code@`,       // the claude harness pin
			`seedtool write`,                   // the manifest: digests, versions, provenance
			`seedtool verify`,                  // the build's own check of the assembled seed
			`--commit "${commit}"`,             // the source commit rides the manifest (r1#2)
			`--channel-repo "${channel_repo}"`, // and so does the channel repository (r1#2)
			`dpkg --print-architecture`,        // the platform fallback when TARGETARCH is unset
		} {
			if !strings.Contains(body, need) {
				t.Errorf("polyforge#162: build-seed.sh must contain %q", need)
			}
		}
		// nothing installs at boot: the seed is ready-to-run.
		if strings.Count(body, "npm ci") < 1 || strings.Contains(body, "npm install -g") {
			t.Error("polyforge#162: build-seed.sh installs everything at build time, never globally and never at boot")
		}
	})

	t.Run("the build context carries the seed's inputs and still no bloat", func(t *testing.T) {
		ignore := w535Deliverable(t, ".dockerignore")
		// the seed's inputs are IN the context now.
		for _, needed := range []string{"pi-web", "skills", "workflows"} {
			if regexp.MustCompile(`(?m)^` + needed + `(\*|/)?$`).MatchString(ignore) {
				t.Errorf("polyforge#162: .dockerignore must NOT exclude %s — the seed's release checkout needs it", needed)
			}
		}
		// node_modules, build caches, the test trees and the credential
		// paths pi-web's own .dockerignore keeps out (review r1#N3) stay
		// OUT: a developer's local pi-web/.env.local must never ride
		// the context into the seed or the image.
		for _, heavy := range []string{
			"pi-web/node_modules", "pi-web/.next", "pi-web/e2e", "pi-web/.polyforge",
			"pi-web/.env", "pi-web/.env.*", "pi-web/.claude", "pi-web/workspace", "pi-web/coverage",
			"webui/node_modules", "webui/e2e", "webui/dist",
			".git", ".github", ".pi", ".polyforge", "docs", "test", "tools", "deploy",
		} {
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(heavy) + `(/|)$`).MatchString(ignore) {
				t.Errorf("polyforge#162: .dockerignore must exclude %s (no dependency caches, no test trees, no credentials in the build context)", heavy)
			}
		}
	})

	t.Run("the docs state the seed fact, not the old never-in-the-image claim", func(t *testing.T) {
		for _, doc := range []string{"dist/workstation/README.md", "docs/workstation.md"} {
			body := w535Deliverable(t, doc)
			// the OLD claim is "the image is only the base / carries only
			// the runtime": pin its absence, not the "never in the image"
			// phrase — the corrected docs may still say the RUNNING
			// versions never live in the image (which is the new fact).
			if regexp.MustCompile(`(?i)(is only the base|carries only the runtime)`).MatchString(body) {
				t.Errorf("polyforge#162: %s still claims the image is only the runtime — the image now carries the read-only seed (the 84-components-A change)", doc)
			}
			if !strings.Contains(body, "/opt/polyforge/seed") {
				t.Errorf("polyforge#162: %s must name the seed's settled path /opt/polyforge/seed", doc)
			}
			if !strings.Contains(body, "build-seed.sh") {
				t.Errorf("polyforge#162: %s must register build-seed.sh in its file table (a new file's incoming reference)", doc)
			}
		}
		// the README keeps the seccomp pin w535 reads.
		readme := w535Deliverable(t, "dist/workstation/README.md")
		if !strings.Contains(readme, "moby's") {
			t.Error("polyforge#161: dist/workstation/README.md must keep the seccomp profile's upstream origin (w535 reads it)")
		}
	})
}

// TestWorkstationSeedEngine — the real-engine half: with a reachable
// docker engine it builds the workstation image and reads the built
// seed back: the manifest validates through the install primitives'
// own reader, the four component categories are physically there, and
// the harness versions the manifest names are the packages the seed
// carries. Without an engine it skips, exactly like the other
// real-container suites.
func TestWorkstationSeedEngine(t *testing.T) {
	root := w535Root(t)
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Skipf("no docker engine (%v: %s), skipping the built-seed inspection", err, strings.TrimSpace(string(out)))
	}
	// the injected values are derived independently of the image's
	// defaults (review r1#2): the source commit is this tree's HEAD,
	// the version and the channel repository are legal non-defaults —
	// so a broken injection chain (a defaulted commit, an unwired
	// channel) cannot pass as a green build.
	version := "v0.2.5-dev.7"
	commitOut, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	commit := strings.TrimSpace(string(commitOut))
	channelRepo := "example/seed-test"

	build := exec.Command("docker", "build",
		"-f", "dist/workstation/Containerfile",
		"--build-arg", "SEED_VERSION="+version,
		"--build-arg", "SEED_COMMIT="+commit,
		"--build-arg", "SEED_CHANNEL_REPO="+channelRepo,
		"-t", "polyforge-162-seed-test",
		".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the workstation image build failed: %v\n%s", err, tailN(string(out), 40))
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", "polyforge-162-seed-test").Run()
	})

	seedDir := "/opt/polyforge/seed/releases/" + version

	// the manifest, read by the install primitives' own contract.
	manifestRaw, err := dockerRun(t, "cat", seedDir+"/manifest.json")
	if err != nil {
		t.Fatalf("the built seed's manifest: %v", err)
	}
	var m components.Manifest
	if err := json.Unmarshal([]byte(manifestRaw), &m); err != nil {
		t.Fatalf("the built seed's manifest is not JSON: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("the built seed's manifest does not satisfy the install's contract: %v", err)
	}
	if m.Version != version {
		t.Errorf("the manifest's version = %q, want the injected %q verbatim", m.Version, version)
	}
	if m.Commit != commit {
		t.Errorf("the manifest's commit = %q, want the injected source commit %q verbatim", m.Commit, commit)
	}
	if m.ChannelRepo != channelRepo {
		t.Errorf("the manifest's channelRepo = %q, want the injected %q verbatim", m.ChannelRepo, channelRepo)
	}
	archOut, err := exec.Command("docker", "version", "--format", "{{.Server.Arch}}").Output()
	if err != nil {
		t.Fatalf("the engine's architecture: %v", err)
	}
	if want := "linux/" + strings.TrimSpace(string(archOut)); m.Platform != want {
		t.Errorf("the manifest's platform = %q, want the engine's own %q", m.Platform, want)
	}
	for _, name := range components.ComponentNames {
		if m.Component(name) == nil {
			t.Errorf("the manifest lacks the %s component", name)
		}
	}

	// the four categories, physically present in the built seed.
	if out, err := dockerRun(t, "test", "-x", seedDir+"/bin/pf"); err != nil {
		t.Errorf("the seed's pf binary is missing or not executable (%v: %s)", err, out)
	}
	for _, dir := range []string{
		seedDir + "/package.json",
		seedDir + "/skills",
		seedDir + "/workflows",
		seedDir + "/pi-web/package.json",
		seedDir + "/pi-web/node_modules",
		seedDir + "/harness/pi/node_modules/@earendil-works/pi-coding-agent/package.json",
		seedDir + "/harness/claude/node_modules/@anthropic-ai/claude-code/package.json",
	} {
		if _, err := dockerRun(t, "test", "-e", dir); err != nil {
			t.Errorf("the built seed lacks %s", dir)
		}
	}

	// the harness versions the manifest names are the packages' own.
	for name, pkgDir := range map[string]string{
		"harness-pi":     seedDir + "/harness/pi/node_modules/@earendil-works/pi-coding-agent/package.json",
		"harness-claude": seedDir + "/harness/claude/node_modules/@anthropic-ai/claude-code/package.json",
	} {
		raw, err := dockerRun(t, "cat", pkgDir)
		if err != nil {
			t.Fatalf("the %s package's manifest: %v", name, err)
		}
		var pkg struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
			t.Fatalf("the %s package.json is not JSON: %v", name, err)
		}
		c := m.Component(name)
		if c == nil {
			t.Errorf("the manifest lacks the %s component", name)
			continue
		}
		if c.Version != pkg.Version {
			t.Errorf("the manifest's %s version (%s) is not the package's own (%s)", name, c.Version, pkg.Version)
		}
	}
}

// dockerRun answers one command's output inside the built seed image.
// The entrypoint is overridden: the image's own PID 1 chain initializes
// the cgroup namespace and hands the machine to `pf supervise`, which
// is not what an inspection run wants.
func dockerRun(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"run", "--rm", "--entrypoint", name, "polyforge-162-seed-test"}, args...)
	cmd := exec.Command("docker", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tailN answers the last n lines of s, for failure messages.
func tailN(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
