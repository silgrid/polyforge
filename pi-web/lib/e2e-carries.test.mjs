// Every e2e suite's `carries:` declaration resolves inside this package
//. The suites' headers are rooted at the pi-web package
// directory — the subtree's engine context, `.polyforge/verify-scope.conf`
// run with `cd pi-web` — so after the merge into the Polyforge repository
// each glob must still name at least one tracked path under pi-web/. A glob
// that names nothing (a renamed or removed file, a path outside the
// package) fails here instead of silently selecting no suite.
//
// The syntax is the engine's (the repository's docs/contributing.md,
// "Carries"): `// carries:` lines in the suite's opening comment block (the
// leading blank and comment lines before its first line of code), globs
// separated by commas or blanks, shell-case syntax with `*` crossing `/`.
import "#test-env";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("..", import.meta.url));

function trackedPaths() {
  const out = execFileSync("git", ["ls-files", "-z"], { cwd: root, encoding: "utf8" });
  return out.split("\0").filter(Boolean);
}

export function carriesGlobs(source) {
  const globs = [];
  for (const line of source.split("\n")) {
    const trimmed = line.trim();
    if (trimmed === "") continue;
    if (!trimmed.startsWith("//")) break;
    const m = /^\/\/\s*carries:(.*)$/.exec(trimmed);
    if (m) globs.push(...m[1].split(/[\s,]+/).filter(Boolean));
  }
  return globs;
}

export function globRegExp(glob) {
  let re = "";
  for (let i = 0; i < glob.length; i += 1) {
    const c = glob[i];
    if (c === "*") re += ".*";
    else if (c === "?") re += ".";
    else if (c === "[") {
      const end = glob.indexOf("]", i + 1);
      if (end === -1) re += "\\[";
      else {
        re += `[${glob.slice(i + 1, end).replace(/^!/, "^").replace(/\\/g, "\\\\")}]`;
        i = end;
      }
    } else re += c.replace(/[.+^${}()|\\/]/g, "\\$&");
  }
  return new RegExp(`^${re}$`);
}

test("the glob syntax is the engine's shell-case syntax", () => {
  assert.ok(globRegExp("lib/polyforge*").test("lib/polyforge-config/x.ts"), "`*` crosses `/`");
  assert.ok(!globRegExp("lib/a.ts").test("lib/aXts"), "`.` is literal");
  assert.ok(globRegExp("e2e/claude-stub/*").test("e2e/claude-stub/bin/claude"));
  assert.deepEqual(carriesGlobs("// carries: a*, b\n// x\n// carries: c d\nimport x;\n// carries: late\n"), ["a*", "b", "c", "d"]);
});

test("every e2e suite's carries globs resolve to tracked pi-web paths", () => {
  const tracked = trackedPaths();
  assert.ok(tracked.includes("package.json"), "git ls-files runs from the pi-web package root");
  const unresolved = [];
  let declared = 0;
  for (const name of readdirSync(join(root, "e2e")).filter((f) => f.endsWith(".mjs")).sort()) {
    for (const glob of carriesGlobs(readFileSync(join(root, "e2e", name), "utf8"))) {
      declared += 1;
      const re = globRegExp(glob);
      if (!tracked.some((p) => re.test(p))) unresolved.push(`e2e/${name}: ${glob}`);
    }
  }
  assert.ok(declared > 0, "the suites declare carries");
  assert.deepEqual(unresolved, [], "carries globs that name no tracked path under pi-web/");
});

// At most 3 suites per path. A path every suite declares (the
// locale tables, the app shell) made the light tier select dozens of browser
// suites for a one-line UI change. Each tracked path — the declared globs
// expanded to the package's files — is declared by at most 3 suites; shared
// files are declared only by the 2–3 suites that exercise them directly, and
// every other suite runs in the full bar. This is an ordinary unit test: the
// engine never falls back to the full tier because of it.
const CAP = 3;

// Every top-level e2e/*.mjs that declares globs, keyed `e2e/<name>`. Helper
// modules, the runner (`all.mjs`) and the conf's excluded `themes.mjs` declare
// none, so this is the engine's suite set.
export function suiteDeclarations(dir = root) {
  const decls = new Map();
  for (const name of readdirSync(join(dir, "e2e")).filter((f) => f.endsWith(".mjs")).sort()) {
    const globs = carriesGlobs(readFileSync(join(dir, "e2e", name), "utf8"));
    if (globs.length > 0) decls.set(`e2e/${name}`, globs);
  }
  return decls;
}

export function declarers(decls, path) {
  const suites = [];
  for (const [suite, globs] of decls) {
    if (globs.some((g) => globRegExp(g).test(path))) suites.push(suite);
  }
  return suites.sort();
}

// Every path whose declaring-suite count exceeds `cap`, with those suites.
export function overCap(decls, paths, cap) {
  const compiled = [...decls].map(([suite, globs]) => [suite, globs.map(globRegExp)]);
  const over = [];
  for (const path of paths) {
    const suites = compiled.filter(([, res]) => res.some((re) => re.test(path))).map(([suite]) => suite).sort();
    if (suites.length > cap) over.push({ path, suites });
  }
  return over;
}

// The light tier's browser selection for a set of changed paths: the suites
// whose globs match a changed path, plus each changed path that is itself a
// suite, kept when the suite is a browser suite (e2e/all.mjs's marker).
export function lightSelection(decls, changed, dir = root) {
  const selected = new Set();
  for (const path of changed) {
    if (decls.has(path)) selected.add(path);
    for (const suite of declarers(decls, path)) selected.add(suite);
  }
  const marker = "chromium" + ".launch";
  return [...selected].filter((s) => readFileSync(join(dir, s), "utf8").includes(marker)).sort();
}

