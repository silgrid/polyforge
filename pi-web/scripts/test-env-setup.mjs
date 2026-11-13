// Test environment setup, imported first by every test file (`import "#test-env";`).
//
// `npm test` isolates the test processes through tmp-guard, but a bare
// `node --test <file>` loads nothing but the file itself, so the file's first
// import is the one place that can isolate it: before any other module is
// evaluated, the process env becomes the isolated test environment
// (scripts/test-env.mjs) with a temp HOME of its own, removed at exit.
// TMPDIR is left alone, so tmp-guard's leak check still applies.
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { isolatedTestEnv } from "./test-env.mjs";

const home = mkdtempSync(join(tmpdir(), "pi-web-test-home-"));
const next = isolatedTestEnv(process.env, home);
for (const key of Object.keys(process.env)) if (!(key in next)) delete process.env[key];
Object.assign(process.env, next);
process.on("exit", () => rmSync(home, { recursive: true, force: true }));
