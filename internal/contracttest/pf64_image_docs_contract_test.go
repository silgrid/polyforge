// carries: docs/concepts.md, docs/reference/config.md, docs/reference/release.md, docs/reference/commands.md, docs/contributing.md, workflows/integrate/README.md, docs/reference/workflow-contract.md, internal/contracttest/pf64_image_docs_contract_test.go
package test

// pf64_image_docs_contract_test.go (polyforge#64): a text contract for the
// prepared-image documentation (AC5): concepts.md's verification-container
// section (the image parameter's use, the prepared-by-a-person /
// user-solves-errors convention, one image for the project's every container
// repo, the root requirement, the private-Go-dependency BuildKit example,
// the allowlist-overrides-ENV and the volume-first-mount reminders, the
// missing-image mark), the config reference (the parameter's setting forms
// and its cross-references), the release reference (the test gate's image
// mode, the gate id's image line, the plan digest's image half, the green
// tag's image_digest field and the release-side known limitation), the
// command reference (verify.py's and gate.py's --image), and the three
// statements elsewhere that would otherwise stay Dockerfile-only (the
// contributing guide's release gate, the integrate README's gated-repo
// needs, the workflow contract's container inventory).
//
// Red on the base tree: none of these texts names the prepared image yet —
// the first must-contain marker (concepts.md's parameter paragraph) fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestPF64ImageParamDocs$' -count=1 -v

import (
	"strings"
	"testing"
)

// pf64DocsHas asserts every marker is in the flattened text of rel.
func pf64DocsHas(t *testing.T, rel string, markers ...string) {
	t.Helper()
	flat := w68Flat(t, rel)
	for _, m := range markers {
		if !strings.Contains(flat, m) {
			t.Errorf("%s must carry the prepared-image marker %q", rel, m)
		}
	}
}

func TestPF64ImageParamDocs(t *testing.T) {
	t.Run("concepts: the verification container's prepared image", func(t *testing.T) {
		pf64DocsHas(t, "docs/concepts.md",
			// the parameter and its default
			"the verification step declares the `image` parameter",
			"the project sets `steps.<project>.verification.params.image`",
			// the convention: prepared by a person, errors are the user's
			"the image is prepared by a person",
			"any trouble it has is the user's to solve",
			// one image for the project's every container repo
			"one prepared image for every container repo of the project",
			// never pulled; the missing-image mark
			"the fixed mark `pf-verify-image-missing` in the summary",
			// the root requirement
			"must run as root",
			// the allowlist reminder and the volume first-mount reminder
			"override the image's ENV",
			"on its first mount copies",
			"rebuild the image",
			// the private-Go-dependency example
			"RUN --mount=type=ssh",
			"--mount=type=secret",
			"--ssh default=$SSH_AUTH_SOCK",
			"`GOMODCACHE` outside",
		)
	})
	t.Run("config: the first lane-declared parameter", func(t *testing.T) {
		pf64DocsHas(t, "docs/reference/config.md",
			"The first lane-declared parameter is `image`",
			"`steps.<project>.verification.params.image <ref>`",
			"`steps.<project>.full-gate.params.image <ref>`",
			"--param image=<ref>",
		)
	})
	t.Run("release: the test gate's prepared image", func(t *testing.T) {
		pf64DocsHas(t, "docs/reference/release.md",
			// the gate's image mode
			"one image for every gated repo of the project",
			"resolved to its local ID and started by it",
			"the fixed mark `pf-verify-image-missing` in the summary",
			// the gate id's image line, and the missing dockerfile line
			"tools/gate-id `[--show] [--image <digest>] [<rev>]`",
			"line follows the dockerfile one",
			"the dockerfile line is left out",
			// the release-side known limitation
			"never pass `--image`",
			// the plan digest's image half and the green tag's record
			"with a prepared image, its local ID",
			"image_digest",
			// the hand run's flag
			"[--image <ref>]",
		)
	})
	t.Run("commands: verify.py's and gate.py's --image", func(t *testing.T) {
		pf64DocsHas(t, "docs/reference/commands.md",
			"names a prepared image every touched repo's bar then runs in, Dockerfile or not",
			"names a prepared image every gated repo runs in",
			"`pf-verify-image-missing`",
		)
	})
	t.Run("the Dockerfile-only statements elsewhere", func(t *testing.T) {
		pf64DocsHas(t, "docs/contributing.md", "inside that prepared image")
		pf64DocsHas(t, "workflows/integrate/README.md", "no Dockerfile at all")
		pf64DocsHas(t, "docs/reference/workflow-contract.md",
			"a prepared image resolved to its local ID and started by it, never built, never pulled")
	})
}
