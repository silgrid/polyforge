import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./usePolyforge.ts", import.meta.url), "utf8");
const effectStart = source.indexOf("if (!probed || !pfPresent || !panelOpen || activeTab !== \"work\" || workTab !== \"jobs\") return;");
const effect = source.slice(effectStart, source.indexOf("}, [probed, pfPresent, panelOpen, activeTab, workTab, pollIntervalSec, refreshCards]);", effectStart));
const refreshStart = source.indexOf("const refreshCards = useCallback");
const refresh = source.slice(refreshStart, source.indexOf("}, []);", refreshStart));

test("card polling is gated on presence, an open dock and Work › Jobs", () => {
  assert.ok(effectStart >= 0, "the gate exists");
  assert.ok(source.indexOf("}, [probed, pfPresent, panelOpen, activeTab, workTab, pollIntervalSec, refreshCards]);", effectStart) > effectStart, "a sub-tab switch re-runs the gate");
  assert.match(effect, /refreshCards\(\);\s*const interval = setInterval\(refreshCards, pollIntervalSec \* 1000\);/, "an immediate read, then the configured interval");
  assert.match(effect, /\+\+cardsSeq\.current;\s*cardsInFlight\.current = false;\s*clearInterval\(interval\);/, "cleanup stops the timer and drops the abandoned read");
});

test("the interval comes from the browser-stored preference", () => {
  assert.match(source, /import \{ usePolyforgePanelPreferences \} from "\.\/usePolyforgePanelPreferences";/);
  assert.match(source, /const \{ pollIntervalSec \} = usePolyforgePanelPreferences\(\);/);
});

test("one request per tick carries every card's cursor", () => {
  assert.equal((refresh.match(/fetch\(/g) ?? []).length, 1, "one fetch per call, never one per card");
  assert.match(refresh, /fetch\(`\/api\/polyforge\/cards\$\{query\}`, \{ signal: controller\.signal \}\)/);
  assert.match(refresh, /const cursors = cardCursors\(cardsRef\.current\);/);
  assert.match(refresh, /`\?cursors=\$\{encodeURIComponent\(JSON\.stringify\(cursors\)\)\}`/);
  assert.match(refresh, /if \(cardsInFlight\.current\) return;/, "a tick that finds a read in flight is skipped");
});

test("a stale read is dropped by sequence and a failed or degraded tick keeps the last cards", () => {
  assert.match(refresh, /if \(seq !== cardsSeq\.current\) return;/);
  assert.match(refresh, /\.catch\(\(\) => null\)/, "a network failure becomes a null body");
  assert.match(refresh, /const next = nextCardSnapshot\(cardsRef\.current, body\);/);
  assert.match(refresh, /cardsRef\.current = next;\s*setCards\(next\);/);
});

test("no new timer for the existing surfaces; cards and their error are exposed", () => {
  assert.equal((source.match(/setInterval\(/g) ?? []).length, 5, "status, messages, watch and open conversation, plus the one cards timer");
  assert.equal((source.match(/const (OPEN|CLOSED)_[A-Z_]+_MS =/g) ?? []).length, 3);
  assert.match(source, /\n    cards,\n    cardsError,\n/);
});

test("a cards request is bounded: a timed-out read keeps the last cards and frees the slot", () => {
  assert.match(source, /const CARD_REQUEST_TIMEOUT_MS = 15000;/, "the bound outlasts the server's 10 s ops read");
  assert.match(refresh, /const controller = new AbortController\(\);\s*const timer = setTimeout\(\(\) => controller\.abort\(\), CARD_REQUEST_TIMEOUT_MS\);/, "one abort timer per request");
  assert.doesNotMatch(source, /AbortSignal\.timeout/, "no AbortSignal.timeout (Safari 16.2 target)");
  assert.match(refresh, /\.catch\(\(\) => null\)\s*\.finally\(\(\) => clearTimeout\(timer\)\)\s*\.then\(\(body\) => \{/, "the abort lands in the catch as a null body and the timer is always cleared");
  assert.match(refresh, /cardsInFlight\.current = false;[\s\S]*const next = nextCardSnapshot\(cardsRef\.current, body\);/, "the timed-out tick frees the slot and keeps the last cards");
});
