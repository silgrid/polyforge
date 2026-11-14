import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// the projects read + restore face (polyforge#30, Work › projects).
const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const route = await read("./projects/route.ts");
const lib = await read("../../../lib/polyforge-projects.ts");

test("the projects route re-checks the authenticated-surface gate in-route, on both doors", () => {
  assert.match(route, /import \{[^}]*isApiRequestAllowed[^}]*\} from "@\/lib\/request-security"/);
  assert.match(route, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(route, /status: 403/);
  assert.match(route, /export const dynamic = "force-dynamic"/);
  assert.match(route, /export async function GET/, "the list door");
  assert.match(route, /export async function POST/, "the restore door");
});

test("the restore write validates its body before any spawn and never answers 5xx", () => {
  assert.match(route, /hasJsonContentType/, "the JSON content-type gate (415), like the other polyforge writes");
  assert.match(route, /status: 415/);
  assert.match(route, /action !== "restore"/, "only the restore action exists");
  assert.match(route, /status: 400/);
  assert.doesNotMatch(route, /status:\s*5\d\d/);
  assert.doesNotMatch(route, /console\.(log|error|warn)/);
});

test("the list is one mapping read plus one bounded projects get per mapped project, status only", async () => {
  assert.match(lib, /readPolyforgeMappedProjects\(/, "the mapping comes from one pf --json config list");
  assert.match(lib, /"\-\-json", "projects", "get", project/, "one pf --json projects get per mapped project");
  assert.match(lib, /READ_TIMEOUT_MS = 15_?000/, "each read carries the 15 s bound");
  const start = lib.indexOf("function sanitizeProjectStatus");
  const body = lib.slice(start, lib.indexOf("\n}\n", start));
  assert.match(body, /status === "deactivated" \? "deactivated" : "active"/, "the status is the project's, defaulted");
  assert.doesNotMatch(body, /members|repos|description|created_at/, "no other project field is copied off the sanitizer");
});

test("the restore is exactly one pf projects restore, errors capped and never 5xx", async () => {
  assert.match(lib, /"\-\-json", "projects", "restore", project/, "the write is pf --json projects restore <p>");
  assert.match(lib, /slice\(0, STDERR_CAP\)/, "pf's own stderr passes through only under the cap");
  assert.match(lib, /STDERR_CAP = 512/);
  assert.match(lib, /export async function restorePolyforgeProject/);
});

test("the project token is validated before any spawn", () => {
  assert.match(lib, /PROJECT_TOKEN/, "a project name safe as one argv value");
  assert.match(route, /isPolyforgeProjectToken\(/, "the route validates the body's project before the lib spawns");
});
