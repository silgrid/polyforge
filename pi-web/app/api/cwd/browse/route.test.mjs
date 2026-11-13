import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// ---------------------------------------------------------------------------
// Route tests for GET and POST /api/cwd/browse. The picker's mkdir must
// enforce filesystem authorization without losing browse coverage. Scopes are injected via
// __setRegistrationScopesForTesting so the registration policy is exercised
// over TMP FIXTURES ONLY, never the operator's real home or prefixes.
// ---------------------------------------------------------------------------

const base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-cwd-browse-")));
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
const previousPrefixes = process.env.PI_WEB_ALLOWED_ROOT_PREFIXES;
process.env.PI_CODING_AGENT_DIR = path.join(base, "agent");
fs.mkdirSync(process.env.PI_CODING_AGENT_DIR);

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { GET, POST } = await jiti.import("./route.ts");
const { NextRequest } = await jiti.import("next/server");
const { __setRegistrationScopesForTesting: setRegistrationScopes } = await jiti.import(
  "../../../../lib/root-registration-policy.ts",
);

test.after(() => {
  setRegistrationScopes(null, previousPrefixes ?? null);
  if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
  fs.rmSync(base, { recursive: true, force: true });
});

// The registration prefixes for this process: a fixture home and the scratch
// base. Anything outside both must be refused by the picker's mkdir.
const homeFixture = path.join(base, "home-fixture");
fs.mkdirSync(homeFixture, { recursive: true });
setRegistrationScopes(homeFixture, base);

function createDirectory(pathValue, name) {
  return POST(new Request("http://localhost/api/cwd/browse", {
    method: "POST",
    headers: { host: "localhost", "content-type": "application/json" },
    body: JSON.stringify({ path: pathValue, name }),
  }));
}

test("creates a directory inside the registration prefixes", async () => {
  const project = path.join(base, "project");
  fs.mkdirSync(project, { recursive: true });

  const response = await createDirectory(project, "new-child");
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { success: true, path: path.join(project, "new-child") });
  assert.ok(fs.existsSync(path.join(project, "new-child")));
});

test("refuses mkdir outside the registration prefixes without filesystem changes", async (t) => {
  const outside = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-cwd-outside-")));
  t.after(() => fs.rmSync(outside, { recursive: true, force: true }));

  const response = await createDirectory(outside, "nope");
  assert.equal(response.status, 403);
  assert.equal(fs.readdirSync(outside).length, 0, "no directory may be created outside the prefixes");
});

test("refuses mkdir through a symlink that escapes the registration prefixes", async (t) => {
  const outside = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-cwd-escape-")));
  t.after(() => fs.rmSync(outside, { recursive: true, force: true }));
  const link = path.join(base, "escape-link");
  const dirType = process.platform === "win32" ? "junction" : "dir";
  try {
    fs.symlinkSync(outside, link, dirType);
  } catch (error) {
    if (error?.code === "EPERM") {
      t.skip("Creating symbolic links requires additional privileges on this platform");
      return;
    }
    throw error;
  }

  // The link itself sits inside the prefixes; the canonical parent does not.
  const response = await createDirectory(link, "nope");
  assert.equal(response.status, 403);
  assert.equal(fs.readdirSync(outside).length, 0, "the symlink target must stay untouched");
});

function browseRequest(query) {
  return new NextRequest(`http://localhost/api/cwd/browse${query}`, {
    headers: { host: "localhost" },
  });
}

async function listNames(response) {
  const data = await response.json();
  return (data.directories ?? []).map((entry) => entry.name);
}

// These GET regressions predate the mkdir authorization repair. Keep both
// sets: guarding a new write route must not remove the fork's browse tests.
test("hidden dot-prefixed directories are excluded unless the literal showHidden=true opts in", async () => {
  const root = path.join(base, "browse-hidden");
  fs.mkdirSync(path.join(root, "visible"), { recursive: true });
  fs.mkdirSync(path.join(root, ".hidden"));

  const absent = await GET(browseRequest(`?path=${encodeURIComponent(root)}`));
  assert.equal(absent.status, 200);
  assert.deepEqual(await listNames(absent), ["visible"]);

  const shown = await GET(browseRequest(`?path=${encodeURIComponent(root)}&showHidden=true`));
  assert.equal(shown.status, 200);
  assert.deepEqual(await listNames(shown), [".hidden", "visible"]);

  for (const value of ["1", "TRUE", "yes", "false", ""]) {
    const response = await GET(browseRequest(`?path=${encodeURIComponent(root)}&showHidden=${value}`));
    assert.equal(response.status, 200);
    assert.deepEqual(await listNames(response), ["visible"], `showHidden=${value} must stay hidden`);
  }
});

test("the Windows drive-picker branch executes and ignores showHidden", async (t) => {
  const original = Object.getOwnPropertyDescriptor(process, "platform");
  Object.defineProperty(process, "platform", { value: "win32", configurable: true });
  t.after(() => {
    if (original) Object.defineProperty(process, "platform", original);
    else Reflect.deleteProperty(process, "platform");
  });

  for (const query of ["", "?showHidden=true"]) {
    const response = await GET(browseRequest(query));
    assert.equal(response.status, 200);
    const data = await response.json();
    assert.equal(data.path, "");
    assert.equal(data.parentPath, null);
    assert.ok(Array.isArray(data.drives));
    assert.deepEqual(data.directories, []);
  }

  const { shouldShowWindowsDrivePicker } = await jiti.import("@/lib/directory-browser.ts");
  assert.equal(shouldShowWindowsDrivePicker(undefined, "win32"), true);
  assert.equal(shouldShowWindowsDrivePicker("C:\\Projects", "win32"), false);
  assert.equal(shouldShowWindowsDrivePicker(undefined, "linux"), false);
});

test("the no-path browse lists the home directory after the platform is restored", { skip: process.platform === "win32" }, async () => {
  const response = await GET(browseRequest(""));
  assert.equal(response.status, 200);
  const data = await response.json();
  assert.equal(data.path, fs.realpathSync(os.homedir()));
  assert.ok(Array.isArray(data.directories));
});

test("browse responses still resolve the directory and its parent", async () => {
  const response = await GET(browseRequest(`?path=${encodeURIComponent(base)}`));
  assert.equal(response.status, 200);
  const data = await response.json();
  assert.equal(data.path, base);
  assert.equal(data.parentPath, path.dirname(base));
});
