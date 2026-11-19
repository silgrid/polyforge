// carries: lib/polyforge-doctor.ts, lib/polyforge-cells.ts
// polyforge#142 (85a2, AC1): the one `pf --json doctor` read behind the
// account page (Q8: the page's 刷新 runs the full doctor and caches) — the
// wrapper spawns pf the way lib/polyforge-cells.ts does and projects the
// per-Cell harnesses facts through a sanitizer that copies known keys with
// closed vocabularies only, so no value and no undeclared field can reach
// the page. Expected shapes come from the doctor's own `cells` contract
// (u1, Q2 A), never from this implementation.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { readDoctorCells, sanitizeDoctorCells } = await jiti.import("./polyforge-doctor.ts");

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-pf-doctor-"));
test.after(() => { fs.rmSync(scratch, { recursive: true, force: true }); });

/** A stub `pf` recording its argv and answering `body` with `code`. */
function stubPf(name, body, code = 0) {
  const dir = path.join(scratch, name);
  fs.mkdirSync(dir, { recursive: true });
  const log = path.join(scratch, `${name}-calls.jsonl`);
  const binary = path.join(dir, "pf");
  fs.writeFileSync(binary, `#!/usr/bin/env node
const fs = require("node:fs");
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(process.argv.slice(2)) + "\\n");
process.stdout.write(${JSON.stringify(body)});
process.exit(${code});
`);
  fs.chmodSync(binary, 0o755);
  return { binary, log };
}

const DOCTOR = JSON.stringify({
  ok: false,
  checks: [],
  cells: {
    beta: {
      harnesses: {
        pi: { bin: "pi", found: false, config_root: "/h/cells/beta/step-agent", credential: "unset", source: "/home/op/.pi/agent", source_kind: "shared-default", undeclared: "sk-SENTINEL-PF" },
        claude: { bin: "claude", found: false, config_root: "/h/cells/beta/claude-config", source_kind: "shared-default", settings_env: { ANTHROPIC_AUTH_TOKEN: "set", ANTHROPIC_API_KEY: "unset", CLAUDE_CODE_OAUTH_TOKEN: "unset", ANTHROPIC_BASE_URL: "unset", LEAKED: "sk-SENTINEL-ENV" }, credentials_file: "present" },
      },
      execution_disabled: "",
    },
    default: {
      harnesses: {
        pi: { bin: "pi", found: true, path: "/usr/bin/pi", config_root: "/h/cells/default/step-agent", credential: "set", source: "/home/op/.pi/agent", source_kind: "operator" },
        claude: { config_root: "/h/cells/default/claude-config", source: "/home/op/.pi/agent", source_kind: "operator", settings_env: {}, credentials_file: "absent" },
      },
    },
    gamma: { probe_error: "the daemon does not answer for the Cell gamma" },
  },
});

test("readDoctorCells: one pf --json doctor read answers the sanitized Cell rows, default first", async () => {
  const { binary, log } = stubPf("ok", DOCTOR);
  const read = await readDoctorCells({ binary });
  assert.equal(read.error, null);
  assert.deepEqual(read.cells.map((c) => c.profile), ["default", "beta", "gamma"]);
  assert.deepEqual(read.cells[0].harnesses?.pi, {
    configRoot: "/h/cells/default/step-agent", credential: "set",
    source: "/home/op/.pi/agent", sourceKind: "operator",
  });
  const beta = read.cells[1];
  assert.equal(beta.executionDisabled, undefined);
  assert.equal(beta.harnesses?.claude.credentialsFile, "present");
  assert.equal(beta.harnesses?.claude.credentialKinds ?? undefined, undefined);
  assert.equal(beta.harnesses?.pi.sourceKind, "shared-default");
  const gamma = read.cells[2];
  assert.equal(gamma.probeError, "the daemon does not answer for the Cell gamma");
  assert.equal(gamma.harnesses, undefined);
  // the value never survives: the undeclared key and the bogus env value are dropped
  const text = JSON.stringify(read);
  assert.ok(!text.includes("SENTINEL"), `the sanitized read carries a value: ${text}`);
  // exactly one pf spawn, argv as the wrapper declares it
  const calls = fs.readFileSync(log, "utf8").trim().split("\n").map((l) => JSON.parse(l));
  assert.deepEqual(calls, [["--json", "doctor"]]);
});

test("readDoctorCells: a failing or unparsable pf degrades to cells null with the reason, never a throw", async () => {
  const failing = await readDoctorCells({ binary: stubPf("fail", "nope", 1).binary });
  assert.equal(failing.cells, null);
  assert.ok(failing.error && !failing.error.includes("SENTINEL"));
  const garbage = await readDoctorCells({ binary: stubPf("garbage", "not json").binary });
  assert.equal(garbage.cells, null);
  assert.ok(garbage.error);
  const missing = await readDoctorCells({ binary: path.join(scratch, "no-such-pf") });
  assert.equal(missing.cells, null);
  assert.ok(missing.error);
  // a doctor answer without cells (an older pf) degrades the same way
  const old = await readDoctorCells({ binary: stubPf("old", JSON.stringify({ ok: true, checks: [] })).binary });
  assert.equal(old.cells, null);
  assert.ok(old.error);
});

test("sanitizeDoctorCells: only the closed vocabularies pass, everything else is dropped", () => {
  const rows = sanitizeDoctorCells({
    cells: {
      default: {
        harnesses: {
          pi: { config_root: "/r", credential: "bogus", source: "/s", source_kind: "no-such-kind" },
          claude: { settings_env: { ANTHROPIC_API_KEY: "set", X: "else" }, credentials_file: "maybe" },
        },
        execution_disabled: 42,
      },
    },
  });
  assert.deepEqual(rows, [{
    profile: "default",
    harnesses: {
      pi: { configRoot: "/r", source: "/s" },
      claude: { settingsEnv: { ANTHROPIC_API_KEY: "set" } },
    },
  }]);
  assert.equal(sanitizeDoctorCells(null), null);
  assert.equal(sanitizeDoctorCells({}), null);
  assert.equal(sanitizeDoctorCells({ cells: "nope" }), null);
});
