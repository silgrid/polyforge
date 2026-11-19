#!/usr/bin/env node
// Skeleton state (polyforge#140 r2 u5): the mode CLI alone. The plan
// resolution, manifest reading and the runner land in the unit's impl commit.
import process from "node:process";

const MODES = new Set(["responsiveness", "read-volume", "cold-summary", "golden", "mutations"]);

function usage(message) {
  if (message) console.error(`session-list-verify: ${message}`);
  console.error("usage: node scripts/session-list-verify.mjs <mode> --fixture DIR --out DIR [--dry-run] [mode options]");
  console.error(`modes: ${[...MODES].join(", ")}`);
  process.exit(2);
}

const argv = process.argv.slice(2);
const mode = argv.find((arg) => !arg.startsWith("--"));
if (!mode || !MODES.has(mode)) usage(mode ? `unknown mode "${mode}"` : "missing mode");
if (argv.includes("--dry-run")) {
  console.log(JSON.stringify({ ok: true, dryRun: true, mode }, null, 2));
} else {
  console.error("session-list-verify: the runner is not implemented in this state");
  process.exit(1);
}
