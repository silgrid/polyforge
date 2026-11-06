import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// the one membership write route.
const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const route = await read("./projects/members/route.ts");
const lib = await read("../../../lib/polyforge-messages.ts");

test("the membership route is an authenticated, JSON-only POST", () => {
  assert.match(route, /import \{[^}]*isApiRequestAllowed[^}]*\} from "@\/lib\/request-security"/);
  assert.match(route, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(route, /status: 403/);
  assert.match(route, /hasJsonContentType\(request\)/);
  assert.match(route, /status: 415/);
  assert.doesNotMatch(route, /export async function (GET|PUT|PATCH|DELETE)/, "one write verb");
});

test("gate, content type, JSON parse, floor, mapping and validation all precede the one spawn", () => {
  const order = [
    /isApiRequestAllowed\(request\)/,
    /hasJsonContentType\(request\)/,
    /request\.json\(\)/,
    /readPolyforgeMessagesCapability\(\)/,
    /validateMembershipInput\(body, await readPolyforgeMappedProjects\(\)\)/,
    /if \(!validated\.ok\)/,
    /changePolyforgeMembership\(validated\.value\)/,
  ].map((pattern) => route.search(pattern));
  for (const [index, at] of order.entries()) assert.ok(at >= 0, `step ${index} is present`);
  for (let i = 1; i < order.length; i++) assert.ok(order[i - 1] < order[i], `step ${i - 1} runs before step ${i}`);
  assert.equal((route.match(/changePolyforgeMembership\(/g) ?? []).length, 1, "exactly one membership spawn site");
});

test("statuses: 400 refusals, pf's 4xx, 200 {error} for an unreachable pf — never 5xx, never a log", () => {
  assert.match(route, /status: 400/);
  assert.match(route, /status: messageErrorStatus\(failure\.code\)/, "the one status mapping");
  assert.match(route, /return NextResponse\.json\(\{ ok: true \}\);/);
  assert.doesNotMatch(route, /status:\s*5\d\d/);
  assert.doesNotMatch(route, /console\.(log|error|warn)/);
  assert.doesNotMatch(route, /code:\s*"/, "no invented code field");
});

test("the lib spawns exactly the CLI's add/remove argv, add always as writer", () => {
  assert.match(lib, /\["--json", "projects", "members", "add", input\.project, "--user", input\.userId, "--role", "writer"\]/);
  assert.match(lib, /\["--json", "projects", "members", "remove", input\.project, "--user", input\.userId\]/);
  assert.match(lib, /\{ timeout: WRITE_TIMEOUT_MS \}/);
  assert.doesNotMatch(lib, /writeFile|fetch\(/, "no config file write and no direct aihub call");
});
