package cli

// doctor_harnesses.go (polyforge#142, 85a2 AC2): the per-Cell structured
// harnesses facts of `pf --json doctor`'s top-level `cells` object. Every
// Cell of the disk set — the default Cell and each named one, an invalid
// Cell included — is probed the same way (the probe is no longer
// claude-steps-conditional), and the daemon's answer is relayed whole:
// set/unset facts, the model-credential source and its kind, the config
// roots and the Cell's execution-disabled reason. A daemon that does not
// answer (down, or too old for ?harnesses=1) leaves an honest probe_error,
// never a faked fact. The human report does not print this object; the
// account page (pi-web) and `pf --json doctor` callers read it.

import (
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// doctorCellHarnesses is one Cell's entry of the --json `cells` object:
// the daemon's structured harnesses facts of THAT Cell (per
// GET /status?harnesses=1 with the Cell's profile header) plus the
// execution-plane reason. Values never appear — the daemon answers
// set/unset, and the typed relay here drops every undeclared field.
type doctorCellHarnesses struct {
	Harnesses         map[string]HarnessStatus `json:"harnesses,omitempty"`
	ExecutionDisabled string                   `json:"execution_disabled,omitempty"`
	ProbeError        string                   `json:"probe_error,omitempty"`
}

// doctorCellHarnessFacts probes one Cell's daemon: its structured
// harnesses facts and its execution-disabled reason. A daemon that does
// not answer answers its probe error.
func doctorCellHarnessFacts(sock, profile string) doctorCellHarnesses {
	st, err := daemonStatusProfile(sock, 15*time.Second, true, profile)
	if err != nil {
		return doctorCellHarnesses{ProbeError: err.Error()}
	}
	return doctorCellHarnesses{Harnesses: st.Harnesses, ExecutionDisabled: st.ExecutionDisabled}
}

// doctorCellsHarnessFacts answers every Cell of the disk set: the default
// Cell plus each named one (an invalid Cell names its parse refusal as the
// probe error). Every read dials the socket the invocation resolved, like
// every other daemon read of the report.
func doctorCellsHarnessFacts(cfg Config, set *HostSet) map[string]doctorCellHarnesses {
	cells := map[string]doctorCellHarnesses{
		cellcfg.DefaultProfile: doctorCellHarnessFacts(cfg.DaemonSocket, ""),
	}
	for _, cc := range set.Cells {
		if cc.Invalid != "" {
			cells[cc.Profile] = doctorCellHarnesses{ProbeError: cc.Invalid}
			continue
		}
		cells[cc.Profile] = doctorCellHarnessFacts(cfg.DaemonSocket, cc.Profile)
	}
	return cells
}
