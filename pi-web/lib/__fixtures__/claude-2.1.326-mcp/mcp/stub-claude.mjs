#!/usr/bin/env node
// The stub `claude` of the MCP write tests (polyforge#144, #85 85c, AC2):
// records every invocation — argv, cwd and the inherited CLAUDE_CONFIG_DIR —
// as one JSON line in $CLAUDE_MCP_STUB_LOG, so a test asserts the exact argv
// Pi Web spawned. Failure modes, picked with CLAUDE_MCP_STUB_FAIL:
// - "remove": every `mcp remove` exits 1;
// - "add": every `mcp add-json` exits 1;
// - "add-first": only the first `mcp add-json` exits 1 (tracked in
//   $CLAUDE_MCP_STUB_STATE), so the rollback's re-add succeeds — the
//   modification-fails-then-rolls-back case.
// Every other invocation exits 0. It never prints the values it was given.
import { appendFileSync, existsSync, writeFileSync } from "node:fs";

const argv = process.argv.slice(2);
const log = process.env.CLAUDE_MCP_STUB_LOG;
if (log) {
  appendFileSync(log, `${JSON.stringify({ argv, cwd: process.cwd(), claudeConfigDir: process.env.CLAUDE_CONFIG_DIR ?? null })}\n`);
}
const fail = process.env.CLAUDE_MCP_STUB_FAIL;
const isAdd = argv[0] === "mcp" && argv[1] === "add-json";
const isRemove = argv[0] === "mcp" && argv[1] === "remove";
if (fail === "remove" && isRemove) {
  process.exit(1);
}
if (fail === "add" && isAdd) {
  process.exit(1);
}
if (fail === "add-first" && isAdd) {
  const state = process.env.CLAUDE_MCP_STUB_STATE;
  if (state && !existsSync(state)) {
    writeFileSync(state, "1");
    process.exit(1);
  }
}
process.exit(0);
