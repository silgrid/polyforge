// carries: pi-web/components/PolyforgeConfig.test.mjs

package test

// polyforge84_polyforgeconfig_jiti_alias_contract_test.go (polyforge#84
// fix build r4, sync r3's fast-check red, owner ruling 2026-11-23T04:06:29Z):
// the step-tier picker test loads PolyforgeConfig.tsx through a controlled
// jiti alias table. Why the red exists: polyforge#177 (this epic) made
// PolyforgeConfig.tsx import ./ComponentsUpdateCard, and that card imports
// "@/lib/components-update-client"; polyforge#151's picker test (main) lists
// the aliases it needs by hand, so the new import chain fell through to
// jiti's resolver and died with MODULE_NOT_FOUND — each side is green
// alone, only the seam is red.
//
// The fix follows the sibling component test
// (pi-web/components/ComponentsUpdateCard.test.mjs): add
// "@/lib/components-update-client" to the picker test's alias table. This
// guard pins the seam statically — the picker test's alias table must carry
// that entry while PolyforgeConfig.tsx still reaches the client through
// ComponentsUpdateCard. No production behaviour is touched.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolyforgeConfigTestAliasesTheUpdateClient(t *testing.T) {
	root := v1Root(t)

	// The seam the alias must bridge: PolyforgeConfig.tsx → ./ComponentsUpdateCard
	// → "@/lib/components-update-client".
	configRaw, err := os.ReadFile(filepath.Join(root, "pi-web", "components", "PolyforgeConfig.tsx"))
	if err != nil {
		t.Fatalf("PolyforgeConfig.tsx must ship: %v", err)
	}
	if !strings.Contains(string(configRaw), `from "./ComponentsUpdateCard"`) {
		t.Fatal("PolyforgeConfig.tsx no longer imports ./ComponentsUpdateCard: the jiti alias guard below is stale, re-derive the seam")
	}
	cardRaw, err := os.ReadFile(filepath.Join(root, "pi-web", "components", "ComponentsUpdateCard.tsx"))
	if err != nil {
		t.Fatalf("ComponentsUpdateCard.tsx must ship: %v", err)
	}
	if !strings.Contains(string(cardRaw), `from "@/lib/components-update-client"`) {
		t.Fatal("ComponentsUpdateCard.tsx no longer imports @/lib/components-update-client: the jiti alias guard below is stale, re-derive the seam")
	}

	// The picker test owns the controlled alias table.
	path := filepath.Join(root, "pi-web", "components", "PolyforgeConfig.test.mjs")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the PolyforgeConfig test must ship: %v", err)
	}
	body := string(raw)

	const opening = `test("the step-tier picker joins the chain and summarize add rows over the one catalog read"`
	from := strings.Index(body, opening)
	if from < 0 {
		t.Fatal("the step-tier picker test must ship: it is the controlled jiti loader the alias belongs to")
	}
	to := strings.Index(body[from:], "\ntest(")
	if to < 0 {
		to = len(body) - from
	}
	block := body[from : from+to]

	aliasStart := strings.Index(block, "alias: {")
	if aliasStart < 0 {
		t.Fatal("the picker test must load PolyforgeConfig.tsx through an explicit alias table")
	}
	aliasEnd := strings.Index(block[aliasStart:], "\n    },")
	if aliasEnd < 0 {
		t.Fatal("the picker test's alias table must be closed by the test's own indentation")
	}
	table := block[aliasStart : aliasStart+aliasEnd]

	const want = `"@/lib/components-update-client": modulePath("lib/components-update-client.ts"),`
	if !strings.Contains(table, want) {
		t.Errorf("the picker test's jiti alias table must map @/lib/components-update-client to lib/components-update-client.ts (as ComponentsUpdateCard.test.mjs does): without it ComponentsUpdateCard.tsx dies with MODULE_NOT_FOUND and the step-tier picker never runs")
	}
}
