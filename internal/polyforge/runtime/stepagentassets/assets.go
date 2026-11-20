package stepagentassets

import "embed"

// FS contains the daemon-owned step-agent resources. They are compiled into
// pf so operator edits cannot replace the worker implementation.
//
//go:embed extensions/subagent/* agents/worker.md
var FS embed.FS
