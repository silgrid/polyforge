package cli

// firstboot.go — polyforge#176 (84-components-C): the workstation's
// first boot, `pf first-boot`. The entrypoint runs it exactly once per
// container start, BEFORE `pf supervise` (the identity must exist
// before the server starts, so the server's own fresh-install bootstrap
// never fires). Everything it writes sits on the persistent volumes:
// the components volume (the seed install), the AIHub volume (the
// identity), pf-home (cli.toml and the harness references) and the
// data volume (the result record).
//
// The phases and their record are the ticket's settled Q7: each stage
// publishes <data-dir>/workstation/first-boot.json atomically — the
// phase it entered, and on failure the phase as <stage>/failed with the
// reason (never a credential) — so a later unit (D, the ops surface)
// and a human can see exactly where a boot stopped. Every stage is
// idempotent, so an interrupted first boot is retried by running the
// whole verb again: the seed install is a same-version noop, the
// identity transaction finds its user by the fixed email and reissues
// the same-named key (Q2), and the config and harness merges replace
// their own rows in place.
//
// The settled Q6 gate runs first: a first boot whose record says done
// AND whose cli.toml carries the default Cell is skipped — the first
// choice (local or external) is made exactly once; either half alone is
// an inconsistency the boot refuses to start on, with the recovery
// hint naming both files.

import (
	"errors"
	"io"
)

// The first-boot record's phases, in order.
const (
	firstBootPhaseComponents = "components"
	firstBootPhaseIdentity   = "identity"
	firstBootPhaseConfig     = "config"
	firstBootPhaseHarness    = "harness"
	firstBootPhaseDone       = "done"
	firstBootPhaseFailed     = "/failed"

	// firstBootLocalServer is the workstation's local AIHub address:
	// the aihub server child listens on 8420 (pf aihub's own default)
	// and the loopback is the only interface a default Cell needs.
	firstBootLocalServer = "http://127.0.0.1:8420"

	// firstBootDefaultCell is the name of the Cell the first boot
	// writes: cli.toml's root IS the default Cell.
	firstBootDefaultCell = "default"
)

// The external mode's input channels (compose carries them; the
// entrypoint's whitelist passes the family through): the server the
// workstation points its default Cell at, its key, and the optional
// already-existing project the /workspace mapping names (Q4: absent
// means no mapping — nothing is created on the remote).
const (
	EnvWorkstationAihubServer  = "WORKSTATION_AIHUB_SERVER"
	EnvWorkstationAihubKey     = "WORKSTATION_AIHUB_KEY"
	EnvWorkstationAihubProject = "WORKSTATION_AIHUB_PROJECT"
)

// firstBootOpts is one first boot's inputs. The paths carry the
// container's canonical defaults; the tests inject their own.
type firstBootOpts struct {
	// Home is $HOME.
	Home string
	// ConfigPath is cli.toml (the default Cell's file).
	ConfigPath string
	// DataDir is the Host data dir (the polyforge-data volume).
	DataDir string
	// AihubDataDir is the AIHub data dir (the AIHub volume).
	AihubDataDir string
	// ComponentsRoot is the components volume's root.
	ComponentsRoot string
	// WorkspaceDir is the workspace volume's mount (the mapping's key).
	WorkspaceDir string
	// SeedRoot is the image's read-only seed root (/opt/polyforge/seed);
	// a root with no releases/ version is a not-image-managed run.
	SeedRoot string
	// AgentDir is the pi agent dir (PI_CODING_AGENT_DIR).
	AgentDir string
	// RepoSlug names this repository (the harness merge's own-entry
	// identity).
	RepoSlug string
	// BaseVersion is the running base's version (internal/version).
	BaseVersion string
	// Platform is the running platform (os/arch).
	Platform string
	// ExtServer, ExtKey and ExtProject carry the external mode's
	// inputs; an empty ExtServer is the local mode.
	ExtServer, ExtKey, ExtProject string
}

// firstBootState is <data-dir>/workstation/first-boot.json: the boot's
// record. It NEVER carries a credential — no key, no hash, no token:
// the key lives only in cli.toml (0600).
type firstBootState struct {
	Phase        string `json:"phase"`
	Mode         string `json:"mode"`
	DefaultCell  string `json:"defaultCell"`
	Server       string `json:"server"`
	Project      string `json:"project,omitempty"`
	UserReady    bool   `json:"userReady"`
	KeyReady     bool   `json:"keyReady"`
	ImageManaged bool   `json:"imageManaged"`
	Error        string `json:"error,omitempty"`
	UpdatedAt    string `json:"updatedAt"`
}

// firstBootPath answers the record's location.
func firstBootPath(dataDir string) string {
	return dataDir + "/workstation/first-boot.json"
}

// runFirstBoot runs the workstation's first boot: the Q6 gate, then
// the components, identity, config and harness stages, publishing the
// record at every phase transition and at a failure.
func runFirstBoot(o firstBootOpts, stdout io.Writer) error {
	_ = o
	_ = stdout
	return errors.New("first boot: not implemented yet")
}
