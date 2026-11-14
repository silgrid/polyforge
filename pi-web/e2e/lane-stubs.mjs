// pi-web- (wi_ISnuy5WMLcdm) e2e shared fixtures: the offline gh CLI stub
// and stub-file helpers, first authored for the repo-level release/deploy
// lane suites (retired with those scripts when pi-web joined the Polyforge
// repository) and still shared by the deploy-piweb.sh suites
// that import it: deploy-pf-bin, deploy-served-installed,
// deploy-service-shape, deploy-install-channel, deploy-tunnel,
// deploy-tunnel-variants, deploy-tunnel-dead-connector and
// deploy-install-scripts.
//
// adds an installed-package fixture (see the helpers at the
// end): writePiwebPackageTree lays an installed @silgrid/pi-web package
// tree (the repository's bin/ copied whole, a patch-package stub and a
// node-pty stand-in whose native module only an install script builds),
// writePiwebPrefix a whole npm global prefix around it, and
// `node e2e/lane-stubs.mjs piweb-package-tree <dir> [--scripts=ran|skipped]
// [--node-pty=ok|fail] [--patch-package=ok|fail]` writes the tree from a
// shell. Neither touches tmux.
//
// gives the installed package the real launcher's process
// shape, since the served == installed judgement (bin/pi-web-served.js) reads
// the working directory of the launcher's child (Next's server):
// writePiwebLauncher writes bin/pi-web.js = piwebLauncher, a stub launcher
// that spawns the suite's entry, copied to bin/pi-web-entry, with the package
// directory as its cwd. writePiwebPrefix installs that pair (used by
// deploy-install-scripts, deploy-service-shape, deploy-install-channel,
// deploy-tunnel, deploy-tunnel-variants and deploy-tunnel-dead-connector);
// deploy-pf-bin and deploy-served-installed write it into the tree their own
// npm stubs copy.
// Mirroring e2e/deploy-install-channel.mjs, the stub mimics the real CLI's
// observable contract rather than any particular call shape (an
// implementation-layer choice the spec leaves open):
//   - any vX.Y.Z argument selects that release; without one the
//     GHSTUB_LATEST_TAG release is served — so an implementation that
//     resolves latest instead of a pin hands out the wrong tarball;
//   - `gh release view [TAG] --jq .tagName` -> the bare tag, `--jq .url` ->
//     the release page URL (any other --jq query answers the tag);
//   - `gh release view [TAG]` -> release JSON mirroring the real CLI's
//     assets-first shape, per-asset `url` fields included (GHSTUB_ASSETS=
//     missing -> no assets, so an assets check must fail). The first
//     textual "url" in that JSON is an ASSET DOWNLOAD URL — exactly the
//     trap the real response sets for a position-based parser — so a
//     consumer that wants the release-level URL must select it
//     structurally, not textually;
//   - `gh release download [TAG] ... -D <dir>` -> the release's tgz + a
//     SHA256SUMS in <dir> (cwd without -D), the sum matching unless
//     GHSTUB_CHECKSUM_MODE=mismatch corrupts it so a checksum gate fails;
//   - `gh run list [--json ...]` -> a JSON array, `gh run watch <id>` ->
//     exit 0 with a status line, `gh api .../actions/runs...` -> runs JSON;
//     every Actions answer exits 1 under GHSTUB_RUN_MODE=fail (the workflow
//     run failed).
// Every invocation is argv-logged to GHSTUB_LOG so the passes can assert
// which channel traffic actually ran. No network, no real gh, ever.
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const piwebRoot = dirname(dirname(fileURLToPath(import.meta.url)));

