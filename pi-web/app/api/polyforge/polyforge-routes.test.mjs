import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const statusSource = await readFile(new URL("./status/route.ts", import.meta.url), "utf8");
const watchSource = await readFile(new URL("./watch/route.ts", import.meta.url), "utf8");
const detectSource = await readFile(new URL("./detect/route.ts", import.meta.url), "utf8");
const libSource = await readFile(new URL("../../../lib/polyforge-status.ts", import.meta.url), "utf8");
const detectLibSource = await readFile(new URL("../../../lib/polyforge-detect.ts", import.meta.url), "utf8");

test("both routes re-check isApiRequestAllowed in-route (audit pattern) and are GET-only", () => {
  for (const [name, source] of [["status", statusSource], ["watch", watchSource], ["detect", detectSource]]) {
    assert.match(source, /export async function GET\(request: Request\)/, `${name} exports GET`);
    assert.doesNotMatch(source, /export async function (POST|PUT|DELETE|PATCH)/, `${name} must stay read-only`);
    assert.match(source, /isApiRequestAllowed\(request\)/, `${name} re-checks the host/origin gate in-route`);
    assert.match(source, /\{ error: "Forbidden" \}, \{ status: 403 \}/, `${name} answers 403 JSON on refusal`);
  }
});

test("degradation-first: no 5xx path, every failure answers decodable JSON with a null status", () => {
  // The detect route's degradation shape is the flat absent status asserted
  // in its own test below (no status/error envelope).
  for (const [name, source] of [["status", statusSource], ["watch", watchSource]]) {
    assert.match(source, /NextResponse\.json\(\{ pfPresent: false, status: null \}\)|NextResponse\.json\(\{ status: null, error/, `${name} has the 200-null degradation path`);
    assert.doesNotMatch(source, /status: 5\d\d/, `${name} must never answer 5xx (the e2e suite treats any 5xx as a browser error)`);
    assert.doesNotMatch(source, /throw new Error/, `${name} must not bare-throw`);
  }
  // pf absence is distinguishable from daemon-down only on the status route
  // (pfPresent), so the client polls only the status re-probe while pf is
  // absent.
  assert.match(statusSource, /pfPresent: false/);
  assert.match(statusSource, /pfPresent: true/);
});

test("the watch route validates wi before any subprocess argv", () => {
  assert.match(watchSource, /searchParams\.get\("wi"\)/);
  assert.match(watchSource, /isPolyforgeWiRef\(wi\)/, "validation happens before getPolyforgeWatch");
  assert.match(watchSource, /\{ error: "wi must be a slug \(pi#107\) or id \(wi_…\)" \},\s*\{ status: 400 \}/, "malformed wi answers a field-level 400");
  // The validation must precede the spawn call in the file.
  assert.ok(
    // the call also carries the row's Cell (`getPolyforgeWatch(wi, profile)`)
    watchSource.indexOf("isPolyforgeWiRef(wi)") < watchSource.indexOf("getPolyforgeWatch(wi"),
    "the ref check must come before the pf call",
  );
});

test("the lib spawns the one-shot watcher with a 40-event tail", () => {
  assert.match(libSource, /"--once"/, "--once is mandatory: the resident watcher would stream forever");
  assert.match(libSource, /"--events", String\(EVENT_TAIL\)/);
  assert.equal(EVENT_TAIL_VALUE(), 40);
});

function EVENT_TAIL_VALUE() {
  const match = libSource.match(/const EVENT_TAIL = (\d+)/);
  assert.ok(match, "EVENT_TAIL constant");
  return Number(match[1]);
}

// ---------------------------------------------------------------------------
// pi#122: the detect route — the Settings Polyforge section's machine-level
// detection face. Degradation-first like its siblings: 200 flat JSON in every
// absent/degraded state (no error field on the pf-absent path), the gate's
// 403 as the only non-2xx, ?force=1 bypassing the 60s presence cache, and the
// presence probe shared with the dock (one detection implementation).
// ---------------------------------------------------------------------------

test("the detect route answers 200 flat JSON in every state; the gate's 403 is its only non-2xx", () => {
  assert.match(detectSource, /export async function GET\(request: Request\)/, "detect exports GET");
  assert.doesNotMatch(detectSource, /export async function (POST|PUT|DELETE|PATCH)/, "detect must stay read-only");
  assert.match(detectSource, /isApiRequestAllowed\(request\)/, "detect re-checks the host/origin gate in-route");
  assert.match(detectSource, /\{ error: "Forbidden" \}, \{ status: 403 \}/, "detect answers 403 JSON on refusal");
  assert.doesNotMatch(detectSource, /status: 5\d\d/, "detect must never answer 5xx (the e2e suite treats any 5xx as a browser error)");
  assert.doesNotMatch(detectSource, /throw new Error/, "detect must not bare-throw");
  // A top-level catch answers 200 with the absent states — no 5xx path exists.
  assert.match(detectSource, /catch \{/);
  assert.match(detectSource, /detectPolyforge\(force\)/, "the route delegates the whole detection to lib/polyforge-detect");
  // The catch's absent shape is the full flat surface (decodable JSON, no error field).
  const caught = detectSource.slice(detectSource.indexOf("catch {"));
  for (const key of ["pfPresent", "pfVersion: null", "pfVersionBelowMinimum", 'line: "unknown"', "piSkillInstalled", "piSkillEnabled", "claudeSkillsSymlink", "claudeSessionStartHook", "daemon: null"]) {
    assert.ok(caught.includes(key), `the catch's absent states must carry ${key}`);
  }
});

test("the detect route wires ?force=1 to the probe's cache bypass", () => {
  assert.match(detectSource, /searchParams\.get\("force"\) === "1"/, "force is the literal ?force=1");
  assert.match(detectLibSource, /probePolyforgeVersion\(force\)/, "force reaches the presence probe's cache bypass");
});

test("one detection implementation: the detect surface reuses the dock's presence probe", () => {
  // The route delegates presence to lib/polyforge-detect.ts, which delegates
  // to the shared probe; no second `pf --version` spawn site exists anywhere.
  // The route names the shared probe it delegates through (the e2e's S3
  // wording check pins the same text).
  assert.match(detectSource, /probePolyforgeVersion|polyforge-status/, "the route names the shared probe");
  assert.match(detectSource, /from "@\/lib\/polyforge-detect"/, "the route imports the detection lib");
  assert.match(detectLibSource, /from "\.\/polyforge-status"/, "the detection lib reuses the dock's probe module");
  assert.match(detectLibSource, /probePolyforgeVersion\(force\)/, "presence flows through the shared probe");
  assert.doesNotMatch(detectLibSource, /"\-\-version"/, "the detection lib spawns no pf --version of its own");
});

test("the detect response surface is flat booleans/labels only (R8)", () => {
  // The assembled status type carries exactly the nine flat keys — no api_key,
  // no server address, no user_id, no cli.toml contents, no absolute home
  // paths, never process.env values.
  const interfaceBody = detectLibSource.slice(detectLibSource.indexOf("export interface PolyforgeDetectStatus"));
  for (const key of ["pfPresent", "pfVersion", "pfVersionBelowMinimum", "line", "piSkillInstalled", "piSkillEnabled", "claudeSkillsSymlink", "claudeSessionStartHook", "daemon"]) {
    assert.match(interfaceBody, new RegExp(`\\b${key}:`), `the detect status carries ${key}`);
  }
  assert.match(detectLibSource, /daemon: PolyforgeDaemonStatus \| null/, "daemon is null (不适用) without pf");
  assert.doesNotMatch(detectLibSource, /process\.env\./, "no process.env value is ever read or echoed");
  assert.doesNotMatch(detectSource, /process\.env\./, "no process.env value is ever read or echoed on the route");
});

test("the status read face forwards the machine's task counts through the one sanitizer", () => {
  assert.match(statusSource, /getPolyforgeOpsStatus\(/, "the status route answers the sanitized ops snapshot");
  const sanitizer = libSource.slice(libSource.indexOf("export function sanitizeOpsStatus("));
  const body = sanitizer.slice(0, sanitizer.indexOf("\n}\n"));
  assert.match(body, /tasks: \{\s*running: toNumber\(tasks\.running\),\s*completed: toNumber\(tasks\.completed\),\s*\}/, "exactly running/completed leave the server");
  assert.doesNotMatch(body, /capped|interrupted|\.failed|\.limit/, "the other task counters are never read");
});

test("a watch addresses the row's Cell — the profile is validated before any spawn and rides as --profile", async () => {
  const progressSource = await readFile(new URL("./progress/route.ts", import.meta.url), "utf8");
  const toolSource = await readFile(new URL("./progress/tool/route.ts", import.meta.url), "utf8");
  const validate = watchSource.indexOf("isPolyforgeProfileName(profile)");
  assert.ok(validate > 0 && validate < watchSource.indexOf("getPolyforgeWatch(wi, profile)"), "the profile check precedes the pf read");
  assert.match(libSource, /\["--profile", profile, "--json", "watch", wiRef, "--once"/);
  for (const source of [progressSource, toolSource]) {
    assert.match(source, /const root = await polyforgeCellRoot\(params\.get\("profile"\) \?\? undefined\);/, "the progress reads resolve the work reference's own Cell dir, never the literal");
  }
});
