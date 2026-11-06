import "#test-env";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { NextRequest } from "next/server.js";
import { createJiti } from "jiti";

// Keep the machine's git configuration out of the answers, same isolation as
// lib/file-tree-visibility.test.mjs: a global excludes file could ignore the
// very names these tests expect to see.
const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-list-route-")));
fs.writeFileSync(path.join(root, "gitconfig"), "");
process.env.GIT_CONFIG_NOSYSTEM = "1";
process.env.GIT_CONFIG_GLOBAL = path.join(root, "gitconfig");
process.env.XDG_CONFIG_HOME = path.join(root, "xdg");
process.env.GIT_CEILING_DIRECTORIES = root;
test.after(() => fs.rmSync(root, { recursive: true, force: true }));

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { GET } = await jiti.import("./route.ts");
const { allowFileRoot } = await jiti.import("../../../../lib/allowed-roots.ts");
allowFileRoot(root);

const source = await fs.promises.readFile(new URL("./route.ts", import.meta.url), "utf8");

function write(filePath, content = "") {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  fs.writeFileSync(filePath, content);
}

function git(cwd, ...args) {
  execFileSync("git", ["-C", cwd, ...args], { stdio: "ignore" });
}

function request(filePath, type, extraQuery = "") {
  const segments = filePath.replace(/\\/g, "/").split("/").filter(Boolean);
  return GET(
    new NextRequest(`http://localhost/api/files/x?type=${type}${extraQuery}`),
    { params: Promise.resolve({ path: segments }) },
  );
}

async function list(dir, extraQuery = "") {
  const response = await request(dir, "list", extraQuery);
  assert.equal(response.status, 200);
  return response.json();
}

async function listNames(dir, extraQuery = "") {
  const { entries } = await list(dir, extraQuery);
  return entries.map((entry) => entry.name);
}

// BUILD_OUTPUT_NAMES is this fork's own opt-in on top of upstream's shared,
// git-aware lib/file-tree-visibility (merge note: the old IGNORED_NAMES /
// IGNORED_SUFFIXES local filter these tests used to assert against was
// removed by the upstream merge; getFileTreeVisibility is now the single
// source of truth for everything except this fork's two exempted names).

test("build/dist stay hidden outside Git unless showBuildOutputs is set, other conventional names never surface", async () => {
  const plain = path.join(root, "plain-build-outputs");
  write(path.join(plain, "build/index.js"));
  write(path.join(plain, "dist/bundle.js"));
  write(path.join(plain, "node_modules/pkg/index.js"));
  write(path.join(plain, ".git/HEAD"));
  write(path.join(plain, "src/main.ts"));

  // Default: dist/build fall back to the unconditional outside-Git name list
  // like every other generated-output name, so they stay hidden.
  assert.deepEqual(await listNames(plain), ["src"]);

  // showBuildOutputs=1 (and the "true" spelling) exempt exactly those two
  // names; node_modules/.git are never exemptable.
  assert.deepEqual(await listNames(plain, "&showBuildOutputs=1"), ["build", "dist", "src"]);
  assert.deepEqual(await listNames(plain, "&showBuildOutputs=true"), ["build", "dist", "src"]);
  assert.deepEqual(await listNames(plain, "&showBuildOutputs=0"), ["src"]);
});

test("a Git-tracked build/ stays visible with the flag off; a Git-ignored dist/ still needs the flag", async () => {
  const repo = path.join(root, "repo-build-outputs");
  write(path.join(repo, ".gitignore"), "dist/\n");
  write(path.join(repo, "build/index.js"));
  write(path.join(repo, "dist/bundle.js"), "bundle");
  write(path.join(repo, "src/main.ts"));
  git(repo, "init", "-q");
  git(repo, "add", ".gitignore", "build", "src");

  // getFileTreeVisibility already shows the tracked build/, independent of
  // the fork's own flag: `isVisible(d.name) || (BUILD_OUTPUT_NAMES.has(...) &&
  // showBuildOutputs)` short-circuits true on the first branch.
  assert.deepEqual(await listNames(repo), ["build", "src", ".gitignore"]);

  // dist/ is git-ignored (so getFileTreeVisibility hides it) and untracked;
  // only the fork's flag can surface it, same as outside a Git work tree.
  assert.deepEqual(await listNames(repo, "&showBuildOutputs=1"), ["build", "dist", "src", ".gitignore"]);

  // Visibility only: an ignored file stays readable and downloadable by path
  // whether or not the flag is set, because read/download never consult
  // BUILD_OUTPUT_NAMES or getFileTreeVisibility at all (see the dedicated
  // test below).
  const readIgnored = await request(path.join(repo, "dist/bundle.js"), "read");
  assert.equal(readIgnored.status, 200);
  assert.equal((await readIgnored.json()).content, "bundle");
});