export const ghStubBody = [
  "#!/bin/sh",
  "# Stub gh CLI (pi-web- lane e2e) — see e2e/lane-stubs.mjs for the",
  "# observable contract this mimics. Logs argv to GHSTUB_LOG.",
  'log="${GHSTUB_LOG:?GHSTUB_LOG must point at the invocation log}"',
  'printf \'%s\\n\' "$*" >>"$log"',
  'tag=""',
  'for arg in "$@"; do',
  '  case "$arg" in v[0-9]*.[0-9]*.[0-9]*) tag="$arg" ;; esac',
  "done",
  '[ -n "$tag" ] || tag="${GHSTUB_LATEST_TAG:?GHSTUB_LATEST_TAG must name the latest release}"',
  'ver="${tag#v}"',
  'rel=0; run=0; view=0; dl=0; jq=0',
  'query=""',
  'prev=""',
  'for arg in "$@"; do',
  "  case \"$arg\" in",
  '    release) rel=1 ;; run) run=1 ;; view) view=1 ;; download) dl=1 ;;',
  "  esac",
  '  if [ "$prev" = "--jq" ]; then jq=1; query="$arg"; fi',
  '  prev="$arg"',
  "  case \"$arg\" in *actions*) run=1 ;; esac",
  "done",
  'if [ "$rel" = 1 ] && [ "$view" = 1 ]; then',
  '  if [ "$jq" = 1 ]; then',
  '    case "$query" in',
  '      *.url) printf \'https://github.com/example-lane/internal-piweb/releases/tag/%s\\n\' "$tag" ;;',
  '      *) printf \'%s\\n\' "$tag" ;;',
  '    esac',
  '    exit 0',
  '  fi',
  '  if [ "${GHSTUB_ASSETS:-present}" = "missing" ]; then',
  '    printf \'{"tagName":"%s","assets":[],"url":"https://github.com/example-lane/internal-piweb/releases/tag/%s"}\\n\' "$tag" "$tag"',
  "  else",
  '    printf \'{"tagName":"%s","assets":[{"name":"silgrid-pi-web-%s.tgz","url":"https://github.com/example-lane/internal-piweb/releases/download/%s/silgrid-pi-web-%s.tgz"},{"name":"SHA256SUMS","url":"https://github.com/example-lane/internal-piweb/releases/download/%s/SHA256SUMS"}],"url":"https://github.com/example-lane/internal-piweb/releases/tag/%s"}\\n\' "$tag" "$ver" "$tag" "$ver" "$tag" "$tag"',
  "  fi",
  "  exit 0",
  "fi",
  'if [ "$rel" = 1 ] && [ "$dl" = 1 ]; then',
  '  out=""; prev=""',
  '  for arg in "$@"; do',
  '    if [ "$prev" = "-D" ] || [ "$prev" = "--dir" ]; then out="$arg"; fi',
  '    prev="$arg"',
  "  done",
  '  [ -n "$out" ] || out="$PWD"',
  '  tgz="silgrid-pi-web-${ver}.tgz"',
  '  printf \'%s\' "${GHSTUB_PAYLOAD:?GHSTUB_PAYLOAD must hold the payload}" >"${out}/${tgz}"',
  '  if [ "${GHSTUB_CHECKSUM_MODE:-match}" = "mismatch" ]; then',
  '    sum="0000000000000000000000000000000000000000000000000000000000000000"',
  "  else",
  '    sum="${GHSTUB_HASH:?GHSTUB_HASH must hold the payload sha256}"',
  "  fi",
  '  printf \'%s  %s\\n\' "$sum" "$tgz" >"${out}/SHA256SUMS"',
  "  exit 0",
  "fi",
  'if [ "$run" = 1 ]; then',
  '  if [ "${GHSTUB_RUN_MODE:-ok}" = "fail" ]; then',
  '    printf \'X the release workflow run for %s failed\\n\' "$tag" >&2',
  "    exit 1",
  "  fi",
  '  case "$*" in',
  '    *" list "* ) printf \'[{"databaseId":4242,"status":"completed","conclusion":"success","headBranch":"%s"}]\\n\' "$tag" ;;',
  '    *actions* ) printf \'{"total_count":1,"workflow_runs":[{"id":4242,"status":"completed","conclusion":"success","head_branch":"%s"}]}\\n\' "$tag" ;;',
  "    * )",
  '      printf \'{"databaseId":4242,"status":"completed","conclusion":"success","headBranch":"%s"}\\n\' "$tag"',
  '      printf \'Run 4242 (%s): completed (success)\\n\' "$tag" ;;',
  "  esac",
  "  exit 0",
  "fi",
  "exit 0",
  "",
].join("\n");

