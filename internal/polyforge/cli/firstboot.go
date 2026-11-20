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
// choice (local or external) is made exactly once. A cell with no
// record at all, or a done record without the cell, is an inconsistency
// the boot refuses to start on, with the recovery hint naming both
// files; a cell with an IN-PROGRESS record is this boot's own
// interruption and is resumed — with the RECORD as the authority for
// the choice (owner ruling B on review r1#1): the environment only
// ever gets to agree with the record or refuse loudly, never to
// re-decide.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/silgrid/polyforge/internal/aihub"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/components"
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
	// The gate is the settled Q6 with the owner's ruling of 2026-11-20
	// (reply "b" to review r1#1) on the one shape Q6's letter left
	// undecided — a default Cell with an IN-PROGRESS record (this
	// boot's own interruption: a crash between the config write and the
	// done record):
	//   - done record + the default Cell: the boot skips (the first
	//     choice is made exactly once);
	//   - a cell with no record at all, or a done record without the
	//     cell: an inconsistency the boot refuses to start on, naming
	//     both files (a hand-deleted half never re-initializes
	//     silently — Q6's own rationale);
	//   - an IN-PROGRESS record: the boot RESUMES, and the record — not
	//     the environment — is the authority for the choice: the mode,
	//     the server and the project come from the record, environment
	//     inputs that disagree with it refuse the start, and the
	//     external record's key input must be set again (a record never
	//     carries a credential).
	rec, recExists, err := readFirstBootRecord(o.DataDir)
	if err != nil {
		return err
	}
	done := recExists && rec.Phase == firstBootPhaseDone
	hasCell, err := configHasDefaultCell(o.ConfigPath)
	if err != nil {
		return err
	}
	record, config := firstBootPath(o.DataDir), o.ConfigPath
	switch {
	case done && hasCell:
		fmt.Fprintf(stdout, "first boot: already done (%s says done and %s carries the default Cell) — nothing to do\n", record, config)
		return nil
	case done && !hasCell:
		return fmt.Errorf("first boot: %s says done but %s carries no default Cell — refusing to start on the inconsistency; recovery: complete the default Cell (server and api_key) in %s by hand, or remove %s and redo the first boot from scratch", record, config, config, record)
	case hasCell && !recExists:
		return fmt.Errorf("first boot: %s carries a default Cell but %s records no first boot at all — refusing to start on the inconsistency; recovery: remove the default Cell from %s and run the first boot again, or complete the first boot by hand and write the done record %s", config, record, config, record)
	}

	st := firstBootState{
		Mode:        "local",
		DefaultCell: firstBootDefaultCell,
		Server:      firstBootLocalServer,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if recExists {
		// the resume: the record is the authority (ruling B)
		st.Mode, st.Server, st.Project = rec.Mode, rec.Server, rec.Project
		if (st.Mode != "local" && st.Mode != "external") || st.Server == "" {
			return fmt.Errorf("first boot: the in-progress record %s names no mode and server — remove it and the default Cell in %s to redo the first boot from scratch", record, config)
		}
		switch {
		case o.ExtServer == "" && o.ExtKey == "":
			// no external inputs: consistent with a local record; an
			// external one needs its key below
		case o.ExtServer == "" || o.ExtKey == "":
			return fmt.Errorf("first boot: resuming with %s set without %s (or the reverse) — set both or neither; the in-progress record already chose %s and never re-decides", EnvWorkstationAihubKey, EnvWorkstationAihubServer, st.Mode)
		case st.Mode == "local" || o.ExtServer != st.Server:
			return fmt.Errorf("first boot: the environment names %s but the in-progress record already chose %s — the first choice is made exactly once; unset %s and %s to resume as recorded, or remove %s and the default Cell in %s to redo the first boot", o.ExtServer, st.Server, EnvWorkstationAihubServer, EnvWorkstationAihubKey, record, config)
		}
		if st.Mode == "external" && o.ExtKey == "" {
			return fmt.Errorf("first boot: the in-progress record is an external boot but %s is unset — the record never carries the key; set it back (its value is already in %s) or remove %s and the default Cell to redo the first boot", EnvWorkstationAihubKey, config, record)
		}
	} else {
		// the fresh boot (Q4): a server means external (its key with
		// it, an optional already-existing project for the mapping);
		// no server means local — the identity transaction and the
		// loopback Cell.
		switch {
		case o.ExtServer == "" && o.ExtKey == "":
		case o.ExtServer == "":
			return fmt.Errorf("first boot: %s is set without %s — a key with no server to point at is not a mode; set both or neither", EnvWorkstationAihubKey, EnvWorkstationAihubServer)
		case o.ExtKey == "":
			return fmt.Errorf("first boot: %s is set without %s — the external mode needs the key that goes with the server; set both or neither (the local mode needs nothing)", EnvWorkstationAihubServer, EnvWorkstationAihubKey)
		default:
			st.Mode, st.Server = "external", o.ExtServer
			if o.ExtProject != "" {
				st.Project = o.ExtProject
			}
		}
	}

	var (
		apiKey, userID string
		identityDone   bool
	)
	stages := []struct {
		name string
		run  func() error
	}{
		{
			name: firstBootPhaseComponents,
			run: func() error {
				managed, err := firstBootInstallSeed(o, stdout)
				if err != nil {
					return err
				}
				st.ImageManaged = managed
				return nil
			},
		},
		{
			name: firstBootPhaseIdentity,
			run: func() error {
				if st.Mode == "external" {
					// nothing is created anywhere: the identity stays the
					// remote's own (userReady stays false — no local user
					// was verified), the key is the one the operator gave
					apiKey = o.ExtKey
					return nil
				}
				res, err := aihub.InitLocal(aihub.InitOptions{DataDir: o.AihubDataDir})
				if err != nil {
					return err
				}
				apiKey, userID = res.PlainKey, res.UserID
				identityDone = true
				return nil
			},
		},
		{
			name: firstBootPhaseConfig,
			run: func() error {
				if err := firstBootWriteConfig(o, st.Mode, apiKey, userID, st.Project, o.WorkspaceDir); err != nil {
					return err
				}
				// keyReady only after the fact is verified (Q7): the
				// written file loads and carries exactly this boot's key
				cfg, err := LoadFileConfig(o.ConfigPath)
				if err != nil {
					return err
				}
				if cfg.APIKey != apiKey {
					return fmt.Errorf("the written default Cell does not carry this boot's key — refusing to record keyReady")
				}
				st.KeyReady = true
				if identityDone {
					st.UserReady = true
				}
				return nil
			},
		},
		{
			name: firstBootPhaseHarness,
			run: func() error {
				rep, err := mergeHarness(harnessMergeOpts{
					Home: o.Home, AgentDir: o.AgentDir,
					ComponentsRoot: o.ComponentsRoot, RepoSlug: o.RepoSlug,
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(stdout, "first boot: harness references: pi packages %s; %d skill link(s) newly, %d already, %d the user's own untouched\n",
					rep.PiPackages, len(rep.SkillsLinked), len(rep.SkillsKept), len(rep.SkillsUntouched))
				return nil
			},
		},
	}
	for _, stage := range stages {
		st.Phase = stage.name
		st.Error = ""
		if err := publishFirstBoot(o.DataDir, &st); err != nil {
			return err
		}
		if err := stage.run(); err != nil {
			// the record names the stage and the reason — never a
			// credential (none of the stage errors carries one)
			st.Phase = stage.name + firstBootPhaseFailed
			st.Error = err.Error()
			if perr := publishFirstBoot(o.DataDir, &st); perr != nil {
				return fmt.Errorf("%v (and publishing the failure record: %v)", err, perr)
			}
			return fmt.Errorf("first boot: the %s stage: %w", stage.name, err)
		}
	}
	st.Phase = firstBootPhaseDone
	if err := publishFirstBoot(o.DataDir, &st); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "first boot: done (%s, %s)\n", st.Mode, st.Server)
	return nil
}

// readFirstBootRecord reads the boot's record (exists=false when
// absent or unparsable — a corrupt record is recreated by the run).
func readFirstBootRecord(dataDir string) (firstBootState, bool, error) {
	raw, err := os.ReadFile(firstBootPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return firstBootState{}, false, nil
	}
	if err != nil {
		return firstBootState{}, false, err
	}
	var st firstBootState
	if err := json.Unmarshal(raw, &st); err != nil {
		return firstBootState{}, false, nil
	}
	return st, true, nil
}

// configHasDefaultCell answers whether cli.toml already carries the
// default Cell's connection (server and api_key).
func configHasDefaultCell(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	parsed, err := parseConfigTOML(path, string(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return parsed.flat["server"] != "" && parsed.flat["api_key"] != "", nil
}

// firstBootInstallSeed installs the image's seed into the components
// volume when there is one (the image-managed fact is exactly the
// seed's presence); without a seed the boot is not image-managed and
// the install is skipped — the harness stage will refuse on a volume
// with no current rather than leave dangling references.
func firstBootInstallSeed(o firstBootOpts, stdout io.Writer) (bool, error) {
	releases := filepath.Join(o.SeedRoot, components.ReleasesDirname)
	entries, err := os.ReadDir(releases)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "first boot: no seed under %s — not image-managed, the seed install is skipped\n", o.SeedRoot)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// the seed with the highest version wins (the image carries one;
	// a future image may stage the newer beside the older)
	best, bestVer := "", (components.Version{})
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(releases, e.Name())
		if _, err := os.Stat(filepath.Join(dir, components.ManifestFilename)); err != nil {
			continue
		}
		v, err := components.ParseVersion(e.Name())
		if err != nil {
			return false, fmt.Errorf("the seed %s: %w", dir, err)
		}
		if best == "" {
			best, bestVer = dir, v
			continue
		}
		c, err := bestVer.Compare(v)
		if err != nil {
			return false, err
		}
		if c < 0 {
			best, bestVer = dir, v
		}
	}
	if best == "" {
		fmt.Fprintf(stdout, "first boot: no seed under %s — not image-managed, the seed install is skipped\n", releases)
		return false, nil
	}
	if _, err := components.Install(components.Options{
		Root:        o.ComponentsRoot,
		Seed:        best,
		DataDir:     o.DataDir,
		BaseVersion: o.BaseVersion,
		Platform:    o.Platform,
	}); err != nil {
		return false, err
	}
	fmt.Fprintf(stdout, "first boot: the seed %s is installed\n", best)
	return true, nil
}

// firstBootWriteConfig writes the default Cell into cli.toml, under
// the config writer's own flock and atomic write, preserving every
// other row the file carries (the editor replaces only its own keys).
func firstBootWriteConfig(o firstBootOpts, mode, apiKey, userID, project, workspaceDir string) error {
	lock, err := lockConfigFile(o.ConfigPath)
	if err != nil {
		return err
	}
	defer unlockConfigFile(lock)
	raw, err := os.ReadFile(o.ConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	c := newConfigLines(string(raw))
	if mode == "external" {
		c.setFlat("server", o.ExtServer)
	} else {
		c.setFlat("server", firstBootLocalServer)
	}
	c.setFlat("api_key", apiKey)
	if mode == "local" && userID != "" {
		c.setFlat("user_id", userID)
	}
	c.setDaemon("container_engine", strconv.Quote("podman"))
	if mode == "local" || project != "" {
		name := project
		if mode == "local" {
			name = aihub.InitProjectName
		}
		c.setWorkspace(workspaceDir, []string{name})
	}
	proposed := []byte(c.String())
	// the proposed bytes parse before they land: the boot never writes
	// a file the shared loader would refuse
	if _, err := parseConfigTOML(o.ConfigPath, string(proposed)); err != nil {
		return fmt.Errorf("the proposed cli.toml does not parse: %w", err)
	}
	return writeConfigAtomic(o.ConfigPath, proposed)
}

// publishFirstBoot lands the record atomically (Q7: each phase's
// publish is one atomic write; the record never carries a
// credential).
func publishFirstBoot(dataDir string, st *firstBootState) error {
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(firstBootPath(dataDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicfile.Write(firstBootPath(dataDir), raw, 0o600)
}
