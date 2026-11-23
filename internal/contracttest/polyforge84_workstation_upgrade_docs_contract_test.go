// carries: docs/workstation.md, docs/reference/commands.md, dist/workstation/*

package test

// polyforge84_workstation_upgrade_docs_contract_test.go (polyforge#84 fix
// build, B2): docs/workstation.md's upgrade story must describe both paths
// it has — the image switch (a new base and seed) and the in-place component
// upgrade — instead of the pre-#190 sentence that called an in-container
// component install impossible while the next section documented it. AC10
// judges the page, and the two sections are written by different child
// tickets (#163 and #190), so this test pins the seam: the contradiction
// fails here, and the in-place upgrade's own facts (the 409 `image_managed`
// refusal of the image's own install/self-update entries) stay stated.

import (
	"strings"
	"testing"
)

func TestWorkstationUpgradeDocsDescribeBothPaths(t *testing.T) {
	body := pf101Read(t, "docs/workstation.md")
	if strings.Contains(body, "never an in-container component install") {
		t.Error("docs/workstation.md says an in-container component install is impossible, contradicting its In-place upgrade section (polyforge#84 B2)")
	}

	from := strings.Index(body, "## Upgrading")
	if from < 0 {
		t.Fatal("docs/workstation.md lacks the ## Upgrading section")
	}
	to := strings.Index(body[from:], "## Platform support")
	if to < 0 {
		t.Fatal("docs/workstation.md's Upgrading section never ends at ## Platform support")
	}
	section := body[from : from+to]

	for _, want := range []string{
		"two upgrade paths",
		"the in-place upgrade below",
		"409 `image_managed`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/workstation.md's Upgrading section lacks %q: the image switch and the in-place component upgrade must both be named", want)
		}
	}
}
