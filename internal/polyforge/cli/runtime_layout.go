package cli

// runtime_layout.go — the machine's run mode and component-root
// resolution (polyforge#175, 84-components-B). One convention, two
// layouts: the workstation image resolves every "current" through the
// components volume (/components/current — the install transaction's
// single commit point, 84-components-A), a host through the release
// checkout tools/install.sh maintains (<data-dir>/release/current).
// The mode is judged by ONE fact (owner 2026-11-19, cross #175/#177):
// the seed manifest the image's final layer carries —
// /opt/polyforge/seed/releases/<ver>/manifest.json — exists. Nothing
// probes /components: a directory someone left there is not a mode, and
// the pre-#162 layout is never bridged.

import (
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/polyforge/components"
)

// workstationSeedRoot is the image's read-only seed root (the final
// layer's /opt/polyforge/seed). A package var so the tests inject a
// fixture; production never changes it.
var workstationSeedRoot = "/opt/polyforge/seed"

// componentsVolumeRoot is the workstation's components volume: the root
// whose current/ symlink every consumer in the image resolves.
const componentsVolumeRoot = "/components"

// workstationImage answers whether this machine is the workstation
// image: the seed (with its manifest) that the image's final layer
// carries is there. The one fact CLI, doctor and Pi Web judge the mode
// by — a components directory alone never does.
func workstationImage() bool {
	matches, err := filepath.Glob(filepath.Join(workstationSeedRoot, components.ReleasesDirname, "*", components.ManifestFilename))
	return err == nil && len(matches) > 0
}

// currentRoot answers the root every consumer resolves current
// through: the image's components volume (/components/current), or the
// host installer's release checkout (<data-dir>/release/current).
func currentRoot(dataDir string) string {
	if workstationImage() {
		return filepath.Join(componentsVolumeRoot, components.CurrentLinkname)
	}
	return filepath.Join(dataDir, "release", components.CurrentLinkname)
}

// currentPfBin answers the pf binary the supervised children launch:
// the components volume's current pf in the image, as a LITERAL path —
// exec resolves the current symlink afresh at every start, so an
// upgrade that switches current is live at the next relaunch
// (os.Executable would resolve the symlink once and keep running the
// old version after the switch). On a host (where no documented flow
// runs `pf supervise`) the answer is the running binary.
func currentPfBin() (string, error) {
	if workstationImage() {
		return filepath.Join(componentsVolumeRoot, components.CurrentLinkname, components.PathPF), nil
	}
	return os.Executable()
}