export const readLog = (file) => {
  try {
    return readFileSync(file, "utf8").split("\n").filter(Boolean);
  } catch {
    return [];
  }
};

export const writeStub = (bin, name, body) => {
  writeFileSync(join(bin, name), body);
  chmodSync(join(bin, name), 0o755);
};

// the lane (.polyforge/deploy.sh) checks this checkout's GitHub
// origin against GH_REPO before any work and refuses a checkout whose origin
// it cannot read — a fresh clone with no origin, as a verification clone is.
// A suite that runs the lane for its pi-web step states the repository the way
// the Go lane tests do: DEPLOY_REMOTE_IS = the GH_REPO the run uses (the
// environment's, else the lane's own default). A readable GitHub origin is
// still checked first, so a checkout with one runs exactly as before.
// deploy-piweb.sh installs from the latest GitHub Release of its channel
// (silgrid/polyforge by default: Pi Web ships only as part of that release),
// so every suite that runs it puts the gh stub on its PATH and this release
// in its environment: one tag whose tarball's SHA256SUMS line matches. The
// stub's argv goes to logPath.
const RELEASE_STUB_PAYLOAD = "stub Pi Web release tarball payload\n";
export const releaseStubEnv = (logPath, tag = "v0.1.1") => ({
  GHSTUB_LOG: logPath,
  GHSTUB_LATEST_TAG: tag,
  GHSTUB_PAYLOAD: RELEASE_STUB_PAYLOAD,
  GHSTUB_HASH: createHash("sha256").update(RELEASE_STUB_PAYLOAD).digest("hex"),
  GHSTUB_CHECKSUM_MODE: "match",
});

export const laneRemoteEnv = (laneDeploy, env) => {
  const fallback = /^GH_REPO="\$\{GH_REPO:-([^}"]+)\}"$/m.exec(readFileSync(laneDeploy, "utf8"))?.[1];
  if (!fallback) throw new Error(`no GH_REPO default line in ${laneDeploy}`);
  return { DEPLOY_REMOTE_IS: env.GH_REPO || fallback };
};

// a real npm global prefix keeps node in <prefix>/bin next to
// the package links it writes there, so the stub pi-web's `#!/usr/bin/env node`
// resolves on the deploy loop's pinned PATH whatever the host's /usr/bin holds.
// Idempotent: the link is (re)written to the node running the suite.
export const linkNode = (bin) => {
  mkdirSync(bin, { recursive: true });
  rmSync(join(bin, "node"), { force: true });
  symlinkSync(process.execPath, join(bin, "node"));
};

// ---------------------------------------------------------------------------
// an installed @silgrid/pi-web package, as `npm install -g`
// leaves it, with or without its install scripts having run.
// ---------------------------------------------------------------------------

// node-pty stand-in: spawn() needs build/Release/pty.node, which only the
// package's install script (scripts/install.js) writes, the way the real
// native module only exists after `node-gyp rebuild` or its prebuild step.
const nodePtyIndex = [
  '"use strict";',
  "// node-pty stand-in (e2e/lane-stubs.mjs fixture): spawn() runs the command as",
  "// a plain child process once the native module the install script builds is",
  "// present, and throws the real package's load error without it.",
  'const { spawn: spawnChild } = require("node:child_process");',
  'const { existsSync } = require("node:fs");',
  'const { join } = require("node:path");',
  "exports.spawn = (file, args, opts = {}) => {",
  '  if (!existsSync(join(__dirname, "build", "Release", "pty.node"))) {',
  "    throw new Error(\"Cannot find module '../build/Release/pty.node'\");",
  "  }",
  "  const child = spawnChild(file, args, { cwd: opts.cwd, env: opts.env });",
  "  const dataListeners = [];",
  "  const exitListeners = [];",
  "  const emit = (chunk) => { for (const listener of dataListeners) listener(chunk.toString()); };",
  '  child.stdout.on("data", emit);',
  '  child.stderr.on("data", emit);',
  '  child.on("exit", (exitCode, signal) => { for (const listener of exitListeners) listener({ exitCode: exitCode ?? 0, signal }); });',
  "  return {",
  "    pid: child.pid,",
  "    onData(listener) { dataListeners.push(listener); return { dispose() {} }; },",
  "    onExit(listener) { exitListeners.push(listener); return { dispose() {} }; },",
  "    kill(signal) { child.kill(signal); },",
  "  };",
  "};",
  "",
].join("\n");

