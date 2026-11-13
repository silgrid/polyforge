// The dependency-audit exemption ledger (the repository root's
// .polyforge/audit-exemptions.conf) covers the recorded baseline readings:
// every high/critical advisory of pi-web's root and demo/ lockfile trees has a
// valid row. It is a subset check, so a
// later owner-ruled row, or a row removed after its fix, needs no edit of the
// recordings. The readings are npm's own output, recorded on 2026-11-04 in
// each tree with
//   npm audit --json --audit-level=high --package-lock-only --include=dev
// (lib/audit-baseline/root.json, lib/audit-baseline/demo.json). The row
// grammar is the gate's: six non-empty `|`-separated fields, a GHSA-/GO-
// advisory id, `expires` no earlier than `from` and at most 90 days after it.
// The test is date-independent (this runs under npm test): whether a row is
// in force today is the live gate's judgment on the full bar, never this
// test's.
import "#test-env";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../", import.meta.url);
const read = (rel) => readFileSync(new URL(rel, root), "utf8");
// One ledger, one place: the repository root's
// .polyforge/audit-exemptions.conf, which the dependency-audit gate reads for
// every lockfile tree, pi-web's included.
const LEDGER = "../.polyforge/audit-exemptions.conf";

const DAY = 24 * 60 * 60 * 1000;
const ADVISORY = /^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|GO-[0-9]{4}-[0-9]{4,})$/;
const DATE = /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/;

function parseDate(s) {
  if (!DATE.test(s)) return null;
  const d = new Date(`${s}T00:00:00Z`);
  return Number.isNaN(d.getTime()) || d.toISOString().slice(0, 10) !== s ? null : d;
}

function ledgerRows() {
  const rows = [];
  read(LEDGER).split("\n").forEach((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith("#")) return;
    assert.ok(line.startsWith("exempt="), `ledger line ${i + 1} is not an exempt= row`);
    const f = line.slice("exempt=".length).split("|").map((v) => v.trim());
    assert.equal(f.length, 6, `ledger line ${i + 1} has ${f.length} fields, a row has 6`);
    const [advisory, pkg, reason, ruling, from, expires] = f;
    for (const [name, v] of Object.entries({ advisory, pkg, reason, ruling, from, expires })) {
      assert.ok(v, `ledger line ${i + 1}: ${name} is empty`);
    }
    rows.push({ line: i + 1, advisory, pkg, from, expires });
  });
  return rows;
}

function findings(rel) {
  const report = JSON.parse(read(rel));
  assert.ok(report.vulnerabilities && !report.error, `${rel} is an audited npm report`);
  const out = new Set();
  for (const v of Object.values(report.vulnerabilities)) {
    for (const via of v.via) {
      if (typeof via !== "object" || !["high", "critical"].includes(via.severity)) continue;
      out.add(`${via.url.split("/").pop()}|${via.name}`);
    }
  }
  return out;
}

test("every exemption row is well formed and spans at most 90 days", () => {
  const rows = ledgerRows();
  assert.ok(rows.length > 0);
  for (const r of rows) {
    assert.match(r.advisory, ADVISORY, `line ${r.line}: advisory id`);
    const from = parseDate(r.from);
    const expires = parseDate(r.expires);
    assert.ok(from && expires, `line ${r.line}: dates are YYYY-MM-DD`);
    assert.ok(expires >= from && expires - from <= 90 * DAY, `line ${r.line}: expires within 90 days of from`);
  }
});

test("every recorded baseline finding has an exemption row", () => {
  const found = new Set([...findings("lib/audit-baseline/root.json"), ...findings("lib/audit-baseline/demo.json")]);
  assert.ok(found.size > 0, "the recorded readings carry the baseline findings");
  const keys = new Set(ledgerRows().map((r) => `${r.advisory}|${r.pkg}`));
  const missing = [...found].filter((k) => !keys.has(k)).sort();
  assert.deepEqual(missing, [], "recorded findings without a row");
});
