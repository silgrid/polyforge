// carries: lib/i18n/messages/*, docs/agents/claude-sessions.md
// polyforge#70: the three claude running-turn placeholder keys carry the
// ticket's shorter copy in every locale at once — switching a language never
// mixes old and new wording; the retired strings are gone from the tracked
// tree; the agents' claude-sessions note teaches the new steer copy instead
// of the retired one.
import "#test-env";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { enLocale } = await jiti.import("@/lib/i18n/messages/en");
const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN");
const { zhTWLocale } = await jiti.import("@/lib/i18n/messages/zh-TW");

// The ticket's background table, verbatim — the requirement, not the implementation.
const COPY = {
  "claude.steerWhileRunning": {
    en: "claude is answering; messages join this turn or wait for the next.",
    "zh-CN": "claude 正在回复；发送消息会并入本轮或排到下一轮。",
    "zh-TW": "claude 正在回覆；傳送訊息會併入本輪或排到下一輪。",
  },
  "claude.sendWhileRunning": {
    en: "claude is answering; send when this turn ends.",
    "zh-CN": "claude 正在回复；本轮结束后可发送。",
    "zh-TW": "claude 正在回覆；本輪結束後可傳送。",
  },
  "claude.compactingPlaceholder": {
    en: "claude is compacting; send when it ends.",
    "zh-CN": "claude 正在压缩；结束后可发送。",
    "zh-TW": "claude 正在壓縮；結束後可傳送。",
  },
};
const locales = { en: enLocale, "zh-CN": zhCNLocale, "zh-TW": zhTWLocale };

test("the three claude running-turn placeholder keys carry the #70 copy in en, zh-CN and zh-TW", () => {
  for (const [key, perLocale] of Object.entries(COPY)) {
    for (const [id, locale] of Object.entries(locales)) {
      assert.equal(locale.messages[key], perLocale[id], `${id} ${key}`);
    }
  }
});

// The AC3 proof's grep, run over the whole tracked tree (":/" spans the
// repository root from any cwd): git grep exits 1 when nothing matches —
// the passing shape here. The needles are assembled from halves so this
// file holds none of them literally: the proof grep must find nothing
// anywhere in the tracked tree, this test included.
const RETIRED = [
  "现在发送" + "的消息",
  "現在傳送" + "的訊息",
  "才能发送" + "下一条消息",
  "才能傳送" + "下一則訊息",
  "the next message can be " + "sent when",
  "a message sent now " + "joins this turn",
];
test("the retired placeholder strings are gone from the tracked tree", () => {
  let out;
  try {
    out = execFileSync("git", ["grep", "-n", ...RETIRED.flatMap((s) => ["-e", s]), "--", ":/"], { encoding: "utf8" });
  } catch (e) {
    // exit 1: nothing matched — the passing shape. Anything else is a real failure.
    assert.equal(e.status, 1, `git grep failed: ${e.stdout}${e.stderr}`);
    return;
  }
  assert.fail(`retired copy is still present:\n${out}`);
});

test("claude-sessions.md teaches the new steer placeholder and quotes the retired one nowhere", () => {
  const doc = readFileSync(fileURLToPath(new URL("../../docs/agents/claude-sessions.md", import.meta.url)), "utf8");
  assert.ok(doc.includes(`"claude is answering; messages join this turn or wait for the next.", \`claude.steerWhileRunning\``), "the Steer paragraph quotes the new copy");
  for (const retired of RETIRED) {
    assert.ok(!doc.includes(retired), `the retired copy "${retired.slice(0, 12)}…" is gone`);
  }
});
