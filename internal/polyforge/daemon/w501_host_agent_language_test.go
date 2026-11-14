package daemon

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

func TestW501HostNamedCellInheritsAndReloadsDefaultAgentLanguage(t *testing.T) {
	th := newTestHost(t,
		hostCell{profile: "default", machineID: "m-def", agentLanguage: "zh-CN"},
		hostCell{profile: "alpha", machineID: "m-alpha"},
	)
	alpha := th.cell("alpha")
	def := th.cell("default")
	if alpha == nil || alpha.d == nil || def == nil || def.d == nil {
		t.Fatal("both test Cells must be serving")
	}
	if got := alpha.d.live.AgentLanguage(); got != "zh-CN" {
		t.Fatalf("named Cell inherited language = %q, want zh-CN", got)
	}
	// An explicit named value wins over the default Cell.
	alpha.d.live.SetAgentLanguage("en")
	if got := alpha.d.live.AgentLanguage(); got != "en" {
		t.Fatalf("explicit named Cell language = %q, want en", got)
	}
	alpha.d.live.SetAgentLanguage("")

	current := "zh-CN"
	def.d.cfg.LoadFile = func() (cli.Config, error) {
		next := def.d.cfg.StartupFile
		next.Server = def.d.cfg.Server
		next.APIKey = def.d.cfg.APIKey
		next.Mapping = def.d.cfg.Mapping
		next.Daemon.AgentLanguage = current
		return next, nil
	}
	current = "en"
	if _, err := def.d.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if got := alpha.d.live.AgentLanguage(); got != "en" {
		t.Fatalf("named Cell language after default reload = %q, want en", got)
	}
}
