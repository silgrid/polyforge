#!/usr/bin/env node
// The stub `claude` of the MCP write tests (polyforge#144, #85 85c, AC2):
// records every `mcp` invocation — argv, cwd and the inherited
// CLAUDE_CONFIG_DIR — as one JSON line in $CLAUDE_MCP_STUB_LOG, so a test
// asserts the exact argv Pi Web spawned. Failure modes, picked with
// CLAUDE_MCP_STUB_FAIL:
// - "remove": every `mcp remove` exits 1;
// - "add": every `mcp add-json` exits 1;
// - "add-first": only the first `mcp add-json` exits 1 (tracked in
//   $CLAUDE_MCP_STUB_STATE), so the rollback's re-add succeeds — the
//   modification-fails-then-rolls-back case.
// A probe invocation (claude-probe's `initialize`, anything that is not
// `mcp …`) is answered with $CLAUDE_MCP_STUB_VERSION's initialize frame and
// never logged — the version gate runs before every write, so the route
// tests probe through this stub too. It never prints the values it was given.
import { appendFileSync, existsSync, writeFileSync } from "node:fs";

const argv = process.argv.slice(2);
if (argv[0] === "mcp") {
  const log = process.env.CLAUDE_MCP_STUB_LOG;
  if (log) {
    appendFileSync(log, `${JSON.stringify({ argv, cwd: process.cwd(), claudeConfigDir: process.env.CLAUDE_CONFIG_DIR ?? null })}\n`);
  }
  const fail = process.env.CLAUDE_MCP_STUB_FAIL;
  const isAdd = argv[1] === "add-json";
  const isRemove = argv[1] === "remove";
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
}

// The probe: read stdin until the initialize request, answer it, leave.
let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  input += chunk;
  let newline;
  while ((newline = input.indexOf("\n")) >= 0) {
    const line = input.slice(0, newline);
    input = input.slice(newline + 1);
    let frame;
    try {
      frame = JSON.parse(line);
    } catch {
      continue;
    }
    if (frame?.type !== "control_request" || frame.request?.subtype !== "initialize") continue;
    const version = process.env.CLAUDE_MCP_STUB_VERSION ?? "2.1.326";
    process.stdout.write(`${JSON.stringify({
      type: "control_response",
      response: {
        request_id: frame.request_id,
        subtype: "success",
        response: { claude_code_version: version, models: [], commands: [], capabilities: [] },
      },
    })}\n`);
  }
});
process.stdin.on("end", () => process.exit(0));
