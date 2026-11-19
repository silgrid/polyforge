#!/usr/bin/env node
// The stub `claude` of the MCP write tests (polyforge#144, #85 85c, AC2):
// records every invocation — argv, cwd and the inherited CLAUDE_CONFIG_DIR —
// as one JSON line in $CLAUDE_MCP_STUB_LOG, so a test asserts the exact argv
// Pi Web spawned. CLAUDE_MCP_STUB_FAIL=add makes `mcp add-json` exit 1,
// CLAUDE_MCP_STUB_FAIL=remove makes `mcp remove` exit 1 (the rollback case);
// every other invocation exits 0. It never prints the values it was given.
import { appendFileSync } from "node:fs";

const argv = process.argv.slice(2);
const log = process.env.CLAUDE_MCP_STUB_LOG;
if (log) {
  appendFileSync(log, `${JSON.stringify({ argv, cwd: process.cwd(), claudeConfigDir: process.env.CLAUDE_CONFIG_DIR ?? null })}\n`);
}
const fail = process.env.CLAUDE_MCP_STUB_FAIL;
const isAdd = argv[0] === "mcp" && argv[1] === "add-json";
const isRemove = argv[0] === "mcp" && argv[1] === "remove";
if ((fail === "add" && isAdd) || (fail === "remove" && isRemove)) {
  process.exit(1);
}
process.exit(0);