const nodePtyInstall = (mode) =>
  [
    '"use strict";',
    "// node-pty stand-in install script: builds the native module (ok) or fails",
    "// the way a missing toolchain fails node-gyp (fail).",
    'const { mkdirSync, writeFileSync } = require("node:fs");',
    'const { join } = require("node:path");',
    mode === "fail"
      ? 'process.stderr.write("gyp ERR! fixture build failure\\n");\nprocess.exit(1);'
      : 'mkdirSync(join(__dirname, "..", "build", "Release"), { recursive: true });\nwriteFileSync(join(__dirname, "..", "build", "Release", "pty.node"), "fixture native module\\n");',
    "",
  ].join("\n");

// patch-package stub: resolves the package directory from its own location
// (<pkg>/node_modules/.bin/patch-package), so a copied tree marks itself.
const patchPackageStub = (mode) =>
  [
    "#!/bin/sh",
    "# patch-package stand-in (e2e/lane-stubs.mjs fixture).",
    ...(mode === "fail"
      ? ['echo "patch-package: fixture failure" >&2', "exit 1"]
      : [
          'pkg="$(cd "$(dirname "$0")/../.." && pwd)"',
          'mkdir -p "$pkg/node_modules/.fixture"',
          'printf \'patched\\n\' >"$pkg/node_modules/.fixture/patched"',
          "exit 0",
        ]),
    "",
  ].join("\n");

// Everything of an installed package except its package.json and its
// bin/pi-web.js entry (the caller writes those). scripts: "ran" leaves what
// npm running the install scripts leaves (the patched marker and the built
// node-pty module); "skipped" leaves neither, as npm 11 does for scripts its
// allowScripts policy does not cover. checker: false removes the shipped
// bin/pi-web-install-check.js (a package that predates it).
export const writePiwebPackageTree = (pkgDir, { scripts = "ran", nodePty = "ok", patchPackage = "ok", checker = true } = {}) => {
  mkdirSync(pkgDir, { recursive: true });
  cpSync(join(piwebRoot, "bin"), join(pkgDir, "bin"), { recursive: true });
  if (!checker) rmSync(join(pkgDir, "bin", "pi-web-install-check.js"), { force: true });

  const modules = join(pkgDir, "node_modules");
  mkdirSync(join(modules, ".bin"), { recursive: true });
  writeStub(join(modules, ".bin"), "patch-package", patchPackageStub(patchPackage));

  const pty = join(modules, "node-pty");
  mkdirSync(join(pty, "scripts"), { recursive: true });
  writeFileSync(
    join(pty, "package.json"),
    `${JSON.stringify({ name: "node-pty", version: "0.0.0-fixture", main: "index.js", scripts: { install: "node scripts/install.js" } })}\n`,
  );
  writeFileSync(join(pty, "index.js"), nodePtyIndex);
  writeFileSync(join(pty, "scripts", "install.js"), nodePtyInstall(nodePty));

  if (scripts === "ran") {
    mkdirSync(join(pty, "build", "Release"), { recursive: true });
    writeFileSync(join(pty, "build", "Release", "pty.node"), "fixture native module\n");
    mkdirSync(join(modules, ".fixture"), { recursive: true });
    writeFileSync(join(modules, ".fixture", "patched"), "patched\n");
  }
};