test("showIgnored=1 surfaces and marks git-ignored entries; the default listing is unchanged", async () => {
  const repo = path.join(root, "repo-show-ignored");
  write(path.join(repo, ".gitignore"), "secrets/\n*.log\nnode_modules/\n");
  write(path.join(repo, "secrets/token.txt"), "token");
  write(path.join(repo, "debug.log"), "log");
  write(path.join(repo, "node_modules/pkg/index.js"));
  write(path.join(repo, "src/main.ts"));
  git(repo, "init", "-q");
  git(repo, "add", ".gitignore", "src");

  // Default: git-ignored entries stay hidden, no `ignored` marker is emitted.
  const defaultListing = await list(repo);
  assert.deepEqual(defaultListing.entries.map((entry) => entry.name), ["src", ".gitignore"]);
  for (const entry of defaultListing.entries) {
    assert.equal(entry.ignored, undefined, "default listing never marks entries ignored");
  }

  // Opt-in: the ignored entries appear, each marked additively; visible
  // entries carry no marker. .git is never surfacable.
  const optedIn = await list(repo, "&showIgnored=1");
  assert.deepEqual(optedIn.entries.map((entry) => entry.name), [
    "node_modules", "secrets", "src", ".gitignore", "debug.log",
  ]);
  const ignoredByName = new Map(optedIn.entries.map((entry) => [entry.name, entry.ignored]));
  assert.equal(ignoredByName.get("secrets"), true);
  assert.equal(ignoredByName.get("debug.log"), true);
  assert.equal(ignoredByName.get("node_modules"), true);
  assert.equal(ignoredByName.get("src"), undefined);
  assert.equal(ignoredByName.get(".gitignore"), undefined);

  // The "true" spelling works like showBuildOutputs's; a 0 keeps the default.
  assert.deepEqual(await listNames(repo, "&showIgnored=true"), [
    "node_modules", "secrets", "src", ".gitignore", "debug.log",
  ]);
  assert.deepEqual(await listNames(repo, "&showIgnored=0"), ["src", ".gitignore"]);

  // Outside a work tree git has no view: the opt-in changes nothing (a
  // name the convention list hides stays hidden, nothing is marked). The
  // fixture uses a conventionally-hidden name that IS ignored inside the
  // repo fixture above, proving the two hiding systems stay separate.
  const plain = path.join(root, "plain-show-ignored");
  write(path.join(plain, "node_modules/pkg/index.js"));
  write(path.join(plain, "src/main.ts"));
  assert.deepEqual(await listNames(plain), ["src"]);
  assert.deepEqual(await listNames(plain, "&showIgnored=1"), ["src"]);
});

test("showIgnored is read exactly once, only inside the type=list branch", () => {
  const listStart = source.indexOf('// type === "list"');
  assert.notEqual(listStart, -1);
  const beforeList = source.slice(0, listStart);
  assert.ok(
    !beforeList.includes('searchParams.get("showIgnored")'),
    "showIgnored must only be read in the type=list branch",
  );
  assert.equal(
    (source.match(/searchParams\.get\("showIgnored"\)/g) ?? []).length,
    1,
    "the query parameter is read exactly once",
  );
});

test("showBuildOutputs is read exactly once, only inside the type=list branch", () => {
  const listStart = source.indexOf('// type === "list"');
  assert.notEqual(listStart, -1);
  const beforeList = source.slice(0, listStart);
  assert.ok(
    !beforeList.includes('searchParams.get("showBuildOutputs")'),
    "showBuildOutputs must only be read in the type=list branch",
  );
  assert.equal(
    (source.match(/searchParams\.get\("showBuildOutputs"\)/g) ?? []).length,
    1,
    "the query parameter is read exactly once",
  );
});

test("the list response shape and dirs-first alphabetical sort are unchanged", async () => {
  const dir = path.join(root, "sorting");
  write(path.join(dir, "b-file.txt"));
  write(path.join(dir, "a-dir/x"));
  write(path.join(dir, "a-file.txt"));
  write(path.join(dir, "b-dir/x"));

  const { entries, path: returnedPath } = await list(dir);
  assert.equal(returnedPath, fs.realpathSync(dir));
  assert.deepEqual(
    entries.map((e) => [e.name, e.isDir]),
    [["a-dir", true], ["b-dir", true], ["a-file.txt", false], ["b-file.txt", false]],
  );
  for (const entry of entries) {
    assert.ok("size" in entry && "modified" in entry, "entry shape (name/isDir/size/modified) is unchanged");
  }
});

test("download and read never gate on build-output visibility (build/ artifacts stay downloadable)", async () => {
  const plain = path.join(root, "downloadable-build-outputs");
  write(path.join(plain, "build/app.bin"), "binary-ish-content");

  // Hidden from the listing by default (no showBuildOutputs)...
  assert.deepEqual(await listNames(plain), []);

  // ...but still readable and downloadable by direct path, same as any other
  // name getFileTreeVisibility or BUILD_OUTPUT_NAMES would hide from a list.
  const read = await request(path.join(plain, "build/app.bin"), "read");
  assert.equal(read.status, 200);
  assert.equal((await read.json()).content, "binary-ish-content");

  const download = await request(path.join(plain, "build/app.bin"), "download");
  assert.equal(download.status, 200);

  // Source-level guard against a regression: neither branch may reference the
  // listing's filter helpers at all.
  const downloadBlock = source.slice(
    source.indexOf('if (type === "download")'),
    source.indexOf('if (type === "meta")'),
  );
  const readBlock = source.slice(
    source.indexOf('if (type === "read")'),
    source.indexOf('if (type === "download")'),
  );
  for (const block of [downloadBlock, readBlock]) {
    assert.ok(!block.includes("getFileTreeVisibility"), "must not filter on getFileTreeVisibility");
    assert.ok(!block.includes("BUILD_OUTPUT_NAMES"), "must not filter on BUILD_OUTPUT_NAMES");
  }
  // The allowed-roots boundary is the only path-based gate on downloads.
  assert.match(source, /isFilePathAllowed\(filePath, allowedRoots\)/);
});
