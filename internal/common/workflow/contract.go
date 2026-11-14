package workflow

// contract.go: the workflow contract's version and its typed load errors
// (docs/reference/workflow-contract.md). A lane — a directory holding a
// workflow.yaml and its step definitions — is read by exactly one loader
// (Load, load.go); every defect it finds is one ContractError carrying a
// WFC-* code, the file and line it sits at and the YAML path of the key,
// and Load collects every defect before it answers.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ContractVersion is the one workflow contract version this build
// implements: 1 (docs/reference/workflow-contract.md): a lane is defined by
// its workflow.yaml — the YAML mapping the earlier releases kept as a
// workflow.md's frontmatter, as a file of its own, the prose in a README.md
// the daemon never reads — and a step may read a long var from a file of
// the lane (`vars_files`); a workflow.md is refused with its rewrite. A
// step's one entry point to the daemon is `pf step` and its one hand-off is
// a section of artifacts/handoff.md; a step's `hub` list names the `pf step
// hub` verbs it may call. A lane declares the version it targets
// (`contract: <n>`); any other value is WFC-101. The version moves by one
// rule: only an incompatible change bumps it, at most once per release
// (owner ruling 2026-11-12). A compatible change — every lane valid under
// the last release stays valid and behaves the same, and the work items it
// claimed keep running: a new optional key whose default is the present
// behaviour, a new value of a key — never bumps it and needs no drain: the
// registry dump's golden file (contract.v<N>.golden) and its baseline are
// regenerated in place, the lane schema
// (workflows/_schema/workflow-<N>.schema.json) with go generate. Any other
// change is incompatible (a key removed or renamed, a key's meaning or
// default changed, a new required key, a check that refuses a lane it
// accepted, a capsule or hand-off format the work items in flight rely on):
// it is registered when it merges, one entry in contract-pending.md (what
// changed, what the migration takes; the pin is red on one the registry
// shows mechanically and nothing registers — compat.go). The release that
// follows a registration bumps the version once, in one change with the
// golden file, the baseline, the lane schema, the reference document,
// every built-in lane, a new docs/upgrade.md section (the drain) and the
// emptied list.
// (Pre-release builds numbered their contract stages up to 5; none of those
// numbers was released, and the first public release, v0.2.0, starts the
// numbering at 1.)
const ContractVersion = 1

// UpgradeSection is the docs/upgrade.md section a deployment moving onto
// this contract version follows (the drain): the WFC-101 refusal and the
// blocked `contract` reason name it.
const UpgradeSection = "v0.2.0, the first public release: workflow contract 1"

// The WFC-* codes in force under ContractVersion (docs/reference/workflow-contract.md,
// "Error codes").
const (
	CodeContractMissing   = "WFC-100"
	CodeContractVersion   = "WFC-101"
	CodeLaneFile          = "WFC-110"
	CodeUnknownKey        = "WFC-111"
	CodeInvalidValue      = "WFC-112"
	CodeMissingKey        = "WFC-113"
	CodeName              = "WFC-120"
	CodeDescription       = "WFC-121"
	CodeShadowsBuiltin    = "WFC-122"
	CodeLaneMissing       = "WFC-123"
	CodeStepID            = "WFC-130"
	CodeOnFail            = "WFC-140"
	CodeDefinitionMissing = "WFC-190"
	CodeDefinitionFront   = "WFC-191"
	CodePlaceholder       = "WFC-195"
	CodeModelCandidate    = "WFC-198"
	CodeSkill             = "WFC-200"
)

// Codes is the meaning of every code in force, keyed by code — the
// registry's error-code table (RegistryDump, the reference document).
var Codes = map[string]string{
	CodeContractMissing:   "`contract` missing",
	CodeContractVersion:   "`contract` is not the version this daemon implements, or the lane is a workflow.md (the lane file of the earlier releases)",
	CodeLaneFile:          "workflow.yaml missing, unparsable or not one mapping, a duplicate key, or a workflow.md beside it",
	CodeUnknownKey:        "unknown or removed key (at any level)",
	CodeInvalidValue:      "a value of the wrong type or outside its range",
	CodeMissingKey:        "a required key is missing",
	CodeName:              "`name` malformed or not the lane's directory name",
	CodeDescription:       "`description` missing or empty",
	CodeShadowsBuiltin:    "a user lane named like a built-in lane of its lane set (it never shadows the built-in lane)",
	CodeLaneMissing:       "(claim) the work item's lane is not in its workspace's lane set (the built-in lanes and that workspace's user lanes); the work item stays queued",
	CodeStepID:            "step id malformed or duplicated",
	CodeOnFail:            "`on_fail` on a non-gate step, or a target that is not an earlier step",
	CodeDefinitionMissing: "a step's definition file is missing",
	CodeDefinitionFront:   "a definition file's frontmatter is missing, unparsable, carries an unknown key, or its `name`/`description` is wrong",
	CodePlaceholder:       "a placeholder no var or fragment resolves, a var nothing uses, or a var shadowing a fragment",
	CodeModelCandidate:    "a model candidate is not `<harness>:<model>[:<effort>]`, or `model_tier` names no tier of `defaults.tiers`",
	CodeSkill:             "`skill` resolves to no file (<lane>/skills/<name>.md, else _shared/skills/<name>.md), or its frontmatter is not {name == the file name, description, commands: []string} over a non-empty body",
}

// ContractError is one contract defect of a lane: its WFC-* code, the
// file and 1-based line it sits at (0 when the defect has no line: a
// missing file) and the YAML path of the key ("" for the file itself).
type ContractError struct {
	Code string `json:"code"`
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Path string `json:"path,omitempty"`
	Msg  string `json:"message"`
}

// Error renders `<code> <file>[:<line>] [<path>: ]<message>`.
func (e ContractError) Error() string {
	var b strings.Builder
	b.WriteString(e.Code)
	b.WriteString(" ")
	b.WriteString(e.File)
	if e.Line > 0 {
		fmt.Fprintf(&b, ":%d", e.Line)
	}
	b.WriteString(" ")
	if e.Path != "" {
		b.WriteString(e.Path)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// LoadError is a lane Load refused: every defect, in file and line order.
type LoadError struct {
	Dir    string
	Errors []ContractError
}

// Error renders every defect, one per line after a summary line.
func (e *LoadError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "workflow lane %s refused (%d contract error", e.Dir, len(e.Errors))
	if len(e.Errors) != 1 {
		b.WriteString("s")
	}
	b.WriteString("):")
	for _, c := range e.Errors {
		b.WriteString("\n  ")
		b.WriteString(c.Error())
	}
	return b.String()
}

// HasCode reports whether one of the defects carries code.
func (e *LoadError) HasCode(code string) bool {
	for _, c := range e.Errors {
		if c.Code == code {
			return true
		}
	}
	return false
}

// AsLoadError answers errs as a *LoadError for dir (nil when there are none).
func AsLoadError(dir string, errs []ContractError) error {
	if len(errs) == 0 {
		return nil
	}
	return &LoadError{Dir: dir, Errors: errs}
}

// sortErrors orders defects: each lane's lane file first, then the
// other files by path, each by line, then code (a stable report).
func sortErrors(errs []ContractError) {
	rank := func(e ContractError) int {
		if b := filepath.Base(e.File); b == WorkflowFile || b == Contract4File {
			return 0
		}
		return 1
	}
	sort.SliceStable(errs, func(i, j int) bool {
		a, b := errs[i], errs[j]
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Code < b.Code
	})
}
