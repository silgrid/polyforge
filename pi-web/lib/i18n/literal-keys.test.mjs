// carries: lib/i18n/literal-keys.ts, lib/i18n/dynamic-key-allowlist.json, lib/i18n/messages/*
// Every literal t("…") key the source uses exists in the English message table
//. registry.test.mjs compares the locales with English; this
// closes the other gap: a key written only in a component, never added to
// English, rendered as the raw key and no test said so. Dynamic keys (built at
// run time) cannot be checked statically; a key concatenated at run time
// (`${}` or `+`) must be listed in lib/i18n/dynamic-key-allowlist.json with a
// one-line reason (owner ruling on review r1#1: enforced for concatenated keys
// only; an empty allowlist passes on a tree without them). Literal leaves of a
// partly dynamic argument (`??`, `||`, `?:`) are checked too.
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { checkLiteralKeys, listSourceFiles } = await jiti.import("./literal-keys.ts");
const { getLocalePlugin } = await jiti.import("./registry.ts");

const root = fileURLToPath(new URL("../..", import.meta.url));
const english = new Set(["fixture.present", "fixture.other"]);
const missing = (file, line, key) => `${file}:${line} "${key}" is not in lib/i18n/messages/en.ts`;
const unlisted = (file, line, argument) =>
  `${file}:${line} ${argument} concatenates its key at run time: list it in lib/i18n/dynamic-key-allowlist.json with a one-line reason`;

const component = [
  'export function Panel({ cond, key, x, deps }) {',
  '  const { t } = useI18n();',
  '  const a = t("fixture.present");',
  '  const b = t("fixture.absent");',
  '  const c = t(cond ? "fixture.other" : "fixture.ternaryAbsent");',
  "  const d = translate('fixture.translateAbsent');",
  '  const e = deps.t(`fixture.templateAbsent`);',
  '  const f = t(`fixture.${x}`);',
  '  const g = t(key);',
  '  const h = t(KEYS[key]);',
  '  const j = t(KEYS[key] ?? "fixture.fallbackAbsent") + t(cond ? key : "fixture.branchAbsent") + t(x || "fixture.present");',
  '  // t("fixture.commentAbsent")',
  '  const i = "t(\\"fixture.stringAbsent\\")";',
  '  return <span title={t("fixture.jsxAbsent")}>t("fixture.textAbsent")</span>;',
  '}',
].join("\n");

test("a literal key missing from English is a problem; dynamic keys, comments and strings are not", () => {
  const report = checkLiteralKeys([{ path: "components/Panel.tsx", source: component }], english, []);
  assert.deepEqual(report.problems, [
    missing("components/Panel.tsx", 4, "fixture.absent"),
    missing("components/Panel.tsx", 5, "fixture.ternaryAbsent"),
    missing("components/Panel.tsx", 6, "fixture.translateAbsent"),
    missing("components/Panel.tsx", 7, "fixture.templateAbsent"),
    unlisted("components/Panel.tsx", 8, "`fixture.${x}`"),
    missing("components/Panel.tsx", 11, "fixture.fallbackAbsent"),
    missing("components/Panel.tsx", 11, "fixture.branchAbsent"),
    missing("components/Panel.tsx", 14, "fixture.jsxAbsent"),
  ]);
  assert.equal(report.literalCount, 10);

  const fixed = checkLiteralKeys([{ path: "a.ts", source: 't("fixture.present"); t(key); t(KEYS[k]);' }], english, []);
  assert.deepEqual(fixed.problems, []);
  assert.equal(fixed.literalCount, 1);
});

test("a concatenated key must be listed with a one-line reason, no duplicate, no stale entry; [] passes without one", () => {
  const files = [{ path: "components/Status.tsx", source: 'const s = t(`status.${state}`);\nconst k = t(key);\nconst p = t("status." + kind);' }];
  const entry = { file: "components/Status.tsx", argument: "`status.${state}`", reason: "state comes from the API at run time" };
  const plus = { file: "components/Status.tsx", argument: '"status." + kind', reason: "kind comes from the API at run time" };

  // AC2: an empty allowlist passes on a tree that concatenates no key (t(key) is dynamic, not concatenated).
  assert.deepEqual(checkLiteralKeys([{ path: "a.ts", source: "t(key); t(KEYS[k]);" }], english, []).problems, []);
  assert.deepEqual(checkLiteralKeys(files, english, []).problems, [
    unlisted("components/Status.tsx", 1, "`status.${state}`"),
    unlisted("components/Status.tsx", 3, '"status." + kind'),
  ]);
  assert.deepEqual(checkLiteralKeys(files, english, [entry, plus]).problems, []);

  const problemsOf = (allowlist) => checkLiteralKeys(files, english, Array.isArray(allowlist) ? [...allowlist, plus] : allowlist)
    .problems.filter((problem) => !problem.startsWith("components/Status.tsx:1 "));
  assert.deepEqual(problemsOf({}), ["dynamic-key allowlist must be a JSON array"]);
  for (const reason of [undefined, "", "   ", "first line\nsecond line"]) {
    assert.deepEqual(problemsOf([{ ...entry, reason }]), [
      'allowlist[0] components/Status.tsx `status.${state}`: reason must be one non-empty line',
    ], `reason ${JSON.stringify(reason)}`);
  }
  assert.deepEqual(problemsOf([{ argument: entry.argument, reason: entry.reason }]), [
    "allowlist[0]: file and argument must be strings",
  ]);
  assert.deepEqual(problemsOf([entry, entry]), [
    'allowlist[1] components/Status.tsx `status.${state}`: duplicate entry',
  ]);
  assert.deepEqual(problemsOf([{ ...entry, argument: "`gone.${state}`" }]), [
    "allowlist[0] components/Status.tsx `gone.${state}`: matches no concatenated t() key (stale)",
  ]);
  assert.deepEqual(problemsOf([{ file: "components/Status.tsx", argument: "key", reason: "a variable" }]), [
    "allowlist[0] components/Status.tsx key: matches no concatenated t() key (stale)",
  ]);
});

test("every literal t() key in app/, components/, hooks/ and lib/ exists in English", () => {
  const files = listSourceFiles(root).map((path) => ({ path, source: readFileSync(`${root}/${path}`, "utf8") }));
  assert.ok(files.some((file) => file.path === "components/AppShell.tsx"), "the scan must include components/");
  assert.ok(!files.some((file) => /\.test\.|\.d\.ts$/.test(file.path)), "tests and declarations are not scanned");

  const englishKeys = new Set(Object.keys(getLocalePlugin("en").messages));
  const allowlist = JSON.parse(readFileSync(new URL("./dynamic-key-allowlist.json", import.meta.url), "utf8"));
  const report = checkLiteralKeys(files, englishKeys, allowlist);
  assert.deepEqual(report.problems, []);
  assert.ok(report.literalCount > 1000, `only ${report.literalCount} literal keys found: the scanner matches nothing`);

  // Owner ruling on r1#1: the allowlist is enforced, so the real tree with an
  // empty one reports every listed concatenated call, and nothing else.
  const unlistedProblems = checkLiteralKeys(files, englishKeys, []).problems;
  assert.ok(unlistedProblems.every((problem) => problem.includes("concatenates its key at run time")), unlistedProblems.join("\n"));
  for (const entry of allowlist) {
    assert.ok(unlistedProblems.some((problem) => problem.startsWith(`${entry.file}:`) && problem.includes(` ${entry.argument} concatenates`)),
      `${entry.file} ${entry.argument} must be reported without its allowlist entry`);
  }
});
