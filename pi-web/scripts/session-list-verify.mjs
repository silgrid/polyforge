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
  const option = (name) => (argv.includes(`--${name}`) ? argv[argv.indexOf(`--${name}`) + 1] : undefined);
  // A plan of the right shape with nothing behind it: every contract
  // assertion the tests make lands on a value, never on a missing field.
  const plan = {
    ok: true,
    dryRun: true,
    mode,
    fixture: option("fixture") ?? "",
    out: option("out") ?? "",
    buildDir: option("build-dir") ?? ".next",
    staticPath: option("static") ?? "",
    port: 0,
    manifestTotals: { piFiles: 0, piBytes: 0, claudeFiles: 0, claudeBytes: 0, largeFiles: 0, projectDirs: 0 },
  };
  if (mode === "responsiveness") Object.assign(plan, { modes: [], intervalMs: 0, minSamples: 0, p99Ms: 0, shapes: [], warmCalls: 1 });
  if (mode === "read-volume") Object.assign(plan, { modes: [], calls: 0, maxSecondReadRatio: 1, independentServers: false });
  if (mode === "cold-summary") Object.assign(plan, { emptyIndex: false, maxMs: 0, maxReadRatio: 1, buildsWarmIndex: false });
  if (mode === "golden") Object.assign(plan, { baseline: "unresolved", candidate: "", transientFields: [] });
  if (mode === "mutations") Object.assign(plan, { harnesses: [], changes: [] });
  console.log(JSON.stringify(plan, null, 2));
} else {
  console.error("session-list-verify: the runner is not implemented in this state");
  process.exit(1);
}
