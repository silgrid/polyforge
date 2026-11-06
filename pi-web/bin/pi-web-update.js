"use strict";

// eslint-disable-next-line @typescript-eslint/no-require-imports
const { execFileSync, spawnSync } = require("child_process");
// eslint-disable-next-line @typescript-eslint/no-require-imports
const crypto = require("crypto");
// eslint-disable-next-line @typescript-eslint/no-require-imports
const fs = require("fs");
// eslint-disable-next-line @typescript-eslint/no-require-imports
const os = require("os");
// eslint-disable-next-line @typescript-eslint/no-require-imports
const path = require("path");

// The package's own name, as app/api/app-update/route.ts checks it; never
// another package's.
// eslint-disable-next-line @typescript-eslint/no-require-imports
const PACKAGE_NAME = require("../package.json").name;
const NPM_LATEST_URL = `https://registry.npmjs.org/${PACKAGE_NAME.replace("/", "%2F")}/latest`;
const FETCH_TIMEOUT_MS = 10_000;
const GH_TIMEOUT_MS = 30_000;
const MANUAL_COMMAND = `npm install -g ${PACKAGE_NAME}@latest`;

// The release channel this tree is updated from, one OWNER/REPO in a shipped
// marker. The public package's own repository (its name without "@") keeps
// the public npm path; any other repository is an internal line delivered as
// GitHub Release assets (tgz + SHA256SUMS, the repository's release.sh). A tree without a
// valid marker (an upstream or copied package) is refused before any network
// or install, so the updater never swaps an install for another package.
const CHANNEL_FILE = path.join(__dirname, "pi-web-release-channel.json");
const REPO_SLUG = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

function readReleaseChannel({ channelFile = CHANNEL_FILE, packageName = PACKAGE_NAME, read = fs.readFileSync } = {}) {
  let repo;
  try {
    repo = JSON.parse(read(channelFile, "utf8")).repo;
  } catch (readError) {
    throw new Error(`${channelFile} is missing or unreadable (${readError.code ?? readError.message})`);
  }
  if (typeof repo !== "string" || !REPO_SLUG.test(repo)) {
    throw new Error(`${channelFile} does not name an OWNER/REPO release channel`);
  }
  return { kind: repo === packageName.replace(/^@/, "") ? "npm" : "github", repo };
}

function describeChannel(channel) {
  return channel.kind === "npm"
    ? `the npm package ${PACKAGE_NAME}`
    : `the GitHub Releases of ${channel.repo}`;
}

function githubManualCommand(repo) {
  return `gh release download -R ${repo} -p '*.tgz' -p SHA256SUMS, check SHA256SUMS, then npm install -g <the .tgz>`;
}

// Port of lib/app-update.ts (bin cannot import TypeScript).
function parseStableVersion(version) {
  const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(version);
  if (!match) return null;
  const parts = match.slice(1).map(Number);
  return parts.every(Number.isSafeInteger) ? parts : null;
}

function isNewerStableVersion(candidate, current) {
  const candidateParts = parseStableVersion(candidate);
  const currentParts = parseStableVersion(current);
  if (!candidateParts || !currentParts) return false;
  for (let index = 0; index < candidateParts.length; index += 1) {
    if (candidateParts[index] !== currentParts[index]) {
      return candidateParts[index] > currentParts[index];
    }
  }
  return false;
}

async function fetchLatestVersion() {
  const response = await fetch(NPM_LATEST_URL, {
    headers: { Accept: "application/json" },
    signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
  });
  if (!response.ok) throw new Error(`npm registry returned HTTP ${response.status}`);
  const { version } = await response.json();
  if (typeof version !== "string" || !parseStableVersion(version)) {
    throw new Error("npm registry returned an invalid version");
  }
  return version;
}

// Release tags are v-prefixed stable versions (the repository's release tags).
function versionFromTag(tag) {
  const match = /^v(\d+\.\d+\.\d+)$/.exec(tag);
  if (!match || !parseStableVersion(match[1])) throw new Error(`release tag "${tag}" is not vX.Y.Z`);
  return match[1];
}

function runGh(args, options = {}) {
  try {
    return execFileSync("gh", args, { encoding: "utf8", timeout: GH_TIMEOUT_MS, ...options });
  } catch (ghError) {
    if (ghError.code === "ENOENT") throw new Error("the GitHub CLI (gh) is required and was not found");
    throw new Error(`gh ${args.slice(0, 2).join(" ")} failed: ${(ghError.stderr || ghError.message || "").toString().trim()}`);
  }
}

function fetchLatestRelease(repo, gh = runGh) {
  return versionFromTag(gh(["release", "view", "-R", repo, "--json", "tagName", "--jq", ".tagName"]).trim());
}

// The one *.tgz in dir, after its SHA-256 matched its SHA256SUMS line
// (sha256sum format: "<hex>  <name>", "*" marks binary mode).
function verifyReleaseAssets(dir) {
  const sums = new Map();
  for (const line of fs.readFileSync(path.join(dir, "SHA256SUMS"), "utf8").split("\n")) {
    const match = /^([0-9a-fA-F]{64}) [ *](.+)$/.exec(line.trim());
    if (match) sums.set(match[2], match[1].toLowerCase());
  }
  const tarballs = fs.readdirSync(dir).filter((name) => name.endsWith(".tgz"));
  if (tarballs.length !== 1) throw new Error(`expected one .tgz release asset, found ${tarballs.length}`);
  const [name] = tarballs;
  const expected = sums.get(name);
  if (!expected) throw new Error(`SHA256SUMS has no line for ${name}`);
  const actual = crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, name))).digest("hex");
  if (actual !== expected) throw new Error(`SHA-256 mismatch for ${name}`);
  return path.join(dir, name);
}

