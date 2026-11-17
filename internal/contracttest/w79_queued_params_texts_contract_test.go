// carries: docs/reference/config.md, docs/reference/uds-routes.md, docs/reference/commands.md, skills/polyforge/verbs/override.md, pi-web/docs/agents/polyforge-panel.md, pi-web/AGENTS.md, internal/contracttest/w79_queued_params_texts_contract_test.go
package test

// w79_queued_params_texts_contract_test.go (polyforge#79): a text
// contract. A QUEUED work item's per-ticket step params ride the reserved
// `step_params` attrs key (owner ruling 2026-11-17): any machine's daemon
// writes them on its machine-signed attrs route, any machine and any Cell
// that claims the ticket sees them, and the claim merges the declared,
// legal entries into the capsule's overlay record and clears the key once
// that publish succeeded. The composed final values and their sources
// read back through `pf params` and Pi Web shows them in the job panel
// and the 工单详情 (the queued ticket's write face). The acceptance (AC4)
// is the documentation itself, so the shipped text is the behaviour
// pinned here: the config reference's queued-params section, the override
// skill fragment's queued posture and its failure-table row, the UDS
// route table's params face and the queued write, the command reference's
// verb descriptions, and Pi Web's panel note and component lines. Every
// marker is lane-neutral: no lane, step or param name is required text.
//
// Red on the base tree: none of these texts names the queued params yet —
// the first must-contain marker (the config reference's queued-params
// section) fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestW79QueuedParamsDocsContract$' -count=1

import (
	"strings"
	"testing"
)

func TestW79QueuedParamsDocsContract(t *testing.T) {
	t.Run("config reference", func(t *testing.T) {
		const rel = "docs/reference/config.md"
		text := w68Flat(t, rel)
		w79Has(t, rel, text,
			// the queued-params section
			"#### Queued work items — params before the claim (polyforge#79)",
			// the reserved key and its one writer
			"ride the reserved `step_params`",
			"the daemon's machine-signed attrs route",
			"`pf file create --attr step_params=…` is refused",
			// the claim's consumption
			"the claim merges them into the capsule and",
			"clears the key once that publish succeeded",
			// the read face
			"`pf params <id>` reads every",
		)
	})

	t.Run("override skill fragment", func(t *testing.T) {
		const rel = "skills/polyforge/verbs/override.md"
		text := w68Flat(t, rel)
		w79Has(t, rel, text,
			// the verb's reach names the queued posture
			"# override — a work item's per-step overlay: the model chain, the skip switch and the step params",
			"A **queued** work item (polyforge#79): the params only",
			"ride the reserved `step_params`",
			// the claim's re-judgment and the race note
			"the claim merged the params into its capsule",
			"set the param on the running ticket again",
			"never a refused claim",
			// the composed read face and the queued layer's label
			"read back with `pf params <id>`",
			// the failure table's queued row
			"a queued work item's lane does not load on this daemon",
		)
	})

	t.Run("uds route table", func(t *testing.T) {
		const rel = "docs/reference/uds-routes.md"
		text := w68Flat(t, rel)
		w79Has(t, rel, text,
			// the new read face's row
			"| GET | `/jobs/{wi}/params` |",
			"the composed params view",
			// the queued posture the write takes
			"a QUEUED work item's param changes ride the reserved `step_params` attrs key",
			"whole-key read-merge-write",
			"one `attrs_written` event at aihub the record",
		)
	})

	t.Run("command reference", func(t *testing.T) {
		const rel = "docs/reference/commands.md"
		text := w68Flat(t, rel)
		w79Has(t, rel, text,
			// the new verb
			"`params` (a work item's composed params read:",
			// the queued reach of the override verb
			"a queued work item's params included, riding the reserved `step_params` attrs key until its claim",
		)
	})

	t.Run("pi-web panel note", func(t *testing.T) {
		const rel = "pi-web/docs/agents/polyforge-panel.md"
		text := w68Flat(t, rel)
		w79Has(t, rel, text,
			// the watch response shape
			"paramsView?:{wi,posture,steps:[{id,status,params:[{name,value,source?}]}]}",
			// the controls' params section and the started step's read-only
			"the params section is the only part a started step sees",
			"read-only",
			// the 工单详情's section and its one write
			"the 工单详情's params section",
			"a QUEUED work item's rows carry",
			// the files
			"`app/api/polyforge/params/route.ts`",
			"`components/PolyforgeWorkItemParams.tsx`",
		)
	})

	t.Run("pi-web component lines", func(t *testing.T) {
		const rel = "pi-web/AGENTS.md"
		text := w68Flat(t, rel)
		w79Has(t, rel, text,
			// the new component's line
			"PolyforgeWorkItemParams.tsx polyforge#79: the 工单详情's params section",
			// the controls' line names the params display
			"polyforge#79's params display for every step",
			// the work items' line: the read-only surface's one write
			"the detail mounts the params section above (polyforge#79)",
		)
	})
}

// w79Has asserts every marker is in the flattened text of rel.
func w79Has(t *testing.T, rel, text string, markers ...string) {
	t.Helper()
	for _, m := range markers {
		if !strings.Contains(text, m) {
			t.Errorf("%s must carry the queued-params marker %q", rel, m)
		}
	}
}
