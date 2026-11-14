package daemon

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

func TestW501HostNamedCellInheritsAndReloadsDefaultAgentLanguage(t *testing.T) {
	t.Run("inherit and reload", func(t *testing.T) {
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
	})

	t.Run("explicit named value wins", func(t *testing.T) {
		th := newTestHost(t,
			hostCell{profile: "default", machineID: "m-def", agentLanguage: "zh-CN"},
			hostCell{profile: "alpha", machineID: "m-alpha", agentLanguage: "en"},
		)
		alpha := th.cell("alpha")
		if alpha == nil || alpha.d == nil {
			t.Fatal("explicit named Cell must be serving")
		}
		if got := alpha.d.live.AgentLanguage(); got != "en" {
			t.Fatalf("explicit named Cell language = %q, want en", got)
		}
	})
}