// Download release v<version>'s assets, verify them, then install the tgz.
// null: no way to run npm safely.
function installRelease(repo, version, { gh = runGh, spawn = spawnSync } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-update-"));
  try {
    gh(["release", "download", `v${version}`, "-R", repo, "-p", "*.tgz", "-p", "SHA256SUMS", "-D", dir], {
      timeout: 10 * 60_000,
    });
    const tarball = verifyReleaseAssets(dir);
    const invocation = findNpmInvocation(["install", "-g", tarball]);
    if (!invocation) return null;
    return spawn(invocation.command, invocation.args, { stdio: "inherit" }).status ?? 1;
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// npm's own CLI script run through this node, so no shell is needed on
// Windows (same lookup as lib/node-cli.ts). null: no way to run npm safely.
function findNpmInvocation(args, nodeDir = path.dirname(process.execPath), platform = process.platform, exists = fs.existsSync) {
  for (const script of [
    path.join(nodeDir, "node_modules", "npm", "bin", "npm-cli.js"),
    path.join(nodeDir, "..", "lib", "node_modules", "npm", "bin", "npm-cli.js"),
  ]) {
    if (exists(script)) return { command: process.execPath, args: [script, ...args] };
  }
  return platform === "win32" ? null : { command: "npm", args };
}

function realpathOrSelf(target, realpath = fs.realpathSync) {
  try {
    return realpath(target);
  } catch {
    return path.resolve(target);
  }
}

// "global": pkgDir is <npm root -g>/<PACKAGE_NAME>; "npx": an npx cache; else "other".
function getInstallKind({ pkgDir, npmRoot, realpath = fs.realpathSync }) {
  const realPkgDir = realpathOrSelf(pkgDir, realpath);
  if (realPkgDir.split(/[\\/]/).includes("_npx")) return "npx";
  if (!npmRoot) return "other";
  let root = realPkgDir;
  for (const part of PACKAGE_NAME.split("/").reverse()) {
    if (path.basename(root) !== part) return "other";
    root = path.dirname(root);
  }
  return realpathOrSelf(root, realpath) === realpathOrSelf(npmRoot, realpath) ? "global" : "other";
}

function getGlobalNpmRoot() {
  const invocation = findNpmInvocation(["root", "-g"]);
  if (!invocation) return null;
  try {
    return execFileSync(invocation.command, invocation.args, { encoding: "utf8", timeout: 30_000 }).trim();
  } catch {
    return null;
  }
}

function runNpmInstall(version) {
  const invocation = findNpmInvocation(["install", "-g", `${PACKAGE_NAME}@${version}`]);
  if (!invocation) return null;
  const result = spawnSync(invocation.command, invocation.args, { stdio: "inherit" });
  return result.status ?? 1;
}

async function runUpdate({
  check,
  currentVersion,
  pkgDir,
  listRecords,
  formatRecord,
  readChannel = readReleaseChannel,
  fetchLatest,
  getNpmRoot = getGlobalNpmRoot,
  install,
  realpath = fs.realpathSync,
  log = console.log,
  error = console.error,
}) {
  let channel;
  try {
    channel = readChannel();
  } catch (channelError) {
    error(`Refusing to update: this is not a pi-web install with a known release channel: ${channelError.message}`);
    return 1;
  }
  const npmChannel = channel.kind === "npm";
  fetchLatest ??= npmChannel ? fetchLatestVersion : async () => fetchLatestRelease(channel.repo);
  install ??= npmChannel ? runNpmInstall : (version) => installRelease(channel.repo, version);
  const manualCommand = npmChannel ? MANUAL_COMMAND : githubManualCommand(channel.repo);
  log(`Update channel: ${describeChannel(channel)}.`);

  let latest;
  try {
    latest = await fetchLatest();
  } catch (fetchError) {
    error(`Could not check for updates: ${fetchError.message}`);
    return 1;
  }
  if (!isNewerStableVersion(latest, currentVersion)) {
    log(`pi-web ${currentVersion} is up to date.`);
    return 0;
  }
  log(`pi-web ${latest} is available (installed: ${currentVersion}).`);
  if (check) return 0;

  // A running `next start` has .next/ open; never replace it underneath.
  const running = await listRecords();
  if (running.length > 0) {
    error(`pi-web is running:\n${running.map(formatRecord).join("\n")}\nRun \`pi-web stop\` first, then update.`);
    return 1;
  }

  const kind = getInstallKind({ pkgDir, npmRoot: getNpmRoot(), realpath });
  if (kind === "npx") {
    error(npmChannel
      ? `This pi-web runs from the npx cache. Run \`npx ${PACKAGE_NAME}@latest\` instead.`
      : `This pi-web runs from the npx cache. Install a release of ${channel.repo} globally instead: ${manualCommand}`);
    return 1;
  }
  if (kind !== "global") {
    error(`This pi-web is not a global npm install. Update it yourself, for example: ${manualCommand}`);
    return 1;
  }

  let code;
  try {
    code = await install(latest);
  } catch (installError) {
    error(`Update failed: ${installError.message}`);
    return 1;
  }
  if (code === null) {
    error(`Could not find npm. Run: ${manualCommand}`);
    return 1;
  }
  return code;
}

module.exports = {
  findNpmInvocation,
  getInstallKind,
  installRelease,
  isNewerStableVersion,
  readReleaseChannel,
  runUpdate,
  verifyReleaseAssets,
  versionFromTag,
};
