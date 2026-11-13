import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// the Host pane's pf install / update card. The face is
// rendered from its state (PolyforgeInstallView); the card's wiring — the
// one POST, the stream, the write-only key, the separate force — is pinned
// on its source, like the rest of Settings › Polyforge.

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { PolyforgeInstallView } = await jiti.import("./PolyforgeInstallCard.tsx");
const source = await readFile(new URL("./PolyforgeInstallCard.tsx", import.meta.url), "utf8");
const configSource = await readFile(new URL("./PolyforgeConfig.tsx", import.meta.url), "utf8");

const h = React.createElement;
const noop = () => {};
const decode = (html) => html.replace(/&quot;/g, '"').replace(/&#x27;/g, "'").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">");
const text = (html) => decode(html.replace(/<[^>]+>/g, " ")).replace(/\s+/g, " ").trim();

const versions = (patch = {}) => ({
  repo: "example-org/forge", pfPresent: true, installed: "v0.2.5", latest: "v0.2.5", latestError: null,
  running: "v0.2.5", daemonReachable: true, daemonError: null, jobs: [], piWeb: "0.2.5", needsConfig: false, ...patch,
});
const run = (patch = {}) => ({
  id: "r1", mode: "update", force: false, state: "running", startedAt: "2026-11-09T00:00:00.000Z", endedAt: null,
  exitCode: null, lineCount: 2, error: null, refusal: null, ...patch,
});
function view(props = {}) {
  return renderToStaticMarkup(h(I18nProvider, null, h(PolyforgeInstallView, {
    versions: versions(), readError: null, streamError: null, run: null, lines: [], host: "", apiKey: "", problem: null, posting: false, confirmingForce: false,
    onHost: noop, onApiKey: noop, onStart: noop, onAskForce: noop, onForce: noop, onCancelForce: noop, ...props,
  })));
}

test("AC1: installed, latest on the channel, the running daemon, the job count and Pi Web are all shown", () => {
  const html = view({ versions: versions({ jobs: [{ slug: "demo#7", status: "running" }] }) });
  const shown = text(html);
  for (const needle of ["Installed pf", "Latest on example-org/forge", "Running daemon", "Running jobs", "Pi Web", "v0.2.5", "0.2.5"]) {
    assert.ok(shown.includes(needle), `missing ${needle}: ${shown}`);
  }
  assert.match(html, /data-pf-install-jobs="1"/);
  assert.ok(shown.includes("demo#7 (running)"));
  assert.match(html, /data-pf-install-flag="jobs"/, "running jobs say an update will be refused");
  assert.doesNotMatch(html, /data-pf-install-flag="(update|daemon-stale|pi-web)"/, "agreeing versions raise no flag");
});

test("AC1: every disagreement is named in words — newer release, a daemon on the old binary, Pi Web out of sync", () => {
  const html = view({ versions: versions({ latest: "v0.2.6", running: "v0.2.4", piWeb: "0.2.3" }) });
  const shown = text(html);
  assert.match(html, /data-pf-install-flag="update"/);
  assert.ok(shown.includes("A newer pf is on the channel: v0.2.6 (installed: v0.2.5)"));
  assert.match(html, /data-pf-install-flag="daemon-stale"/);
  assert.ok(shown.includes("The running daemon is still v0.2.4 while pf v0.2.5 is installed"));
  assert.match(html, /data-pf-install-flag="pi-web"/);
  assert.ok(shown.includes("Pi Web 0.2.3 is not in sync with pf v0.2.5"));
});

test("AC1: a down daemon and an unreadable channel say why; without pf the daemon is not applicable", () => {
  let shown = text(view({ versions: versions({ daemonReachable: false, daemonError: "pf: daemon not reachable", running: null, latest: null, latestError: "gh is not available" }) }));
  assert.ok(shown.includes("not answering: pf: daemon not reachable"));
  assert.ok(shown.includes("unknown: gh is not available"));
  shown = text(view({ versions: versions({ pfPresent: false, installed: null, running: null, daemonReachable: null }) }));
  assert.ok(shown.includes("not installed"));
  assert.ok(shown.includes("not applicable"));
  assert.ok(shown.includes("Install pf"), "pf absent: the button installs");
});

test("AC3: a first install asks for the host and a write-only key; an update asks for neither", () => {
  const first = view({ versions: versions({ pfPresent: false, installed: null, needsConfig: true }) });
  assert.match(first, /data-pf-install-credentials/);
  assert.match(first, /<input aria-label="aihub host"/);
  assert.match(first, /<input type="password" autoComplete="off" aria-label="API key"[^>]*value=""/);
  assert.ok(text(first).includes("on stdin only"));
  assert.doesNotMatch(view(), /data-pf-install-credentials/);
});

test("AC3: an installed pf with no cli.toml key asks for the host and the key too — the route and install.sh need them whenever cli.toml has no api_key", () => {
  const installed = view({ versions: versions({ needsConfig: true }) });
  assert.match(installed, /data-pf-install-credentials/);
  assert.match(installed, /<input aria-label="aihub host"/);
  assert.match(installed, /<input type="password" autoComplete="off" aria-label="API key"[^>]*value=""/);
  assert.match(installed, /<button type="button"(?! disabled)[^>]*data-pf-install-start[^>]*>Update pf<\/button>/, "the update stays startable");
  // a reinstall onto an existing cli.toml key needs neither
  assert.doesNotMatch(view({ versions: versions({ pfPresent: false, installed: null, needsConfig: false }) }), /data-pf-install-credentials/);
  // the start's browser-side check follows the same rule, pf installed or
  // not: no request without a host and a key of at least 8 characters
  assert.match(source, /\} else if \(versions\?\.needsConfig\) \{\s*const trimmed = host\.trim\(\);\s*if \(!trimmed\) \{\s*setProblem\(t\("polyforge\.install\.needHost"\)\);\s*return;\s*\}\s*if \(typedKey\.length < 8\) \{\s*setProblem\(t\("polyforge\.install\.needKey"\)\);\s*return;\s*\}\s*body\.host = trimmed;\s*body\.apiKey = typedKey;/);
  assert.match(source, /\{versions\?\.needsConfig && !running && \(\s*<div className="polyforge-install-credentials"/);
  assert.doesNotMatch(source, /pfPresent[^\n]*needsConfig|needsConfig[^\n]*pfPresent/, "pf's presence never decides whether the credentials are asked for");
});

test("the line naming tools/install.sh shows only while an install or update is relevant", () => {
  const hint = (props) => /data-pf-install-hint/.test(view(props)) && text(view(props)).includes("tools/install.sh");
  assert.equal(/data-pf-install-hint|tools\/install\.sh/.test(view()), false, "a healthy, current pf with its daemon on it shows no install.sh line");
  assert.equal(hint({ versions: versions({ jobs: [{ slug: "demo#7", status: "running" }], piWeb: "0.2.4" }) }), false, "running jobs or Pi Web out of sync alone need no install.sh run");
  assert.equal(hint({ versions: null }), false, "unknown versions prove nothing");
  assert.equal(hint({ versions: versions({ pfPresent: false, installed: null, running: null, daemonReachable: null }) }), true, "pf absent");
  assert.equal(hint({ versions: versions({ latest: "v0.2.6" }) }), true, "a newer release on the channel");
  assert.equal(hint({ versions: versions({ running: "v0.2.4" }) }), true, "the daemon on an older binary");
  for (const state of ["running", "refused", "failed"]) {
    assert.equal(hint({ versions: null, run: run({ state }) }), true, `a ${state} run`);
  }
  assert.equal(hint({ run: run({ state: "succeeded", exitCode: 0 }) }), false, "a succeeded run onto a current pf");
});

test("AC2: a running install disables the button, says why, and shows its output line by line", () => {
  const html = view({ run: run(), lines: ["== 1/5 prepare", "   release channel: example-org/forge"] });
  assert.match(html, /<button type="button" disabled=""[^>]*data-pf-install-start/);
  assert.ok(text(html).includes("An install is running."));
  assert.match(html, /data-pf-install-conclusion="running"/);
  assert.match(html, /<pre[^>]*role="log"[^>]*>== 1\/5 prepare\n {3}release channel: example-org\/forge<\/pre>/);
});

test("AC2: a clear conclusion — succeeded, or failed with install.sh's reason", () => {
  assert.ok(text(view({ run: run({ state: "succeeded", exitCode: 0 }) })).includes("Succeeded: install.sh finished."));
  const failed = view({ run: run({ state: "failed", exitCode: 1, error: "SHA mismatch on pf-linux-amd64" }) });
  assert.match(failed, /data-pf-install-conclusion="failed"/);
  assert.ok(text(failed).includes("Failed: SHA mismatch on pf-linux-amd64"));
  assert.doesNotMatch(failed, /data-pf-install-force/, "a failure offers no force");
});

test("AC2: a refusal shows install.sh's reason and jobs; the force needs a second, separate confirmation naming its cost", () => {
  const refusal = { reason: "the daemon is running jobs; Upgrade refused", jobs: ["demo#7 (running)", "demo#9 (recovering)"] };
  const refused = view({ run: run({ state: "refused", exitCode: 3, refusal }) });
  const shown = text(refused);
  assert.ok(shown.includes("Refused by install.sh: the daemon is running jobs; Upgrade refused"));
  assert.ok(shown.includes("demo#7 (running)") && shown.includes("demo#9 (recovering)"));
  assert.match(refused, /data-pf-install-force(?!-)/, "the first step only asks");
  assert.doesNotMatch(refused, /data-pf-install-force-go/, "no force control before the confirmation opens");
  // the confirmation lists the jobs running NOW (the re-read versions), not
  // the ones the refusal named
  const now = versions({ jobs: [{ slug: "demo#11", status: "running" }] });
  const confirming = view({ versions: now, run: run({ state: "refused", exitCode: 3, refusal }), confirmingForce: true });
  assert.match(confirming, /role="alertdialog" aria-label="Force the update \(PF_INSTALL_FORCE=1\)\?"/);
  assert.ok(text(confirming).includes("the agent tree of every running job is terminated"));
  const dialog = text(confirming.slice(confirming.indexOf('role="alertdialog"')));
  assert.ok(dialog.includes("Jobs running now, which the shutdown ends: demo#11 (running)"), dialog);
  assert.ok(!dialog.includes("demo#7"), "the refusal's job list is not passed off as the current one");
  assert.match(confirming, /data-pf-install-force-go/);
  assert.ok(text(confirming).includes("Cancel"));
  const idle = text(view({ versions: versions({ jobs: [] }), run: run({ state: "refused", exitCode: 3, refusal }), confirmingForce: true }));
  assert.ok(idle.includes("No job is running now: a plain update no longer needs the force."));
  const down = text(view({ versions: versions({ daemonReachable: false, jobs: [] }), run: run({ state: "refused", exitCode: 3, refusal }), confirmingForce: true }));
  assert.ok(down.includes("whether jobs run is unknown"));
});

test("AC5: a refused output stream leaves the running state with a reason and frees the button", () => {
  const lost = view({ run: run(), streamError: "Stopped following the install: E2E", lines: ["== 1/5 prepare"] });
  assert.match(lost, /data-pf-install-conclusion="lost"/);
  assert.ok(text(lost).includes("Stopped following the install: E2E"));
  assert.doesNotMatch(lost, /<button type="button" disabled=""[^>]*data-pf-install-start/, "the page no longer waits on a run it cannot follow");
  assert.ok(!text(lost).includes("An install is running."));
  assert.match(source, /source\.onerror = \(\) => \{\s*if \(source\.readyState !== EventSource\.CLOSED\) return;/, "only a closed (refused) stream counts, a reconnect does not");
  assert.match(source, /setStreamError\(t\("polyforge\.install\.streamLost"\)\);/);
});

test("the card's wiring: one POST, the events stream, the key cleared before the request, the force only from its confirmation", () => {
  assert.match(source, /fetch\("\/api\/polyforge\/install", \{\s*method: "POST"/);
  assert.match(source, /new EventSource\(`\/api\/polyforge\/install\/events\?after=\$\{cursorRef\.current\}`\)/);
  const clear = source.indexOf('setApiKey("");');
  const post = source.indexOf('fetch("/api/polyforge/install", {');
  const check = source.indexOf('setProblem(t("polyforge.install.needHost"));');
  assert.ok(clear > 0 && clear < check && check < post, "the key leaves the form on every submit, before the browser-side check and the request");
  assert.match(source, /const typedKey = apiKey;\s*setApiKey\(""\);/);
  assert.match(source, /body\.force = true;\s*body\.refusedRun = run\?\.id;/, "a force names the refusal it answers");
  assert.match(source, /const askForce = async \(\) => \{\s*setProblem\(null\);\s*await read\(true\);\s*setConfirmingForce\(true\);/, "the confirmation opens on a fresh read of the jobs");
  assert.match(source, /onForce=\{\(\) => void start\(true\)\}/, "the force is the confirmation's own action");
  assert.equal((source.match(/start\(true\)/g) ?? []).length, 1, "nothing else forces");
  assert.doesNotMatch(source, /localStorage|sessionStorage|console\./, "the key is never stored or logged");
  assert.doesNotMatch(source, /setInterval|setTimeout/, "no poll: the stream carries the output");
  assert.match(source, /useEffect\(\(\) => \(\) => \{ sourceRef\.current\?\.close\(\); \}, \[\]\)/, "leaving the page closes only the stream");
});

test("the Host pane hosts the card, re-detects when a run ends, and 重新检测 refreshes the card", () => {
  assert.match(configSource, /<PolyforgeInstallCard active=\{active\} refreshKey=\{installRefresh\} onFinished=\{onInstallFinished\} \/>/);
  assert.match(configSource, /void runDetect\(true\); void runCellsRead\(\); setInstallRefresh\(\(n\) => n \+ 1\);/, "重新检测 re-reads the card past its caches");
  assert.match(source, /if \(seenRefresh\.current === refreshKey\) return;\s*seenRefresh\.current = refreshKey;\s*void sync\(true\);/);
  assert.match(configSource, /const onInstallFinished = useCallback\(\(\) => \{\s*void runDetect\(true\);\s*void runCellsRead\(\);/);
});

test("every install string exists in all three locales", async () => {
  const keys = [...source.matchAll(/t\("(polyforge\.install\.[A-Za-z]+)"/g)].map((match) => match[1]);
  assert.ok(keys.length > 20);
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const messages = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of new Set(keys)) assert.match(messages, new RegExp(`"${key.replace(/\./g, "\\.")}": "`), `${locale} must carry ${key}`);
  }
});
