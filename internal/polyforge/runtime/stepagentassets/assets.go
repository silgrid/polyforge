package stepagentassets

import "embed"

// FS contains the daemon-owned step-agent resources. They are compiled into
// pf so operator edits cannot replace the worker implementation. The guard
// extension's node test files are deliberately NOT embedded: the step agent
// directory carries only index.ts and rules.ts.
//
//go:embed extensions/subagent/* extensions/guard/index.ts extensions/guard/rules.ts agents/worker.md
var FS embed.FS
