package test

// carries: skills/polyforge/verbs/doctor.md, docs/reference/commands.md, docs/install.md, docs/quickstart-macos.md, docs/user-guide.md, docs/concepts.md, docs/reference/config.md, skills/polyforge/verbs/init.md, internal/polyforge/daemon/ensure.go, internal/polyforge/cli/verbs_config.go, internal/polyforge/cli/verbs_daemon.go, internal/polyforge/cli/config_validate.go, internal/polyforge/cli/verbs_doctor.go

// w20_doctor_cells_texts_contract_test.go (polyforge#20, AC7): the
// shipped texts state the per-Cell doctor and the machine-wide ensure
// gate — doctor.md teaches the per-Cell examination (a section per named
// Cell, the cell field, the Host/Cell row division, --profile narrowing
// nothing, the cross-Cell mapping verdict, the per-Cell git root), the
// docs no longer say "the daemon refuses to start" without a mapping
// (the daemon starts fine — `pf daemon ensure` is the one that waits for
// a mapping in some Cell), and the CLI's own wordings match. Every
// absence check is paired with a positive marker in the same file, so
// none passes vacuously. Red on the base tree: none of the new texts
// exist there, and every "gone" phrase does.

import (
	"strings"
	"testing"
)

// w20Texts is one file's contract: the phrases it must carry and the
// phrases it must no longer carry (whitespace collapsed in both).
type w20Texts struct {
	rel  string
	want []string
	gone []string
}

// TestW20DoctorCellsTextsContract (AC7): the doctor skill, the docs and
// the CLI wordings teach the per-Cell world.
func TestW20DoctorCellsTextsContract(t *testing.T) {
	for _, c := range []w20Texts{
		{
			// the doctor skill: the per-Cell examination
			"skills/polyforge/verbs/doctor.md",
			[]string{
				// the intro: every Cell, once each, profile-routed reads
				"examines every Cell",
				"one section per named Cell",
				"== Cell <profile>: <file> ==",
				// the JSON shape gains the cell field
				`"detail", "cell"`,
				// the Host-level / Cell-level division and --profile
				"Host-level",
				"Cell-level",
				"--profile` narrows nothing",
				// the mapping row's cross-Cell verdict
				"execution plane is closed",
				"no Cell has a mapping",
				// the retention row's per-Cell git root
				"cells/<profile>/git",
			},
			[]string{
				// the retired pre-migration git root
				"<data-dir>/git/",
				// the retired failure-branch wordings
				"the daemon refuses to start without one",
				"while the config has no real `[workspace]` mapping",
			},
		},
		{
			// the command reference's doctor entry examines every Cell
			"docs/reference/commands.md",
			[]string{"examines every Cell"},
			nil,
		},
		{
			"docs/install.md",
			[]string{
				"`pf daemon ensure` does not start the daemon until at least one Cell has one",
				"once at least one Cell's `[workspace]` mapping exists",
			},
			[]string{"the daemon refuses to start"},
		},
		{
			"docs/quickstart-macos.md",
			[]string{"`pf daemon ensure` does not start the daemon until at least one Cell has one"},
			[]string{"the daemon refuses"},
		},
		{
			"docs/user-guide.md",
			[]string{"starts the daemon only once at least one Cell"},
			[]string{"the daemon refuses to start without a real mapping"},
		},
		{
			"docs/concepts.md",
			[]string{"only once at least one Cell carries a real mapping"},
			[]string{"the daemon refuses to start without a real mapping"},
		},
		{
			"docs/reference/config.md",
			[]string{
				"`pf daemon ensure` starts the daemon only once at least one Cell has a mapping",
				// the memory-policy family is served from the routed Cell's
				// own config, not "the loaded cli.toml snapshot"
				"the serving Cell's loaded config",
			},
			[]string{"the daemon refuses to start until a mapping exists"},
		},
		{
			"skills/polyforge/verbs/init.md",
			[]string{"`pf daemon ensure` starts the daemon only once at least one Cell has a real one"},
			[]string{"the daemon refuses to start until a real one exists"},
		},
		{
			// the daemon-side refusal: every checked file, no retired
			// wording
			"internal/polyforge/daemon/ensure.go",
			[]string{"no [workspace] mapping in any Cell"},
			[]string{"would refuse to start"},
		},
		{
			// pf config's empty-mapping note
			"internal/polyforge/cli/verbs_config.go",
			[]string{"starts the daemon only once at least one Cell has one"},
			[]string{"the daemon refuses to start until one exists"},
		},
		{
			// pf help daemon
			"internal/polyforge/cli/verbs_daemon.go",
			[]string{"starts the daemon only once at least one Cell has one"},
			[]string{"(the daemon would refuse to start)"},
		},
		{
			// the config writer's doc comment
			"internal/polyforge/cli/config_validate.go",
			[]string{"starts the daemon only once at least one Cell has a mapping"},
			[]string{"the daemon refuses to start until a mapping exists"},
		},
		{
			// the doctor's auth wording points at the Cell's file, not
			// the retired PF_API_KEY hint (workbench#350's ledger item)
			"internal/polyforge/cli/verbs_doctor.go",
			[]string{"fix the api key (config api_key in "},
			[]string{"config api_key / PF_API_KEY"},
		},
	} {
		text := w68Flat(t, c.rel)
		for _, want := range c.want {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", c.rel, want)
			}
		}
		for _, gone := range c.gone {
			if strings.Contains(text, gone) {
				t.Errorf("%s still carries the retired %q", c.rel, gone)
			}
		}
	}
}
