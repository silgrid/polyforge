package test

// pi_subagent_proof_docs_contract_test.go (polyforge#228 AC4): the proof
// ticket's residual documentation, checked where each document owns it —
// SOURCE.md's fixed upstream version, the two files' real sha256 values,
// the MIT license source and text, and the local fork note; concepts.md and
// bundles.md's explicit "none + reason"; v2-converge §2.7's
// explicit-source-refusal / implicit-default-warning rule; and the workflow
// contract's every-resume-shape restatement. A digest in SOURCE.md that no
// longer matches the embedded bytes is red, so the document cannot drift
// from the fork it describes.

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

func proofDocFlat(t *testing.T, rel string) string {
	t.Helper()
	return w68Flat(t, rel)
}

// embeddedDigest is one embedded resource's sha256.
func embeddedDigest(t *testing.T, rel string) string {
	t.Helper()
	raw, err := stepagentassets.FS.ReadFile(rel)
	if err != nil {
		t.Fatalf("the embedded %s: %v", rel, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestPiSubagentProofDocs(t *testing.T) {
	// SOURCE.md: fixed upstream, the real digests, the license, the fork.
	source := proofDocFlat(t, "internal/polyforge/runtime/stepagentassets/extensions/subagent/SOURCE.md")
	for _, want := range []string{
		"@earendil-works/pi-coding-agent",
		"1.4.0",
		"MIT License",
		"Mario Zechner",
		"Permission is hereby granted, free of charge",
		"THE SOFTWARE IS PROVIDED \"AS IS\"",
		"daemon fork",
		"`agents.ts` 与上游快照**逐字节相同**",
		"3493514708d71de722b26494f23b77258e32c75cc7c74302ac96b7db139eb4b5",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("SOURCE.md must document %q", want)
		}
	}
	// every 64-hex run in SOURCE.md is one of these three values; the two
	// local files' digests must be the CURRENT embedded bytes.
	digests := regexp.MustCompile(`\b[0-9a-f]{64}\b`).FindAllString(source, -1)
	wantDigests := map[string]bool{
		embeddedDigest(t, "extensions/subagent/index.ts"):                  true,
		embeddedDigest(t, "extensions/subagent/agents.ts"):                 true,
		"3493514708d71de722b26494f23b77258e32c75cc7c74302ac96b7db139eb4b5": true,
	}
	if len(digests) < 3 {
		t.Fatalf("SOURCE.md carries %d sha256 values, want the two local digests and the upstream snapshot's", len(digests))
	}
	for _, d := range digests {
		if !wantDigests[d] {
			t.Errorf("SOURCE.md pins %s, which is neither a current embedded file's digest nor the upstream snapshot's", d)
		}
	}
	for _, rel := range []string{"extensions/subagent/index.ts", "extensions/subagent/agents.ts"} {
		if !strings.Contains(source, embeddedDigest(t, rel)) {
			t.Errorf("SOURCE.md must pin the current %s digest %s", rel, embeddedDigest(t, rel))
		}
	}

	// concepts.md and bundles.md: none + the reason (owner ruling A on the
	// #193 residual #10).
	for _, rel := range []string{"docs/concepts.md", "docs/bundles.md"} {
		doc := proofDocFlat(t, rel)
		if !strings.Contains(doc, "polyforge#228 adds no") || !strings.Contains(doc, "`none`") {
			t.Errorf("%s must state polyforge#228's none with its reason", rel)
		}
		if !strings.Contains(doc, "daemon-owned step agent") && !strings.Contains(doc, "daemon-owned step agent directory") {
			t.Errorf("%s must give the reason: the daemon-owned step agent is reused, not a new concept/bundle shape", rel)
		}
	}

	// v2-converge §2.7: the code's credential rule (#193 residual #13).
	converge := proofDocFlat(t, "docs/design/polyforge-vnext/v2-converge.md")
	for _, want := range []string{
		"a source configured explicitly",
		"`auth.json` is missing refuses",
		"implicit default source without `auth.json` only warns",
	} {
		if !strings.Contains(converge, want) {
			t.Errorf("v2-converge §2.7 must document %q", want)
		}
	}

	// the workflow contract: every resume shape restates the verified
	// capability and revokes the earlier fact (#215 Q3).
	contract := proofDocFlat(t, "docs/reference/workflow-contract.md")
	for _, want := range []string{
		"every resume shape",
		"restates the capability this dispatch verified",
		"replaces the one stated earlier",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("workflow-contract.md's prompt facts must document %q", want)
		}
	}
}
