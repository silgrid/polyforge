package cli

// config_validate.go: the one shared daemon-acceptance validator, used by
// `pf config` before it writes, by `pf config validate`, by the daemon's
// reload path and (through ValidateMappingForDaemon) by the daemon's
// startup. A file that fails here is never written / never applied — a
// refused write leaves the on-disk bytes untouched.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// LoadFileConfig parses a cli.toml WITHOUT environment or flag overlays:
// the file's own values (defaults for anything absent). A missing file is
// not an error (the defaults apply, with an empty mapping).
func LoadFileConfig(path string) (Config, error) {
	cfg := Config{
		Server:       defaultServer,
		DaemonSocket: defaultDaemonSocket(),
		Mapping:      &mapping.Plan{},
	}
	if path == "" {
		path = DefaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if err := applyConfigBytes(&cfg, path, data); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyConfigBytes parses file bytes into cfg (flat keys + [workspace]
// mapping + [daemon] table), sharing the exact parser LoadConfig uses.
func applyConfigBytes(cfg *Config, path string, data []byte) error {
	flat, sections, dkvs, perr := parseConfigTOML(path, string(data))
	if perr != nil {
		var uk *unknownKeyError
		if errors.As(perr, &uk) {
			return perr
		}
		return fmt.Errorf("config %s: %w", path, perr)
	}
	applyConfigFile(cfg, flat)
	var err error
	if cfg.Daemon, err = parseDaemonTable(path, dkvs); err != nil {
		return err
	}
	entries := make([]mapping.Entry, 0, len(sections))
	for _, s := range sections {
		entries = append(entries, mapping.Entry{Workspace: s.key, Projects: s.projects})
	}
	plan, berr := mapping.Build(entries)
	if berr != nil {
		return fmt.Errorf("config %s: %w", path, berr)
	}
	cfg.Mapping = plan
	return nil
}

// ParseConfigBytes parses an in-memory cli.toml (used for the proposed
// bytes of a set/unset before anything is written).
func ParseConfigBytes(path string, data []byte) (Config, error) {
	cfg := Config{
		Server:       defaultServer,
		DaemonSocket: defaultDaemonSocket(),
		Mapping:      &mapping.Plan{},
	}
	if err := applyConfigBytes(&cfg, path, data); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ValidateMappingForDaemon is the mapping/workspace half of the shared
// daemon-acceptance validator: a config carries a non-empty mapping and
// every mapped workspace is usable. Daemon startup (daemon.New),
// `pf config` and `pf doctor` all share it, so the workspace contract has
// exactly one implementation.
func ValidateMappingForDaemon(cfg Config) error {
	if cfg.Mapping == nil || cfg.Mapping.Empty() {
		return fmt.Errorf("no [workspace] mapping is configured — add at least one [workspace.\"<path>\"] section with a projects list")
	}
	for _, ws := range cfg.Mapping.Workspaces() {
		if err := ValidateWorkspaceRoot(ws); err != nil {
			return err
		}
	}
	return nil
}

// ValidateConfigForDaemon is the shared validator: a config is
// daemon-acceptable when it carries a non-empty mapping, every mapped
// workspace is a usable directory (capsules/ creatable and writable) and
// an explicitly configured operator-agent dir carries a readable
// auth.json.
func ValidateConfigForDaemon(cfg Config) error {
	if err := ValidateMappingForDaemon(cfg); err != nil {
		return err
	}
	return validateOperatorAgentDir(cfg)
}

// ValidateConfigWrite is the write-time validator `pf config set|unset`
// runs (owner ruling on review finding 9): the mapping-non-empty and
// workspace-usability checks apply only when the write touches the mapping
// (`touchesMapping`, a [workspace] set/unset) or the resulting file
// already carries a mapping. A flat/[daemon] write to a mapping-less file
// is accepted; the caller notes that the daemon refuses to start until a
// mapping exists. The operator-agent-dir check always applies.
func ValidateConfigWrite(cfg Config, touchesMapping bool) error {
	if touchesMapping || !cfg.Mapping.Empty() {
		if err := ValidateMappingForDaemon(cfg); err != nil {
			return err
		}
	}
	return validateOperatorAgentDir(cfg)
}

// validateOperatorAgentDir checks an explicitly configured operator-agent
// dir carries a readable auth.json.
func validateOperatorAgentDir(cfg Config) error {
	if dir := cfg.Daemon.OperatorAgentDir; dir != "" {
		auth := filepath.Join(dir, "auth.json")
		if _, err := os.Stat(auth); err != nil {
			return fmt.Errorf("operator_agent_dir %s: %s is not readable: %w", dir, auth, err)
		}
	}
	return nil
}

// ValidateWorkspaceRoot is the one workspace-usability check the daemon
// startup and `pf config` share: the workspace must exist, be a
// directory, and have a writable capsules/ subtree.
func ValidateWorkspaceRoot(ws string) error {
	st, err := os.Stat(ws)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", ws, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("workspace %s is not a directory", ws)
	}
	capsules := filepath.Join(ws, "capsules")
	if err := os.MkdirAll(capsules, 0o755); err != nil {
		return fmt.Errorf("workspace %s: capsules/ cannot be created: %w", ws, err)
	}
	probe, err := os.CreateTemp(capsules, ".write-probe-*")
	if err != nil {
		return fmt.Errorf("workspace %s: capsules/ is not writable: %w", ws, err)
	}
	name := probe.Name()
	probe.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("workspace %s: capsules/ write probe residue: %w", ws, err)
	}
	return nil
}