// Every path an e2e/*.mjs reaches through a relative import (helpers, and the
// few lib files a suite imports): the engine's import-graph selection.
function e2eImportTargets(dir = root) {
  const targets = new Set();
  const re = /(?:\bfrom|\bimport)\s*\(?\s*["'](\.\.?\/[^"']+)["']/g;
  for (const name of readdirSync(join(dir, "e2e")).filter((f) => f.endsWith(".mjs"))) {
    for (const m of readFileSync(join(dir, "e2e", name), "utf8").matchAll(re)) {
      targets.add(join("e2e", m[1]).split("\\").join("/"));
    }
  }
  return targets;
}

const LOCALE_TABLES = ["lib/i18n/messages/en.ts", "lib/i18n/messages/zh-CN.ts", "lib/i18n/messages/zh-TW.ts"];

// The light-tier fixtures: each ticket's first-parent diff, under pi-web/ with
// the prefix stripped — `git diff --name-only a1472dc1^1 a1472dc1 -- pi-web`
// (10 paths) and `git diff --name-only c33c1809^1 c33c1809 --
// pi-web` (15 paths) — and a change to the three locale tables
// alone. Before the narrowing they selected 35 / 44 browser suites at their
// verification and 34 / 37 / 29 on the tree the narrowing started from.
const LIGHT_FIXTURES = {
  "task#484": [
    "components/ChatWindow.tsx",
    "components/ClaudeSubagentPanel.test.mjs",
    "components/ClaudeSubagentPanel.tsx",
    "components/ClaudeSubagentRunningList.test.mjs",
    "components/ClaudeSubagentRunningList.tsx",
    "docs/agents/claude-sessions.md",
    "e2e/claude-subagent-253.mjs",
    ...LOCALE_TABLES,
  ],
  "task#485": [
    "AGENTS.md",
    "components/ChatInput.test.mjs",
    "components/ChatInput.tsx",
    "components/ClaudeSubagentPanel.tsx",
    "components/ComposerBox.tsx",
    "components/PolyforgeJobComposer.tsx",
    "components/PolyforgePanel.test.mjs",
    "components/PolyforgePanel.tsx",
    "components/PolyforgeProgressSubagent.tsx",
    "docs/agents/claude-sessions.md",
    "docs/agents/polyforge-panel.md",
    "e2e/claude-subagent-253.mjs",
    "e2e/polyforge-job-chat-464.mjs",
    "lib/polyforge-conversation-pure.test.mjs",
    "lib/polyforge-conversation-pure.ts",
  ],
  "locale tables only": LOCALE_TABLES,
};

test("no tracked path is declared by more than 3 e2e suites", () => {
  const over = overCap(suiteDeclarations(), trackedPaths(), CAP).map((p) => `${p.path}: ${p.suites.join(", ")}`);
  assert.deepEqual(over, [], `tracked paths declared by more than ${CAP} e2e suites (${over.length}):\n${over.join("\n")}`);
});

test("widening lib/i18n/* past the cap turns the check red and names the suites", () => {
  const tracked = trackedPaths();
  const real = suiteDeclarations();
  const i18nPaths = tracked.filter((p) => p.startsWith("lib/i18n/"));
  const owners = declarers(real, "lib/i18n/messages/en.ts");
  const widened = [...real.keys()]
    .filter((s) => !real.get(s).some((g) => i18nPaths.some((p) => globRegExp(g).test(p))))
    .sort()
    .slice(0, 2);
  assert.equal(widened.length, 2, "two suites that declare nothing under lib/i18n/");
  const mutated = new Map([...real].map(([s, globs]) => [s, widened.includes(s) ? [...globs, "lib/i18n/*"] : globs]));
  const hit = overCap(mutated, tracked, CAP).find((p) => p.path === "lib/i18n/messages/en.ts");
  assert.ok(hit, `adding lib/i18n/* to ${widened.join(", ")} must put lib/i18n/messages/en.ts over the cap`);
  assert.deepEqual(hit.suites, [...owners, ...widened].sort(), "the report names every declaring suite");
  assert.equal(hit.suites.length, 4, `the owners plus the two widened suites, all named: ${hit.suites.join(", ")}`);
});

test("the locale tables are declared by one or two suites and the shell components by two or three", () => {
  const decls = suiteDeclarations();
  const bad = [];
  for (const path of trackedPaths().filter((p) => p.startsWith("lib/i18n/"))) {
    const suites = declarers(decls, path);
    if (suites.length < 1 || suites.length > 2) bad.push(`${path} (want 1–2): ${suites.length} ${suites.join(", ")}`);
  }
  for (const path of ["components/AppShell.tsx", "components/ChatWindow.tsx", "components/ChatInput.tsx"]) {
    const suites = declarers(decls, path);
    if (suites.length < 2 || suites.length > 3) bad.push(`${path} (want 2–3): ${suites.length} ${suites.join(", ")}`);
  }
  assert.deepEqual(bad, [], `declaring-suite counts out of range:\n${bad.join("\n")}`);
});

test("the light tier selects at most 8 browser suites for the #484, #485 and locale-only changes", () => {
  const decls = suiteDeclarations();
  const tracked = new Set(trackedPaths());
  const graph = e2eImportTargets();
  const results = [];
  for (const [name, changed] of Object.entries(LIGHT_FIXTURES)) {
    for (const p of changed) {
      assert.ok(tracked.has(p), `${name}: fixture path ${p} is tracked`);
      assert.ok(!graph.has(p), `${name}: fixture path ${p} is imported by an e2e module, so import-graph selection would apply`);
    }
    const selected = lightSelection(decls, changed);
    if (selected.length > 8) results.push(`${name}: ${selected.length} — ${selected.join(", ")}`);
  }
  assert.deepEqual(results, [], `light selections over 8 browser suites:\n${results.join("\n")}`);
});
