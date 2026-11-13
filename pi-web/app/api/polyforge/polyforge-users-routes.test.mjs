import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// the users read face (admin probe + membership picker data).
const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const route = await read("./users/route.ts");
const lib = await read("../../../lib/polyforge-messages.ts");

test("the users route re-checks the authenticated-surface gate in-route and is a GET only", () => {
  assert.match(route, /import \{[^}]*isApiRequestAllowed[^}]*\} from "@\/lib\/request-security"/);
  assert.match(route, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(route, /status: 403/);
  assert.doesNotMatch(route, /export async function (POST|PUT|PATCH|DELETE)/, "a read face only");
});

test("the users route degrades to 200 with admin:false and never answers 5xx, logs or invents a code", () => {
  assert.doesNotMatch(route, /status:\s*5\d\d/);
  assert.equal((route.match(/status:/g) ?? []).length, 1, "the gate's 403 is the only non-200");
  assert.match(route, /admin: false,\s*users: \[\]/, "the catch answers the read-only state");
  assert.doesNotMatch(route, /console\.(log|error|warn)/);
  assert.doesNotMatch(route, /code:\s*"/);
});

test("the users wire is id + display_name of human users only", () => {
  const start = lib.indexOf("export function sanitizeUsersAnswer");
  const body = lib.slice(start, lib.indexOf("\n}\n", start));
  assert.match(body, /user\.user_type !== "human"/, "machines never reach the picker");
  assert.match(body, /return \[\{ id, display_name: str\(user\.display_name\) \}\];/, "exactly id + display_name");
  assert.doesNotMatch(body, /email|api_tokens|role/, "no other field is copied");
  assert.match(lib, /runPfJson\(\["--json", "users", "list"\]\)/, "one bounded pf --json users list (the 15 s read bound)");
});
