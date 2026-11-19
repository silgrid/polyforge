// seedtool is the workstation image's seed-manifest writer and
// verifier (polyforge#162): the build assembles the version directory
// (the release checkout, bin/pf, pi-web, the two harness packages) and
// seedtool writes its manifest — computing the component digests — or
// verifies an assembled seed against its manifest. It is a build tool,
// not a pf subcommand; the install transaction itself lives in the
// components package this tool fronts.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/polyforge/components"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "seedtool:", err)
		os.Exit(1)
	}
}

// run is main's testable body: args[0] names the command.
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: seedtool write --root DIR --version V --commit C --platform P --channel-repo O/N [--min-base M] [--pi-web-package DIR] --harness-pi-package DIR --harness-claude-package DIR | seedtool verify --root DIR")
	}
	switch args[0] {
	case "write":
		return cmdWrite(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	default:
		return fmt.Errorf("unknown command %q (write or verify)", args[0])
	}
}

// cmdWrite assembles the manifest of the version directory at --root
// and writes it as manifest.json. The four component categories must
// all be present — the tool refuses an incomplete seed before it
// computes anything — and the harness versions are read from the
// installed packages' own package.json files, never assumed.
func cmdWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	var (
		root                 = fs.String("root", "", "the assembled seed's version directory")
		version              = fs.String("version", "", "the product version (vX.Y.Z, vX.Y.Z-gmi.N or vX.Y.Z-dev.N)")
		commit               = fs.String("commit", "", "the source commit the seed was built from")
		platform             = fs.String("platform", "", "the seed's platform (os/arch, e.g. linux/amd64)")
		channelRepo          = fs.String("channel-repo", "", "the channel repository (owner/name)")
		minBase              = fs.String("min-base", "", "the minimum base requirement (default: --version — the base and the seed of one image are one build)")
		piWebPackage         = fs.String("pi-web-package", "", "the pi-web tree carrying its package.json (default: <root>/pi-web)")
		harnessPiPackage     = fs.String("harness-pi-package", "", "the installed pi harness package directory (its package.json carries the version)")
		harnessClaudePackage = fs.String("harness-claude-package", "", "the installed claude harness package directory")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	for name, v := range map[string]string{
		"--root":               *root,
		"--version":            *version,
		"--commit":             *commit,
		"--platform":           *platform,
		"--channel-repo":       *channelRepo,
		"--harness-pi-package": *harnessPiPackage,
	} {
		if v == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if *piWebPackage == "" {
		*piWebPackage = filepath.Join(*root, components.PathPiWeb)
	}
	if *minBase == "" {
		*minBase = *version
	}

	// the four component categories must be present and shaped — a
	// seed missing one of them never gets a manifest.
	if err := checkAssembled(*root); err != nil {
		return err
	}
	piWebVersion, err := packageVersion(*piWebPackage)
	if err != nil {
		return fmt.Errorf("pi-web's package: %w", err)
	}
	piHarnessVersion, err := packageVersion(*harnessPiPackage)
	if err != nil {
		return fmt.Errorf("the pi harness package: %w", err)
	}
	claudeHarnessVersion, err := packageVersion(*harnessClaudePackage)
	if err != nil {
		return fmt.Errorf("the claude harness package: %w", err)
	}

	m := &components.Manifest{
		SchemaVersion: components.SchemaVersion,
		Version:       *version,
		Commit:        *commit,
		Platform:      *platform,
		ChannelRepo:   *channelRepo,
		MinBase:       *minBase,
	}
	for _, name := range components.ComponentNames {
		path, _ := components.PathOfComponent(name)
		c := components.Component{Name: name, Path: path}
		switch name {
		case components.ComponentPF, components.ComponentRelease:
			c.Version = *version
		case components.ComponentPiWeb:
			c.Version = piWebVersion
		case components.ComponentHarnessPi:
			c.Version = piHarnessVersion
		case components.ComponentHarnessClaude:
			c.Version = claudeHarnessVersion
		}
		m.Components = append(m.Components, c)
	}
	if err := m.FillDigests(*root); err != nil {
		return fmt.Errorf("computing the component digests: %w", err)
	}
	if err := components.WriteManifest(*root, m); err != nil {
		return fmt.Errorf("writing the manifest: %w", err)
	}
	fmt.Printf("seedtool: wrote %s for %s (%s, %s): %s\n",
		filepath.Join(*root, components.ManifestFilename), *version, *commit, *platform, *channelRepo)
	return nil
}

// cmdVerify reads the manifest of the version directory at --root and
// verifies every component's digest against the tree — the build's own
// check of the assembled seed, and the same reader the install uses.
func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	root := fs.String("root", "", "the assembled seed's version directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	m, err := components.ReadManifest(*root)
	if err != nil {
		return err
	}
	if err := m.VerifyTree(*root); err != nil {
		return err
	}
	fmt.Printf("seedtool: %s verifies: %s (%s, %s)\n", *root, m.Version, m.Platform, m.Commit)
	return nil
}

// checkAssembled refuses a version directory that lacks one of the
// four component categories, naming the first thing missing.
func checkAssembled(root string) error {
	pf := filepath.Join(root, components.PathPF)
	fi, err := os.Stat(pf)
	if err != nil {
		return fmt.Errorf("the seed is incomplete: %s: %w", pf, err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("the seed is incomplete: %s is not an executable file", pf)
	}
	for _, dir := range []string{
		filepath.Join(root, "package.json"),
		filepath.Join(root, "skills"),
		filepath.Join(root, "workflows"),
		filepath.Join(root, components.PathPiWeb),
		filepath.Join(root, components.PathHarnessPi),
		filepath.Join(root, components.PathHarnessClaude),
	} {
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("the seed is incomplete: %s: %w", dir, err)
		}
	}
	return nil
}

// packageVersion answers the version field of the package.json in
// dir — the installed package's own version, never assumed.
func packageVersion(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", err
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", err
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("%s: the package carries no version", filepath.Join(dir, "package.json"))
	}
	return pkg.Version, nil
}
