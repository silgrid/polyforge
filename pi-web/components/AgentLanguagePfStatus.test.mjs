// carries: components/AgentLanguagePfStatus*, components/SettingsPanel.tsx, lib/i18n/messages/*
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { AgentLanguagePfStatus, requestAgentLanguageSync } = await jiti.import("./AgentLanguagePfStatus.tsx");
const { enLocale } = await jiti.import("@/lib/i18n/messages/en");
const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN");
const { zhTWLocale } = await jiti.import("@/lib/i18n/messages/zh-TW");

const SYNC_ATTR = "data-agent-language-pf-sync";

function props(fields) {
  return {
    language: "zh-CN",
    polyforge: { language: "zh-CN", source: "file" },
    languageLabels: { en: "English", "zh-CN": "中文" },
    statusText: (label) => `Polyforge step agents use: ${label}`,
    mismatchText: "Polyforge uses a different language.",
    unsetMismatchText: "Polyforge's config does not set a language yet.",
    syncLabel: "Sync to Polyforge",
    syncing: false,
    onSync: () => {},
    ...fields,
  };
}

function render(fields) {
  return renderToStaticMarkup(React.createElement(AgentLanguagePfStatus, props(fields)));
}

/** The element carrying the sync attribute in the component's returned tree (it holds no hook, so it is called directly). */
function findSyncButton(node) {
  if (node === null || node === undefined || typeof node !== "object") return null;
  if (Array.isArray(node)) {
    for (const child of node) {
      const found = findSyncButton(child);
      if (found) return found;
    }
    return null;
  }
  if (node.props?.[SYNC_ATTR] !== undefined) return node;
  return findSyncButton(node.props?.children);
}

test("shows the language Polyforge's step agents use", () => {
  const html = render({});
  assert.match(html, /Polyforge step agents use: 中文/);
  const english = render({ language: "en", polyforge: { language: "en", source: "default" } });
  assert.match(english, /Polyforge step agents use: English/);
});

test("a mismatch shows the hint and a sync button whose click calls onSync", () => {
  let calls = 0;
  const fields = { language: "zh-CN", polyforge: { language: "en", source: "file" }, onSync: () => { calls += 1; } };
  const html = render(fields);
  assert.match(html, /Polyforge step agents use: English/);
  assert.match(html, /<[^>]*role="status"[^>]*>Polyforge uses a different language\.</);
  assert.doesNotMatch(html, /role="alert"/);
  assert.match(html, /Sync to Polyforge/);
  const button = findSyncButton(AgentLanguagePfStatus(props(fields)));
  assert.ok(button, "the sync button is in the returned tree");
  assert.equal(button.props.disabled, false);
  button.props.onClick();
  assert.equal(calls, 1);
});

test("agreement shows neither hint nor button; no pf shows nothing", () => {
  const agreed = render({ language: "zh-CN", polyforge: { language: "zh-CN", source: "file" } });
  assert.match(agreed, /Polyforge step agents use: 中文/);
  assert.doesNotMatch(agreed, /Polyforge uses a different language/);
  assert.doesNotMatch(agreed, /Sync to Polyforge/);
  assert.equal(findSyncButton(AgentLanguagePfStatus(props({}))), null);
  assert.equal(render({ polyforge: null }), "");
  assert.equal(render({ polyforge: undefined }), "");
  assert.equal(render({ language: "en", polyforge: null }), "");
});

test("while syncing the sync button is disabled", () => {
  const fields = { language: "zh-CN", polyforge: { language: "en", source: "default" }, syncing: true };
  const button = findSyncButton(AgentLanguagePfStatus(props(fields)));
  assert.ok(button, "the sync button is shown while syncing");
  assert.equal(button.props.disabled, true);
  assert.match(render(fields), /<button[^>]*disabled=""[^>]*>Sync to Polyforge<\/button>/);
});

test("requestAgentLanguageSync PUTs the language, then reads GET and returns pf's state", async () => {
  const calls = [];
  const fetchImpl = async (url, init = {}) => {
    calls.push({ url, method: init.method ?? "GET", body: init.body });
    if ((init.method ?? "GET") === "PUT") return Response.json({ language: "zh-CN" });
    return Response.json({ language: "zh-CN", polyforge: { language: "zh-CN", source: "file" } });
  };
  const result = await requestAgentLanguageSync("zh-CN", fetchImpl);
  assert.deepEqual(calls.map((call) => [call.url, call.method]), [["/api/agent-language", "PUT"], ["/api/agent-language", "GET"]]);
  assert.deepEqual(JSON.parse(calls[0].body), { language: "zh-CN" });
  assert.equal(result.language, "zh-CN");
  assert.equal(result.polyforge.language, result.language);
  assert.deepEqual(result.polyforge, { language: "zh-CN", source: "file" });

  const refused = [];
  const refusingFetch = async (url, init = {}) => {
    refused.push(init.method ?? "GET");
    return Response.json({ error: "pf refused" }, { status: 500 });
  };
  await assert.rejects(requestAgentLanguageSync("zh-CN", refusingFetch));
  assert.deepEqual(refused, ["PUT"]);

  const getFails = async (url, init = {}) => (init.method ?? "GET") === "PUT"
    ? Response.json({ language: "en" })
    : Response.json({ error: "boom" }, { status: 500 });
  await assert.rejects(requestAgentLanguageSync("en", getFails));
});

// when the start's backfill was refused (CORE-409) the GET reads pf still unset
// (source default) — the "never overwritten" hint is wrong then; the unset hint says so.
test("a pf that is still unset shows the unset hint, not the never-overwritten one", () => {
  const unset = render({ language: "zh-CN", polyforge: { language: "en", source: "default" } });
  assert.match(unset, /<[^>]*role="status"[^>]*>Polyforge&#x27;s config does not set a language yet\.</);
  assert.doesNotMatch(unset, /Polyforge uses a different language/);
  assert.match(unset, /Sync to Polyforge/);
  assert.ok(findSyncButton(AgentLanguagePfStatus(props({ language: "zh-CN", polyforge: { language: "en", source: "default" } }))), "the sync button is shown");
  const set = render({ language: "zh-CN", polyforge: { language: "en", source: "file" } });
  assert.match(set, /<[^>]*role="status"[^>]*>Polyforge uses a different language\.</);
  assert.doesNotMatch(set, /does not set a language yet/);
  assert.match(set, /Sync to Polyforge/);
});

test("the three locales carry the Agent language Polyforge strings", () => {
  const keys = ["settings.agentLanguagePolyforge", "settings.agentLanguagePolyforgeMismatch", "settings.agentLanguagePolyforgeSync", "settings.agentLanguagePolyforgeUnset"];
  for (const locale of [enLocale, zhCNLocale, zhTWLocale]) {
    for (const key of keys) {
      const message = locale.messages[key];
      assert.equal(typeof message, "string", `${locale.id} ${key}`);
      assert.notEqual(message.trim(), "", `${locale.id} ${key}`);
    }
    assert.match(locale.messages["settings.agentLanguagePolyforge"], /\{language\}/, `${locale.id} keeps the {language} placeholder`);
  }
});