// The stub launcher: the real bin/pi-web.js's process shape.
// It spawns the package's bin/pi-web-entry (argv and environment passed
// through, stdio inherited) with the package directory (the realpath parent
// of its own directory) as cwd, as the real launcher spawns Next's server,
// forwards SIGINT, SIGTERM and SIGHUP to it and exits when it exits. The
// entry is spawned as an executable, so a `#!/bin/sh` entry works too.
export const piwebLauncher = [
  "#!/usr/bin/env node",
  '"use strict";',
  "// Stub pi-web launcher (e2e/lane-stubs.mjs): spawns bin/pi-web-entry with the",
  "// package directory as its cwd, as bin/pi-web.js spawns Next's server.",
  'const { spawn } = require("node:child_process");',
  'const { realpathSync } = require("node:fs");',
  'const { dirname, join } = require("node:path");',
  "const pkgDir = dirname(realpathSync(__dirname));",
  'const child = spawn(join(pkgDir, "bin", "pi-web-entry"), process.argv.slice(2), { cwd: pkgDir, stdio: "inherit" });',
  'for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) process.on(signal, () => child.kill(signal));',
  'child.on("error", (error) => { process.stderr.write(`pi-web stub launcher: ${error.message}\\n`); process.exit(1); });',
  'child.on("exit", (code) => process.exit(code ?? 1));',
  "",
].join("\n");

// bin/pi-web.js = piwebLauncher and bin/pi-web-entry = a copy of <entry>,
// both executable, in the package directory pkgDir.
export const writePiwebLauncher = (pkgDir, entry) => {
  const bin = join(pkgDir, "bin");
  mkdirSync(bin, { recursive: true });
  rmSync(join(bin, "pi-web.js"), { force: true });
  writeStub(bin, "pi-web.js", piwebLauncher);
  rmSync(join(bin, "pi-web-entry"), { force: true });
  copyFileSync(entry, join(bin, "pi-web-entry"));
  chmodSync(join(bin, "pi-web-entry"), 0o755);
};

// A whole npm global prefix holding the package: the tree above, its
// package.json (with the repository's own postinstall when postinstall is
// true), the stub launcher as bin/pi-web.js with bin/pi-web-entry = a copy
// of <entry>, the relative <prefix>/bin/pi-web link npm writes, and node in
// <prefix>/bin as a real prefix keeps it. A `cp -R '<prefix>/.' <dest>/`
// keeps the relative link valid.
export const writePiwebPrefix = (prefixDir, { version, entry, postinstall = false, ...treeOpts }) => {
  const pkgDir = join(prefixDir, "lib", "node_modules", "@silgrid", "pi-web");
  writePiwebPackageTree(pkgDir, treeOpts);
  const pkg = { name: "@silgrid/pi-web", version, bin: { "pi-web": "bin/pi-web.js" } };
  if (postinstall) {
    pkg.scripts = { postinstall: JSON.parse(readFileSync(join(piwebRoot, "package.json"), "utf8")).scripts.postinstall };
  }
  writeFileSync(join(pkgDir, "package.json"), `${JSON.stringify(pkg)}\n`);
  writePiwebLauncher(pkgDir, entry);
  mkdirSync(join(prefixDir, "bin"), { recursive: true });
  rmSync(join(prefixDir, "bin", "pi-web"), { force: true });
  symlinkSync("../lib/node_modules/@silgrid/pi-web/bin/pi-web.js", join(prefixDir, "bin", "pi-web"));
  linkNode(join(prefixDir, "bin"));
  return pkgDir;
};

// CLI: node e2e/lane-stubs.mjs piweb-package-tree <pkgDir> [--scripts=ran|skipped]
// [--node-pty=ok|fail] [--patch-package=ok|fail]
const isMain = (() => {
  try {
    return Boolean(process.argv[1]) && realpathSync(resolve(process.argv[1])) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
})();
if (isMain) {
  const [command, pkgDir, ...flags] = process.argv.slice(2);
  if (command !== "piweb-package-tree" || !pkgDir) {
    console.error("usage: node e2e/lane-stubs.mjs piweb-package-tree <pkgDir> [--scripts=ran|skipped] [--node-pty=ok|fail] [--patch-package=ok|fail]");
    process.exit(2);
  }
  const opts = {};
  const keys = { "--scripts": ["scripts", ["ran", "skipped"]], "--node-pty": ["nodePty", ["ok", "fail"]], "--patch-package": ["patchPackage", ["ok", "fail"]] };
  for (const flag of flags) {
    const [name, value] = flag.split("=");
    const known = keys[name];
    if (!known || !known[1].includes(value)) {
      console.error(`lane-stubs: unknown flag ${flag}`);
      process.exit(2);
    }
    opts[known[0]] = value;
  }
  writePiwebPackageTree(resolve(pkgDir), opts);
}
