// Package cellcfg is the pure model of a DaemonHost and its ProfileCells:
// the profile-name syntax, the Host/Cell key partition, the per-Cell file
// naming, the conflict rules between Cells and the per-Cell data layout.
// It parses no TOML itself (the cli package owns the cli.toml grammar) and
// starts nothing: the daemon and the CLI assemble their behavior from it.
//
// One Host file (`cli.toml`) carries the Host keys and the `default`
// Cell's keys; one `cli-<profile>.toml` per named Cell carries that Cell's
// keys only. A Cell is a unit of state isolation, never a sandbox: every
// Cell runs inside the one Host process.
package cellcfg

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultProfile is the reserved name of the Cell the Host file defines.
const DefaultProfile = "default"

// HostFileName is the Host file's base name.
const HostFileName = "cli.toml"

// InstancesDirName is the retired multi-daemon configuration directory: it
// is not a configuration form and is refused by name.
const InstancesDirName = "instances"

var profileRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidName checks a profile name's syntax. `default` is a valid name (the
// Host file's Cell); use ValidNamedProfile for a name that must come from a
// cli-<profile>.toml file or a lifecycle request.
func ValidName(name string) error {
	if !profileRE.MatchString(name) {
		return fmt.Errorf("profile %q is not a valid name: it must match [a-z0-9][a-z0-9-]{0,31}", name)
	}
	return nil
}

// ValidNamedProfile checks a named Cell's profile: the syntax, and the
// reserved `default` (its keys live in cli.toml; cli-default.toml is refused).
func ValidNamedProfile(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if name == DefaultProfile {
		return fmt.Errorf("profile %q is reserved for the Host file %s (cli-%s.toml is refused)", name, HostFileName, DefaultProfile)
	}
	return nil
}

// CellFileName answers the file a profile's keys live in: cli.toml for the
// default Cell, cli-<profile>.toml for a named one.
func CellFileName(profile string) string {
	if profile == DefaultProfile || profile == "" {
		return HostFileName
	}
	return "cli-" + profile + ".toml"
}

// UnknownProfileDetail is the one refusal text for a profile no Cell is
// configured under — the Host's router (daemon/cells.go) and the CLI's
// placement validation (cli/profile.go) share it, so a name the Host
// would refuse DAEMON-404 is refused the same way before anything is
// sent. cfgDir is the configuration directory the Cell file is expected
// in: the Host's loaded set directory, the CLI's Host-file directory.
func UnknownProfileDetail(profile, cfgDir string) string {
	return fmt.Sprintf(
		"unknown profile %q: no Cell is configured under that name — expected %s (create it with `pf cell add %s …` or write that file and run `pf --profile %s config reload`)",
		profile, filepath.Join(cfgDir, CellFileName(profile)), profile, profile)
}

// ProfileOfFile answers the profile a base file name names (ok=false: not
// a Cell file at all). The name is answered even when it is not a valid
// profile, so the caller can report the file as invalid rather than skip it.
func ProfileOfFile(base string) (string, bool) {
	if base == HostFileName {
		return DefaultProfile, true
	}
	if !strings.HasPrefix(base, "cli-") || !strings.HasSuffix(base, ".toml") {
		return "", false
	}
	p := strings.TrimSuffix(strings.TrimPrefix(base, "cli-"), ".toml")
	if p == "" {
		return "", false
	}
	return p, true
}
