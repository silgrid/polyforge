import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./PolyforgeConfig.tsx", import.meta.url), "utf8");
const panelSource = await readFile(new URL("./SettingsPanel.tsx", import.meta.url), "utf8");

test("detection runs once per activation (StrictMode-resilient), never on a poll", () => {
  // One detect when the section becomes active, guarded against React
  // StrictMode's simulated remount (setup → cleanup → setup in dev) so the
  // cadence is exactly one request per open.
  assert.match(source, /const detectRanForActive = useRef\(false\)/);
  assert.match(
    source,
    /useEffect\(\(\) => \{\s*\n\s*if \(!active\) \{\s*\n\s*detectRanForActive\.current = false;\s*\n\s*return;\s*\n\s*\}\s*\n\s*if \(detectRanForActive\.current\) return;\s*\n\s*detectRanForActive\.current = true;\s*\n\s*void runDetect\(false\);/,
    "the activation effect fetches once, non-force",
  );
  // No timers anywhere in the section (R10): the dock's polling hook is
  // deliberately not reused (no import, no status route).
  assert.doesNotMatch(source, /setInterval|setTimeout/);
  assert.doesNotMatch(source, /from "@\/hooks\/usePolyforge"/, "the polling dock hook is not reused");
  assert.doesNotMatch(source, /\/api\/polyforge\/status/, "the section never touches the dock's status route");
});

test("the re-detect control bypasses the 60s presence cache with ?force=1", () => {
  assert.match(source, /\/api\/polyforge\/detect\$\{force \? "\?force=1" : ""\}/);
  // the same click also re-reads the Cell list beside it;
  // and the install card's versions, past their caches.
  assert.match(source, /onClick=\{\(\) => \{ void runDetect\(true\); void runCellsRead\(\); setInstallRefresh\(\(n\) => n \+ 1\); \}\}/, "the re-detect button forces past the cache");
  assert.match(source, /disabled=\{!detect\}/, "re-detect waits for a first detection");
});

test("the toggle writes only through the existing /api/plugins global path", () => {
  assert.match(
    source,
    /fetch\("\/api\/plugins", \{\s*\n\s*method: "POST",\s*\n\s*headers: \{ "Content-Type": "application\/json" \},\s*\n\s*body: JSON\.stringify\(\{ action: enabled \? "enable" : "disable", source: sourceState\.source, scope: "global", cwd \}\),/,
    "the exact PluginsConfig package-switch call shape",
  );
  // The package switch is one POST; the configuration panel adds its own
  // write/reload/rollback POSTs.
  assert.ok((source.match(/method: "POST"/g) ?? []).length >= 1, "the package switch POST exists");
  // Auto re-detect after the write settles (non-force).
  assert.match(source, /await runDetect\(false\)/, "detection re-runs automatically after the toggle");
  // The source is resolved from the listing join, never from the detect
  // response (which carries no paths).
  assert.match(
    source,
    /pkg\.scope === "global" && pkg\.packageName === "polyforge"/,
    "the source joins the listing on scope + packageName",
  );
  assert.match(source, /\/api\/plugins\?cwd=\$\{encodeURIComponent\(cwd\)\}/, "the listing is the existing GET /api/plugins");
});

test("no other write exists: no cli.toml, no Claude config, no install execution of its own", () => {
  // The section imports no server-side module at all (type-only imports) —
  // it cannot touch cli.toml, Claude Code config or any install by itself;
  // the only write it can issue is the one /api/plugins POST above. An
  // install runs only through the install card's route (
  // components/PolyforgeInstallCard.test.mjs).
  assert.doesNotMatch(source, /from "node:/, "no node built-in import (config files are untouchable)");
  assert.match(source, /import type \{ PolyforgeDetectStatus \} from "@\/lib\/polyforge-detect";/, "the server detection module is imported as a type only");
  assert.doesNotMatch(source, /spawn|execFile/);
  assert.doesNotMatch(source, /method: "(PUT|DELETE|PATCH)"/);
  assert.match(source, /\/api\/polyforge\/config/);
  // The install command is copyable text only.
  assert.match(source, /navigator\.clipboard\.writeText\(installCommand\)/);
});

test("the pi package switch renders only when the skill is installed; disabled reasons are visible text", () => {
  // The switch block is gated on piSkillInstalled (未安装 shows no toggle).
  assert.match(source, /\{detect\?\.piSkillInstalled && \(\s*\n\s*<ConfigSwitch/);
  // Disabled = no project or no resolved source, with the reason as visible
  // text under the switch's line (describedBy), never only a tooltip.
  assert.match(source, /disabled=\{!cwd \|\| !sourceState\.source\}/);
  assert.match(source, /describedBy=\{switchReason \? switchReasonId : undefined\}/);
  assert.match(source, /<span id=\{switchReasonId\} className="config-scope-switch-reason">/, "the reason renders as visible text");
  const reasons = [
    "polyforge.settings.switchNeedsProject",
    "polyforge.settings.switchResolving",
    "polyforge.settings.switchNoPackage",
  ];
  for (const key of reasons) {
    assert.match(source, new RegExp(`t\\("${key.replace(/\./g, "\\.")}"\\)`), `the section carries ${key}`);
  }
});

test("the section reuses the shared SettingsUi blocks and every word is a prop", () => {
  for (const block of ["ConfigButton", "ConfigDetailGrid", "ConfigDetailGridRow", "ConfigDetailStack", "ConfigFooter", "ConfigFooterStatus", "ConfigSectionTitle", "ConfigSwitch"]) {
    assert.match(source, new RegExp(`<${block}[\\s>]`), `the section renders through ${block}`);
  }
  // The section label reuses the existing polyforge.title key (no new label).
  assert.match(source, /t\("polyforge\.title"\)/);
});

test("the panel hosts the section without a project and re-detects on every open", async () => {
  assert.match(panelSource, /\{ id: "polyforge", label: t\("polyforge\.title"\) \}/);
  // Which sections need a project is decided in lib/settings-navigation.ts; polyforge is not one.
  const { settingsSectionRequiresProject } = await import("../lib/settings-navigation.ts");
  assert.equal(settingsSectionRequiresProject("polyforge"), false);
  assert.match(panelSource, /<PolyforgeConfig cwd=\{cwd\} active=\{section === "polyforge"\} \/>/);
  // Not cwd-gated: the section host renders without a selected project.
  const hostLine = panelSource.slice(panelSource.indexOf('sectionHost("polyforge"'));
  assert.doesNotMatch(hostLine.split("\n")[0], /cwd &&/, "the polyforge section host is not project-gated");
});

test("the install command and every section string exist in all three locales", async () => {
  const installNeedles = ["tools/install.sh", "raw.githubusercontent.com"];
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of [
      "polyforge.settings.notInstalled",
      "polyforge.settings.installCommandTitle",
      "polyforge.settings.installCommand",
      "polyforge.settings.copyInstall",
      "polyforge.settings.redetect",
      "polyforge.settings.version",
      "polyforge.settings.belowMinimum",
      "polyforge.settings.line",
      "polyforge.settings.line.internal",
      "polyforge.settings.line.public",
      "polyforge.settings.line.unknown",
      "polyforge.settings.piSkill",
      "polyforge.settings.claudeSymlink",
      "polyforge.settings.claudeHook",
      "polyforge.settings.state.enabled",
      "polyforge.settings.state.disabled",
      "polyforge.settings.state.notInstalled",
      "polyforge.settings.state.present",
      "polyforge.settings.state.absent",
      "polyforge.settings.daemon",
      "polyforge.settings.daemonReachable",
      "polyforge.settings.daemonUnreachable",
      "polyforge.settings.daemonNotApplicable",
      "polyforge.settings.packageSwitch",
      "polyforge.settings.switchNeedsProject",
      "polyforge.settings.switchResolving",
      "polyforge.settings.switchNoPackage",
      "polyforge.settings.packageToggled",
    ]) {
      assert.match(text, new RegExp(`"${key}": "`), `${locale} must carry ${key}`);
    }
    // The install command is the fixed install.sh usage string, identical in
    // every locale, placeholders only (never a real key).
    const command = text.match(/"polyforge\.settings\.installCommand": "((?:[^"\\]|\\.)*)"/)?.[1]?.replace(/\\"/g, '"') ?? "";
    for (const needle of installNeedles) {
      assert.ok(command.includes(needle), `${locale}'s install command must carry ${needle}`);
    }
    assert.ok(command.includes("<api-key>"), "the api key stays a placeholder");
    assert.doesNotMatch(command, /sk-|ghp_[A-Za-z0-9]/, "no real credential shape in the install text");
  }
});

test("the configuration panel reads through pf and keeps api_key write-only", () => {
  assert.match(source, /\/api\/polyforge\/config\?cwd=/);
  assert.match(source, /type=\{item\.key === "api_key" \? "password"/);
  assert.match(source, /item\.key === "api_key" \? \(item\.set \? "<set>" : "<unset>"\)/, "api_key never renders a value");
  assert.match(source, /expect_state/);
  assert.match(source, /\/api\/polyforge\/config\/history/);
  assert.match(source, /\/api\/polyforge\/config\/reload/);
});

test("the form enforces pf's schema client-side so 0 and 9 are unreachable", () => {
  // The value is validated against the key's pf schema BEFORE any write.
  assert.match(source, /configValueError\(item\.key, value, item\.schema\)/);
  // The guard lives in the browser-safe schema module: a value import of the
  // server read module would pull node:child_process into the client bundle.
  // the same browser-safe module also carries the step-key guard.
  // the key-set constants come from that module too (one fact for the form and the route).
  assert.match(source, /import \{[^}]*\bconfigValueError,[^}]*\bstepValueError,[^}]*\} from "@\/lib\/polyforge-config-schema";/, "the guard comes from the browser-safe module");
  assert.doesNotMatch(source, /import \{ configValueError \} from "@\/lib\/polyforge-config";/, "never value-import the server read module");
  assert.match(source, /min=\{item\.schema\?\.min\}/);
  assert.match(source, /max=\{item\.schema\?\.max\}/);
  // And the read snapshot (values, api_key as {set}) rides expect_state.
  assert.match(source, /function expectedValues\(/);
  assert.match(source, /file_sha256: config\.file\?\.sha256 \?\? null, values: expectedValues\(config\.items\)/);
});

test("the daemon column says the daemon is not running, and only the three live keys carry it", () => {
  assert.match(source, /isDaemonColumnKey/);
  assert.match(source, /polyforge\.settings\.configDaemonDown/);
  assert.match(source, /config\?\.daemonReachable === false/);
});

test("visible reasons: capability floor and an incomplete workflow read are text; no false read-only claim", () => {
  assert.match(source, /config\?\.capable === false/);
  assert.match(source, /configReason/);
  // (review s2#1): the "not mapped … workspace settings are read-only" line is gone —
  // nothing in a Cell's pane depends on the settings cwd's mapping, and its 工作区 card is editable
  assert.doesNotMatch(source, /config\.mapped|configUnmapped/);
  // the step facts moved to the workflow card, whose failed or
  // daemon-less read is a visible alert line (the old steps_complete note's role).
  assert.match(source, /\{readError && <p role="alert" className="config-add-source-error">\{readError\}<\/p>\}/);
  assert.match(source, /polyforge\.settings\.configMachineScope/);
});

test("one cli.toml card holds the top-level, [daemon] and [summarize] zones, each headed by its literal section; the 工作区 card keeps the mapping", () => {
  // (review s17#2): restated — the card groups every key but the Host-only ones (their one surface is the Host region)
  assert.match(source, /const cards = config\?\.capable \? configCards\(groupConfigItems\(\(config\.items \?\? \[\]\)\.filter\(\(item\) => !HOST_ONLY_KEYS\.includes\(item\.key\)\)\)\) : null;/);
  // plus a named Cell's [model] zone (pf lists model.source for named Cells only)
  assert.match(source, /cli: \(\["flat", "daemon", "model", "summarize"\] as const\)\.map\(\(id\) => \(\{ id, items: itemsOf\(id\) \}\)\)/, "the zones, top-level first");
  assert.match(source, /workspace: itemsOf\("workspace"\),/, "the 工作区 card holds the mapping rows only");
  // Literal file sections, never translated — after the Cell's own file name.
  assert.match(source, /flat: "",\s*\n\s*daemon: " \[daemon\]",\s*\n\s*model: " \[model\]",\s*\n\s*summarize: " \[summarize\]",/);
  assert.match(source, /<p className="polyforge-config-zone-title">\{`\$\{file\}\$\{ZONE_SECTION\[zone\.id\]\}`\}<\/p>/);
  assert.match(source, /const file = polyforgeCellFileName\(profile\);/, "the file is the Cell's own (cli.toml / cli-<profile>.toml)");
  assert.match(source, /<div key=\{zone\.id\} className=\{`polyforge-config-zone is-\$\{zone\.id\}`\}>/);
  // One card: one section opens before the zone loop, the loop lives inside it.
  const cli = source.slice(source.indexOf('className="polyforge-config-card polyforge-config-cli"'), source.indexOf("the connection-key confirmation renders"));
  assert.ok(cli.length > 0, "the cli.toml card exists");
  assert.equal((cli.match(/<section /g) ?? []).length, 0, "no second card inside the cli.toml card");
  assert.match(cli, /\{cards\.cli\.map\(\(zone\) => \{/);
  assert.match(cli, /<p className="polyforge-config-card-title">\{file\}<\/p>/, "the card is titled by the Cell's file name");
  assert.match(cli, /<p className="polyforge-config-card-hint">\{t\("polyforge\.settings\.configGroupFlatHint", \{ file \}\)\}<\/p>/);
  assert.match(cli, /\{hasDaemon && <p className="polyforge-config-card-hint">\{t\("polyforge\.settings\.configGroupDaemonHint"\)\}<\/p>\}/);
  assert.doesNotMatch(cli, /workspaceSectionPath/, "the mapping stays in the 工作区 card");
  assert.match(cli, /\{memoryPolicyPaths\.map\(\(\[path, entries\]\) => \(/, "the read-only memory_policy blocks render inside the cli.toml card");
  // The 工作区 card keeps its #216 title/hint.
  assert.match(source, /aria-label=\{t\("polyforge\.settings\.workspaceTitle"\)\}/);
  assert.match(source, /t\("polyforge\.settings\.configGroupWorkspaceHint", \{ file \}\)/);
  assert.doesNotMatch(source, /configGroupSummarize|configGroupWorkspace"|configGroupDaemon"/, "no summarize card, no old mapping-card title, no separate [daemon] card");
  // The daemon column stays the three live keys.
  assert.match(source, /function isDaemonColumnKey\(key: string\): boolean \{\s*\n\s*return key === "max_concurrent_jobs" \|\| key === "step_timeout" \|\| key === "stall_window";/);
});

test("owner-intended change: cli.toml zones are de-boxed subheadings over one divider while the value column keeps one page-wide x", async () => {
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  const zone = css.slice(css.indexOf(".polyforge-config-zone {"), css.indexOf("}", css.indexOf(".polyforge-config-zone {")));
  assert.doesNotMatch(zone, /margin-left:/, "no zone indentation ( de-boxed the zones)");
  assert.doesNotMatch(zone, /background:/, "no zone background tint (no grey box in a grey card)");
  assert.match(zone, /border-top: 1px solid var\(--border\);/, "one thin divider heads the zone");
  assert.doesNotMatch(css, /--polyforge-zone-inset/, "the zone inset is gone");
  assert.match(css, /\.polyforge-config-zone \.polyforge-config-grid \{[^}]*grid-template-columns: var\(--polyforge-col-key\) var\(--polyforge-col-class\) var\(--polyforge-col-value\);/, "the zone keeps the page's key track");
  assert.match(css, /\.polyforge-config-zone-title \{/);
});

test("each card declares only its effective columns and the page shares one column system", () => {
  for (const label of ["configColKey", "configColClass", "configColFile", "configColDaemon"]) {
    assert.match(source, new RegExp(`t\\("polyforge\\.settings\\.${label}"\\)`), `grid header ${label}`);
  }
  // Only the daemon card declares the daemon column: no header and no cell in
  // every other card (the #88 five-cells-in-every-row rule is reversed).
  assert.match(source, /const hasDaemon = zone\.id === "daemon";/);
  assert.match(source, /const hasDaemon = zone === "daemon";/, "a row's daemon cell is decided by its zone");
  assert.match(source, /className=\{`polyforge-config-grid/);
  assert.match(source, /hasDaemon \? " has-daemon" : ""/);
  assert.match(source, /\{hasDaemon && <span>\{t\("polyforge\.settings\.configColDaemon"\)\}<\/span>\}/);
  assert.match(source, /const daemonCell = hasDaemon && isDaemonColumnKey\(item\.key\);/);
  assert.match(source, /\{daemonCell && \(/, "the daemon cell is per group and per key");
  assert.doesNotMatch(source, /<span className="polyforge-config-daemon">\s*\{isDaemonColumnKey/, "no unconditional daemon placeholder cell");
  // The row still renders key | class | value unconditionally, in that column
  // order, so no cell collapses when a row is read-only (the #88 intent kept).
  // Only the daemon cell may be conditional, and only behind daemonCell.
  const row = source.slice(source.indexOf("<div key={item.key} className={`polyforge-config-row"), source.indexOf("</div>", source.indexOf("<div key={item.key} className={`polyforge-config-row")));
  let at = 0;
  for (const cell of ["polyforge-config-key", "polyforge-config-class", "polyforge-config-file"]) {
    const index = row.indexOf(`<span className="${cell}">`, at);
    assert.ok(index > at, `${cell} renders unconditionally, in column order`);
    // The line opening the cell is the cell itself, not a `cond && (` wrapper,
    // and no conditional opens on an earlier line either.
    const before = row.slice(row.lastIndexOf("\n", index), index);
    assert.match(before, /^\n\s*$/, `${cell} is not wrapped in a condition`);
    assert.doesNotMatch(row.slice(0, index), /&&\s*\(/, `${cell} is not behind a condition`);
    at = index;
  }
  assert.match(source, /<span className="polyforge-config-badge" title=\{item\.class\}>\{reloadClassLabel\(item\.class\)\}<\/span>/, "the reload class is a localized badge");
  assert.match(source, /const unset = t\("polyforge\.settings\.configUnset"\)/);
  // renderValue still renders the unset placeholder for unset file values and
  // empty project/model lists; the WI removed only the whole-column daemon one.
  assert.match(source, /<span className="polyforge-config-empty">\{unset\}<\/span>/, "unset values show the placeholder");
  assert.match(source, /is-readonly/, "read-only rows are muted");
});

test("the Save action sits beside its input, and model lists render as chips", () => {
  // The write control and the Save button share one cell; the old far-right
  // actions column is gone.
  assert.match(source, /<span className="polyforge-config-write">\s*\n\s*<input/, "the input opens the write cell");
  assert.match(source, /<span className="polyforge-config-write-row">/);
  assert.match(source, /<ConfigButton size="small" disabled=\{writeBusy !== null\} onClick=\{\(\) => void save\(item\)\}>/);
  assert.doesNotMatch(source, /polyforge-config-actions/);
  // summarize.models is a chip list with an add input, never String(array).
  assert.match(source, /function isModelListKey\(item: PolyforgeConfigItem\): boolean \{/);
  assert.match(source, /kind === "model-list" \|\| item\.key === "summarize\.models"/);
  // the chip label renders its token with separator <wbr>s.
  assert.match(source, /<span className="polyforge-config-chip-label"><BreakableToken text=\{model\} \/><\/span>/);
  assert.match(source, /polyforge\.settings\.configAddModel/);
  assert.match(source, /polyforge\.settings\.configRemoveModel/);
  // The save path still hands a comma string to formValue, which splits it.
  assert.match(source, /isModelListKey\(item\)\s*\n\s*\? \[/);
  assert.match(source, /\.join\(","\)/);
});

test("a workspace row shows the path and project chips, never the raw TOML header", () => {
  assert.match(source, /workspaceSectionPath\(item\.key\)/);
  assert.match(source, /shortenPath\(workspacePath\)/);
  assert.match(source, /const projects = workspaceProjects\(item\.value\);/);
  assert.match(source, /\{projects\.map\(\(project\) =>/);
  assert.match(source, /<span key=\{project\} className="polyforge-config-chip"><BreakableToken text=\{project\} \/><\/span>/);
  assert.match(source, /aria-label=\{t\("polyforge\.settings\.configProjects"\)\}/);
  // The raw key renders only when the header did not parse (a visible degradation).
  assert.match(source, /workspacePath !== null\s*\n\s*\? <span className="polyforge-config-workspace-path"><BreakableToken text=\{shortenPath\(workspacePath\)\} \/>/);
});

test("the grid CSS shares fixed tracks and stacks on phones", async () => {
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  for (const cls of ["polyforge-config-card", "polyforge-config-grid", "polyforge-config-badge", "polyforge-config-empty", "polyforge-config-chips", "polyforge-config-chip", "polyforge-config-write", "polyforge-config-workspace-path"]) {
    assert.match(css, new RegExp(`\\.${cls}\\b`), `${cls} is styled`);
  }
  // Page-level shared tracks: key/class are fixed, so the value column starts
  // at the same x in every card regardless of a daemon column.
  assert.match(css, /--polyforge-col-key: \d+px;/);
  assert.match(css, /--polyforge-col-class: \d+px;/);
  assert.match(css, /\.polyforge-config-grid \{[^}]*grid-template-columns: var\(--polyforge-col-key\) var\(--polyforge-col-class\) var\(--polyforge-col-value\);/);
  assert.match(css, /\.polyforge-config-grid\.has-daemon \{[^}]*var\(--polyforge-col-daemon\);/);
  assert.match(css, /\.polyforge-config-row \{[^}]*grid-template-columns: subgrid;/);
  // the page sits in a split view's detail pane, so its narrow
  // layout follows the pane's width (a container query), not the viewport's.
  assert.match(css, /\.settings-polyforge \{[^}]*container-type: inline-size;/, "the page is the query container");
  assert.match(css, /@container \(max-width: 640px\) \{[^@]*\.polyforge-config-grid-header \{ display: none; \}/);
});

test("at <=640px the key and class badge share a row and the values span the row below", async () => {
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  // a container query on the page (any pane this narrow), not a viewport query.
  // The anchor is the comment's own word, not the ticket reference beside it
  // (that reference is not part of every copy of this file).
  const at = css.indexOf("@container (max-width: 640px) {\n  /* Phones");
  assert.ok(at >= 0, "the config page's phone block is anchored");
  const phone = css.slice(at);
  const block = phone.slice(0, phone.indexOf("\n}\n"));
  assert.ok(block.length > 0, "the config page's phone block exists");
  assert.match(block, /\.polyforge-config-row,\s*\.polyforge-config-row\.no-daemon \{\s*grid-template-columns: minmax\(0, 1fr\) auto;/, "two columns: key, then the badge");
  assert.match(block, /\.polyforge-config-class \{ justify-self: end; \}/, "the badge sits at the row's right end");
  assert.match(block, /\.polyforge-config-badge \{ max-width: none; \}/, "the badge is never squeezed");
  assert.match(block, /\.polyforge-config-file,\s*\.polyforge-config-daemon,\s*\.polyforge-config-row\.no-daemon \.polyforge-config-file \{ grid-column: 1 \/ -1; \}/, "values span the row below");
  assert.doesNotMatch(block, /\.polyforge-config-row,\s*\.polyforge-config-row\.no-daemon \{\s*grid-template-columns: minmax\(0, 1fr\);/, "the config rows never stack to one column");
  // owner-intended change P4: the label+control rows
  // stack label over a full-width control.
  assert.match(block, /\.polyforge-config-cards \.config-detail-grid \{\s*grid-template-columns: minmax\(0, 1fr\);/, "the Jobs preferences stack");
  assert.match(block, /\.polyforge-workflow-editor > \.polyforge-config-write > input \{ flex: 1 1 100%; \}/, "the workflow chain-add control spans the row");
});

test("the read-only declared-candidates card is gone, region and strings included", async () => {
  // Replaces the #88/#212 candidates-card pins: the workflow card carries the step facts.
  assert.doesNotMatch(source, /polyforge-config-steps|polyforge-config-candidate-(group|step|project)/, "no candidates card markup");
  assert.doesNotMatch(source, /disclosure\.candidates|setRegion\("candidates"/, "no candidates region");
  assert.doesNotMatch(source, /groupStepsByProject|config\.steps\b/, "the card no longer reads the config steps");
  assert.doesNotMatch(source, /configCandidates|configCandidateCount|configBundleDefault|configAllProjects|configStepsIncomplete/);
  assert.ok(source.indexOf("<WorkflowCard") > 0, "the workflow card takes over");
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\.polyforge-config-candidate-(group|step|project)/, "the candidate CSS is deleted");
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["configCandidates", "configGroupCandidatesHint", "configCandidateCount", "configAllProjects", "configBundleDefault", "configStepsIncomplete", "configGroupSummarize", "configGroupSummarizeHint", "configGroupWorkspace"]) {
      assert.doesNotMatch(text, new RegExp(`"polyforge\\.settings\\.${key}": `), `${locale} drops ${key}`);
    }
  }
});

test("summarize rows and the read-only memory_policy, grouped per workspace path, live in the cli.toml card; the 工作区 card presents neither", () => {
  assert.match(source, /function configCards</);
  // summarize: the cli.toml card's [summarize] zone, its rows common, written as cli.toml keys.
  // a Cell's own keys (CELL_KEYS: its ceiling, a named Cell's model.source) are common too
  assert.match(source, /const common = zone\.items\.filter\(\(item\) => \(CLI_COMMON_KEYS\.includes\(item\.key\) \|\| SUMMARIZE_KEYS\.includes\(item\.key\) \|\| CELL_KEYS\.includes\(item\.key\)\) && !item\.section\);/);
  assert.match(source, /const writable = WRITABLE\.has\(item\.key\) && !item\.section;/, "summarize no longer depends on a mapped cwd");
  assert.doesNotMatch(source, /workspaceFile\?\.sha256|startsWith\("summarize\."\) !== workspace/, "no summarize write is keyed to the workspace yaml");
  // memory_policy: from the config read, grouped per path, read-only, no write control.
  assert.match(source, /Object\.entries\(config\.memoryPolicy\)\.sort\(/);
  assert.match(source, /t\("polyforge\.settings\.memoryPolicyPath", \{ path: shortenPath\(path\) \}\)/);
  const memory = source.slice(source.indexOf("const renderMemoryRow ="), source.indexOf("const classesOf ="));
  assert.ok(memory.length > 0);
  assert.match(memory, /className="polyforge-config-row is-readonly"/);
  assert.doesNotMatch(memory, /<input|<textarea|<ConfigButton|onClick/, "memory_policy is read-only");
  assert.match(source, /t\("polyforge\.settings\.memoryPolicyTitle"\)/);
  assert.match(source, /<p className="config-scope-switch-reason">\{t\("polyforge\.settings\.memoryPolicyAppendNote"\)\}<\/p>/);
  // Below the summarize floor: the visible upgrade hint, inside the cli.toml card.
  const cli = source.slice(source.indexOf('className="polyforge-config-card polyforge-config-cli"'), source.indexOf("the connection-key confirmation renders"));
  // the hint is localized (all three locales) and names the floor and the found version.
  assert.match(cli, /\{config\.summarizeReason && \(\s*<p className="config-scope-switch-reason">\s*\{t\("polyforge\.settings\.summarizeFloorHint", \{\s*required: POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION,\s*found: config\.pfVersion \?\? t\("polyforge\.settings\.versionUnparseable"\),/);
  assert.match(cli, /\{`\$\{file\} \[memory_policy\]`\}/, "the policy block label is the literal file section");
  assert.match(source, /t\("polyforge\.settings\.summarizeWriteRefused", \{/, "the 400 floor refusal is localized under its row");
  // The 工作区 card: the mapping rows only.
  const workspace = source.slice(source.indexOf("{cards && (() => {"), source.indexOf("void reload()"));
  assert.ok(workspace.length > 0, "the 工作区 card exists");
  assert.doesNotMatch(workspace, /memoryPolicy|summarize|policy/, "the 工作区 card presents neither family");
});

test("every card's legend and every zone's grid header sit above its fold, so collapsing hides rows only", () => {
  const cli = source.slice(source.indexOf('className="polyforge-config-card polyforge-config-cli"'), source.indexOf("the connection-key confirmation renders"));
  const workspace = source.slice(source.indexOf("// The legend and the column header sit above the fold"), source.indexOf("void reload()"));
  for (const [name, card] of [["cli.toml", cli], ["工作区", workspace]]) {
    const fold = card.indexOf('className="polyforge-disclosure-body polyforge-config-fold"');
    assert.ok(fold > 0, `${name} has a fold`);
    const legend = card.indexOf("{legendOf(");
    assert.ok(legend > 0 && legend < fold, `${name}: the legend renders before the fold body`);
    assert.equal((card.match(/\{legendOf\(/g) ?? []).length, 1, `${name}: one legend per card, never inside the fold`);
    const header = card.indexOf("{gridHeader(");
    assert.ok(header > 0 && header < fold, `${name}: the header renders before the fold body`);
    assert.equal((card.match(/\{gridHeader\(/g) ?? []).length, 1, `${name}: one header per zone/card, never inside the fold`);
  }
});

test("exactly two browser-stored numeric controls, built from the shared blocks, with no server write", () => {
  const start = source.indexOf("function PanelPreferenceRow(");
  const row = source.slice(start, source.indexOf("export function PolyforgeConfig("));
  assert.ok(start >= 0, "the preference row exists");
  assert.match(row, /<ConfigDetailGridRow label=\{label\} tone="plain">/, "a shared grid row");
  assert.match(row, /type="number"/);
  assert.match(row, /min=\{spec\.min\}\s*max=\{spec\.max\}/, "the input declares the bounds");
  assert.match(row, /<ConfigButton size="small" onClick=\{save\}>\{saveLabel\}<\/ConfigButton>/, "Save sits right after its input");
  assert.match(row, /writePolyforgePreference\(spec, draft \?\? value\);/, "the save clamps and stores through the preferences module");
  assert.match(row, /className="config-scope-switch-reason">\{hint\}</, "a visible hint, not a tooltip");
  assert.doesNotMatch(row, /fetch\(|\/api\//, "no request: browser storage only");
  // the Jobs card's own section, marker to its closing tag
  // (the package switch now renders before it).
  const cardsStart = source.indexOf("polyforge-config-card polyforge-config-cards");
  const section = source.slice(cardsStart, source.indexOf("</section>", cardsStart));
  assert.ok(cardsStart > source.indexOf("polyforge-config-card polyforge-config-package"), "the Jobs card renders after the package switch");
  assert.equal((section.match(/<PanelPreferenceRow/g) ?? []).length, 2, "exactly the two new controls");
  assert.equal((section.match(/<input|<ConfigSwitch|<ConfigButton/g) ?? []).length, 0, "the two preference rows are the card's only controls");
  assert.match(section, /spec=\{POLYFORGE_TITLE_LENGTH\}/);
  assert.match(section, /spec=\{POLYFORGE_POLL_INTERVAL\}/);
  assert.doesNotMatch(section, /detect/, "the card does not depend on pf detection");
  assert.match(source, /const \{ titleLength, pollIntervalSec \} = usePolyforgePanelPreferences\(\);/, "the inputs show the stored (clamped) values");
});

test("detection collapses to one visible summary line over its six detail rows", async () => {
  const statusStart = source.indexOf("polyforge-config-card polyforge-config-status");
  const status = source.slice(statusStart, source.indexOf("</section>", statusStart));
  assert.ok(status.length > 0, "the detection card exists");
  // One toggle line, default closed through the stored region, never forced open.
  assert.match(status, /<DisclosureToggle\s*\n\s*open=\{disclosure\.detect\}/);
  assert.match(status, /onToggle=\{\(\) => setRegion\("detect", !disclosure\.detect\)\}/);
  assert.match(status, /hidden=\{!disclosure\.detect\}/, "the detail body is hidden while the region is closed");
  assert.match(status, /\{detectSummary\}/, "the summary line is the toggle's visible text");
  // The six detail rows stay, unchanged, inside the body.
  for (const key of ["version", "line", "piSkill", "claudeSymlink", "claudeHook", "daemon"]) {
    assert.match(status, new RegExp(`<ConfigDetailGridRow label=\\{t\\("polyforge\\.settings\\.${key}"\\)\\}`), `detail row ${key}`);
  }
  assert.equal((status.match(/<ConfigDetailGridRow /g) ?? []).length, 6, "exactly the six detail rows");
  // Abnormal states are visible text in the summary, never a tooltip.
  assert.match(source, /detect\.pfPresent === false\s*\n\s*\? t\("polyforge\.settings\.notInstalled"\)/, "pf absent names itself");
  assert.match(source, /detect\.pfVersionBelowMinimum \? ` · \$\{t\("polyforge\.settings\.belowMinimum"\)\}` : ""/, "below minimum is appended");
  assert.match(source, /t\("polyforge\.settings\.configSummary", \{\s*\n\s*version:[^\n]*\n\s*line: lineLabel,\s*\n\s*daemon: daemonState,/);
  assert.match(source, /t\("polyforge\.settings\.daemonUnreachable"\)/, "daemon unreachable reads as text");
  assert.doesNotMatch(source, /<p className="config-empty-state">\{t\("polyforge\.settings\.notInstalled"\)\}<\/p>/, "no duplicate standalone not-installed paragraph");
  // The toggle is a real button with aria-expanded/aria-controls.
  assert.match(source, /function DisclosureToggle\(/);
  assert.match(source, /aria-expanded=\{open\}\s*\n\s*aria-controls=\{controls\}/);
  assert.match(source, /usePolyforgeDisclosure\(\)/, "the regions come from the browser-only disclosure hook");
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    const line = text.match(/"polyforge\.settings\.configSummary": "([^"]*)"/)?.[1] ?? "";
    for (const placeholder of ["{version}", "{line}", "{daemon}"]) assert.ok(line.includes(placeholder), `${locale} summary carries ${placeholder}`);
  }
});

test("each cli.toml zone shows its common keys and folds its advanced keys under one counted control", async () => {
  const cli = source.slice(source.indexOf('className="polyforge-config-card polyforge-config-cli"'), source.indexOf("the connection-key confirmation renders"));
  // Common = the schema module's five keys, decided by key, never by mapping;
  // plus the default Cell's own ceiling (the Host ceiling's row moved to the Host pane).
  assert.match(cli, /const common = zone\.items\.filter\(\(item\) => \(CLI_COMMON_KEYS\.includes\(item\.key\) \|\| SUMMARIZE_KEYS\.includes\(item\.key\) \|\| CELL_KEYS\.includes\(item\.key\)\) && !item\.section\);/);
  assert.match(cli, /const advanced = zone\.items\.filter\(\(item\) => !common\.includes\(item\)\);/, "every other zone item, unknown keys included, folds");
  assert.match(cli, /\{common\.map\(\(item\) => renderRow\(item, zone\.id\)\)\}/);
  assert.match(cli, /\{advanced\.map\(\(item\) => renderRow\(item, zone\.id\)\)\}/);
  assert.match(cli, /t\("polyforge\.settings\.configAdvancedCount", \{ count: advanced\.length \}\)/, "one counted control per zone");
  assert.match(cli, /const fold = regionToggle\(region, lockMarker\(advanced\.map\(\(item\) => item\.key\)\)\);/);
  assert.match(cli, /open=\{fold\.open\}/);
  assert.match(cli, /hidden=\{!fold\.open\}/);
  // The #212 regions are reused: no new stored field.
  assert.match(source, /const ZONE_REGION: Record<ConfigZoneId, PolyforgeDisclosureRegion> = \{\s*\n\s*flat: "cliToml",\s*\n\s*daemon: "daemon",\s*\n\s*model: "cliToml",\s*\n\s*summarize: "cliToml",\s*\n\};/);
  assert.match(source, /const WRITABLE = new Set\(\[\.\.\.CLI_WRITABLE_KEYS, \.\.\.SUMMARIZE_KEYS\]\);/, "the writable set is the route's own allowlist constants");
  // The 工作区 card (the mapping rows only) folds them under its counted control.
  const workspace = source.slice(source.indexOf("{cards && (() => {"), source.indexOf("void reload()"));
  assert.match(workspace, /t\("polyforge\.settings\.configRowCount", \{ count: items\.length \}\)/);
  assert.match(workspace, /const fold = regionToggle\("workspace", lockMarker\(items\.map\(\(item\) => item\.key\)\)\);/);
  // A mapping row is never a generic key write: it writes only through
  // writeMapping, behind the one browser-safe guard.
  assert.match(source, /const writable = WRITABLE\.has\(item\.key\) && !item\.section;/);
  assert.match(source, /const editable = writable \|\| \(item\.section === true && parseWorkspaceWriteKey\(item\.key\) !== null\);/);
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["configAdvancedCount", "configRowCount"]) {
      assert.match(text, new RegExp(`"polyforge\\.settings\\.${key}": "[^"]*\\{count\\}`), `${locale}'s ${key} interpolates {count}`);
    }
    assert.doesNotMatch(text, /"polyforge\.settings\.configReadOnlyCount": /, `${locale} drops the retired read-only count`);
  }
});

test("every read-only row shares one muted value treatment", async () => {
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  for (const leaf of ["polyforge-config-value", "polyforge-config-workspace-path", "polyforge-config-chips", "polyforge-config-chip", "polyforge-config-empty"]) {
    assert.match(css, new RegExp(`\\.polyforge-config-row\\.is-readonly \\.${leaf}[,\\s][^}]*?\\{ color: var\\(--text-muted\\); \\}`), `${leaf} is muted in a read-only row`);
  }
  // A read-only row renders no write control: renderValue only writes when writable.
  assert.match(source, /if \(writable\) \{\s*\n\s*return \(\s*\n\s*<span className="polyforge-config-write">/);
  assert.match(source, /if \(!writable\) return chips;/);
});

test("the confirmed page order and the default-collapsed read-outs", async () => {
  // owner-intended change: the page is master-detail, so the
  // confirmed order holds within each home — the Host pane (status, then the
  // Host-level settings and read-outs) and the default Cell's pane (its
  // configuration cards, Reload, then the workflow card) — and the footer
  // closes the page below both.
  // every Cell's settings home is CellSettings (the default's and each named Cell's).
  const hostPane = source.slice(source.indexOf("const renderHostPane = () =>"), source.indexOf("const renderCellPane = (profile: string) =>"));
  const defaultPane = source.slice(source.indexOf("function CellSettings("), source.indexOf("function HostSettingsRegion("));
  const homes = [
    [hostPane, [
      "polyforge-config-card polyforge-config-status",
      "data-pf-host-region",
      "polyforge-config-card polyforge-config-package",
      "polyforge-config-card polyforge-config-cards",
      "aria-label={t(\"polyforge.settings.configHistory\")}",
      "polyforge-config-card polyforge-config-install",
    ]],
    [defaultPane, [
      "<div className=\"polyforge-config-panel\">",
      "polyforge-config-card polyforge-config-cli",
      "{cards && (() => {",
      "void reload()",
      "<WorkflowCard",
    ]],
  ];
  for (const [home, markers] of homes) {
    assert.ok(home.length > 0, "both homes exist");
    let previous = -1;
    for (const marker of markers) {
      const at = home.indexOf(marker);
      assert.ok(at > previous, `${marker} renders in the confirmed order`);
      previous = at;
    }
  }
  assert.ok(source.indexOf("<ConfigFooter") > source.indexOf("</ConfigSplitView>"), "the footer closes the page below the split view");
  // History: closed by default, its title counts the entries; rollback rows unchanged inside.
  assert.match(source, /const historyRegion = regionToggle\("history", lockMarker\(history\.map\(\(entry\) => entry\.id\)\)\);/);
  assert.match(source, /open=\{historyRegion\.open\}/);
  assert.match(source, /hidden=\{!historyRegion\.open\}/);
  assert.match(source, /t\("polyforge\.settings\.configHistoryCount", \{ count: history\.length \}\)/);
  assert.match(source, /\{entry\.rollbackable && entry\.key !== "api_key" && \(/);
  // Install: closed by default, no count, forced open (never stored) until pf is proven present and new enough.
  assert.match(source, /const installForced = !detect \|\| detect\.pfPresent === false \|\| detect\.pfVersionBelowMinimum === true;/);
  assert.match(source, /hidden=\{!installForced && !disclosure\.install\}/);
  assert.doesNotMatch(source, /setRegion\("install", (true|installForced)/, "the forced state is never written");
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    assert.match(text, /"polyforge\.settings\.configHistoryCount": "[^"]*\{count\}/, `${locale}'s history count interpolates {count}`);
  }
});

test("per-key results render under their own row; the footer keeps page-level status only", async () => {
  const saveFn = source.slice(source.indexOf("const commit = useCallback("), source.indexOf("const reload = useCallback("));
  const rollbackFn = source.slice(source.indexOf("const rollback = useCallback("), source.indexOf("const onCellWritten = useCallback("));
  // a Cell pane reaches the footer only through Reload's page-level status.
  const cellPane = source.slice(source.indexOf("function CellSettings("), source.indexOf("function HostSettingsRegion("));
  const reloadFn = cellPane.slice(cellPane.indexOf("const reload = useCallback("), cellPane.indexOf("const rollback = useCallback("));
  assert.equal((cellPane.match(/onPageStatus\(/g) ?? []).length, (reloadFn.match(/onPageStatus\(/g) ?? []).length, "only Reload reports to the footer");
  // The schema guard still runs before any write, and its error lands under the row.
  assert.match(saveFn, /const invalid = configValueError\(item\.key, value, item\.schema\);\s*\n\s*if \(invalid\) \{\s*\n\s*setRowFeedback\(\{ \[item\.key\]: \{ message: invalid, tone: "error" \} \}\);\s*\n\s*return;/);
  // keyed by the row's feedback id (the key; a mapping add row its own id)
  assert.match(saveFn, /setRowFeedback\(\{ \[feedbackId\]: \{ message: data\.message \?\? t\("polyforge\.settings\.configSaved"\), tone: "default" \} \}\);/);
  assert.match(saveFn, /commit = useCallback\(async \(key: string, action: "set" \| "unset", value: unknown, feedbackId: string = key\)/);
  assert.match(saveFn, /onWritten\(\);/, "a save still refreshes history (the page's onWritten re-reads it)");
  assert.match(source, /const onCellWritten = useCallback\(\(\) => \{\s*\n\s*void runHistory\(\);/);
  assert.match(rollbackFn, /feedbackId: string = entry\.id/, "a history rollback's result is keyed by its entry");
  assert.match(rollbackFn, /setRowFeedback\(\{ \[feedbackId\]: /, "a notice rollback's result lands under its row");
  for (const fn of [saveFn, rollbackFn]) {
    assert.doesNotMatch(fn, /setActionError|setActionMessage/, "row results never reach the footer");
  }
  // Rendered inside the row / entry; an error is an alert.
  assert.match(source, /role=\{feedback\.tone === "error" \? "alert" : undefined\}/);
  assert.match(source, /\{renderFeedback\(item\.key\)\}\s*\n\s*\{renderNotice\(item\.key\)\}\s*\n\s*<\/div>/, "under its config row");
  assert.match(source, /\{renderFeedback\(entry\.id\)\}\s*\n\s*<\/div>/, "under its history entry");
  // Copy feedback stays in the install block.
  assert.match(source, /setInstallMessage\(t\("polyforge\.settings\.installCopied"\)\)/);
  assert.doesNotMatch(source, /setActionMessage\(t\("polyforge\.settings\.installCopied"\)\)/);
  // The footer's summary is the page-level status.
  assert.match(source, /summary=\{actionError \?\? actionMessage \?\? \(detect \? t\("polyforge\.settings\.detected"\) : t\("polyforge\.loading"\)\)\}/);
  // Element budget: still exactly one switch; numeric inputs only via the two preference rows plus pf's own int keys.
  assert.equal((source.match(/<ConfigSwitch\b/g) ?? []).length, 1, "exactly one role=switch");
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  assert.match(css, /\.polyforge-config-row-feedback \{[^}]*grid-column: 1 \/ -1;/, "the result spans the row");
});

test("a draft or an error locks its region open with a visible marker, never stored", async () => {
  assert.match(source, /const lockMarker = \(ids: string\[\]\): string \| null =>/);
  assert.match(source, /rowFeedback\[id\]\?\.tone === "error"\)\) return t\("polyforge\.settings\.configErrorMarker"\)/);
  assert.match(source, /return t\("polyforge\.settings\.configDraftMarker"\)/);
  assert.match(source, /open: marker !== null \|\| disclosure\[region\],/, "open = locked || stored");
  assert.match(source, /onToggle: \(\) => \{ if \(marker === null\) setRegion\(region, !disclosure\[region\]\); \},/, "a locked toggle writes nothing");
  assert.match(source, /\{marker \? <span className="polyforge-disclosure-marker">\{marker\}<\/span> : null\}/);
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["configDraftMarker", "configErrorMarker"]) {
      assert.match(text, new RegExp(`"polyforge\\.settings\\.${key}": "\\S`), `${locale} must carry ${key}`);
    }
  }
});

// the workflow card.
const workflowCard = source.slice(source.indexOf("function WorkflowCard("));

test("the workflow card reads its own route afresh per click with the #204 refresh pattern", () => {
  assert.ok(workflowCard.length > 0, "the card exists");
  // of the pane's Cell (its --profile)
  assert.match(workflowCard, /fetch\(`\/api\/polyforge\/workflows\?cwd=\$\{encodeURIComponent\(cwd \?\? ""\)\}&profile=\$\{encodeURIComponent\(profile\)\}`\)/, "one fresh read of the route");
  // Only the newest read writes the card or ends the spin.
  assert.match(workflowCard, /const seq = \+\+readSeqRef\.current;/);
  assert.equal((workflowCard.match(/if \(seq !== readSeqRef\.current\) return;/g) ?? []).length, 2, "both the answer and the failure check the seq");
  // A failed read keeps the last good list: it never clears `read`.
  assert.doesNotMatch(workflowCard, /setRead\(null\)/);
  assert.match(workflowCard, /if \(!good\) \{\s*\n\s*\/\/ Keep the list on screen; the error sits above it\.\s*\n\s*setReadError\(/);
  assert.match(workflowCard, /\{readError && <p role="alert" className="config-add-source-error">\{readError\}<\/p>\}/);
  // The 26px ↻: disabled, aria-busy and spinning while the newest read is in flight; the existing keys.
  assert.match(workflowCard, /className=\{`polyforge-workflow-refresh\$\{refreshing \? " animate-spin" : ""\}`\}/);
  assert.match(workflowCard, /disabled=\{refreshing\}\s*\n\s*aria-busy=\{refreshing\}/);
  assert.match(workflowCard, /aria-label=\{refreshing \? t\("polyforge\.refreshing"\) : t\("polyforge\.refresh"\)\}/);
  assert.doesNotMatch(workflowCard, /setInterval|setTimeout/, "no timer: a read is a mount or a click");
});

test("rows come from the pure model; one summary line per row and one card-level note and badge", () => {
  // the rows are the owning workspace's.
  assert.match(workflowCard, /const rows = buildWorkflowRows\(workflows, owner\?\.declared \?\? null, project\);/);
  assert.match(workflowCard, /: defaultProject\(\[\.\.\.ownProjects, \.\.\.projects\]\);/, "the settings cwd's first project is the default, else the union's first");
  assert.match(workflowCard, /<li key=\{row\.id\} className="polyforge-workflow-row">/);
  assert.match(workflowCard, /<span className="polyforge-workflow-id">\{row\.id\}<\/span>/);
  assert.match(workflowCard, /t\("polyforge\.settings\.workflowInherit"\)/);
  assert.match(workflowCard, /t\("polyforge\.settings\.workflowDeclared"\)/);
  assert.match(workflowCard, /t\("polyforge\.settings\.workflowSourceLabel"\)\}: <BreakableToken text=\{view\.from\} \/>/, "the effective chain names its level");
  // the next-dispatch note and the live badge are the card's,
  // stated exactly once, never inside a row; the bundle label key is retired.
  assert.equal((workflowCard.match(/t\("polyforge\.settings\.classLiveNote"\)/g) ?? []).length, 1, "the next-dispatch note is stated once");
  assert.equal((workflowCard.match(/reloadClassLabel\("live"\)/g) ?? []).length, 1, "exactly one live badge");
  assert.doesNotMatch(workflowCard, /workflowBundleLabel/, "the retired bundle label is still referenced");
  // No element inside a row other than the row itself carries "row" in its class (the row is the row).
  const rowStart = workflowCard.indexOf('className="polyforge-workflow-row"');
  const rowBody = workflowCard.slice(rowStart + 10, workflowCard.indexOf("</li>", rowStart));
  assert.ok(rowBody.length > 0, "the row body is found");
  assert.doesNotMatch(rowBody.replace("polyforge-config-row-feedback", ""), /className="[^"]*row[^"]*"/);
  assert.doesNotMatch(rowBody, /classLiveNote|reloadClassLabel\("live"\)/, "a row repeats the card-level note or badge");
  // One summary line holding the id, chain, truncatable source and right-aligned bundle suffix.
  assert.match(rowBody, /className=\{row\.diverges \? "polyforge-workflow-summary is-divergent" : "polyforge-workflow-summary"\}/);
  assert.match(rowBody, /<span className="polyforge-workflow-effective">/);
  assert.match(rowBody, /className="polyforge-workflow-from" title=\{/, "the truncated source keeps its full text in a title");
  assert.match(rowBody, /className="polyforge-workflow-bundle-suffix">/);
  assert.match(rowBody, /ref\.candidates/, "bundle candidates move to a tooltip");
});

test("pf submit --models is a note only and writes go through the one config route with the yaml CAS", () => {
  assert.match(workflowCard, /<p className="polyforge-workflow-submit-note">\{t\("polyforge\.settings\.workflowSubmitNote"\)\}<\/p>/);
  for (const label of workflowCard.matchAll(/aria-label=\{([^}]*)\}/g)) {
    assert.doesNotMatch(label[1], /submit|--models/, "no control edits pf submit --models");
  }
  assert.match(workflowCard, /fetch\("\/api\/polyforge\/config", \{/);
  assert.match(workflowCard, /expect_state: \{ file_sha256: owner\.declared\?\.yaml\.sha256 \?\? null, values: \{\} \}/, "the owning workspace's yaml CAS");
  assert.match(workflowCard, /cwd: owner\.workspace,/, "the owning workspace's --cwd, never the settings cwd");
  assert.match(workflowCard, /cwd: owner\.workspace,\s*\n\s*profile,/, "written in the pane's Cell");
  assert.match(workflowCard, /stepWriteKey\(project \?\? "", row\.id\)/);
  assert.doesNotMatch(workflowCard, /attrDrafts|row\.attrs|workflowAttrBatch/, "no step-attr editor");
  assert.match(workflowCard, /void write\(row\.id, key, "unset"\)/, "clear = unset (falls back one level)");
  assert.doesNotMatch(workflowCard, /harness\.|ParseCandidate|candidateError/, "no UI candidate validator");
});

test("below the capability floor the read facts stay and every write control is disabled with visible text", () => {
  assert.match(workflowCard, /const writeCapable = workflows\?\.write_capable === true && owner\?\.declared != null && project !== null;/);
  assert.match(workflowCard, /const disabled = !writeCapable \|\| writing !== null;/);
  assert.match(workflowCard, /\{workflows && !workflows\.write_capable && \(\s*\n\s*<p className="config-scope-switch-reason">\s*\n\s*\{t\("polyforge\.settings\.workflowCapabilityHint"\)\}/);
  const editor = workflowCard.slice(workflowCard.indexOf("const renderEditor ="), workflowCard.indexOf("return (\n    <section"));
  const controls = (editor.match(/<input|<button|<ConfigButton/g) ?? []).length;
  const gated = (editor.match(/disabled=\{disabled/g) ?? []).length;
  assert.equal(gated, controls, "every write control is gated on the capability");
});

test("the card folds its write controls under the stored workflow region, locked open by a draft or error", () => {
  assert.match(workflowCard, /const foldOpen = marker !== null \|\| disclosure\.workflow;/);
  assert.match(workflowCard, /onToggle=\{\(\) => \{ if \(marker === null\) setRegion\("workflow", !disclosure\.workflow\); \}\}/);
  assert.match(workflowCard, /<div className="polyforge-workflow-editor" hidden=\{!foldOpen\}>/);
  assert.match(workflowCard, /usePolyforgeDisclosure\(\)/, "the #212 store, no third collapse idiom");
  assert.doesNotMatch(workflowCard, /localStorage/);
});

test("the workflow strings exist in all three locales and the CSS defines the card", async () => {
  const keys = ["workflowTitle", "workflowHint", "workflowProject", "workflowInherit", "workflowDeclared",
    "workflowEffective", "workflowSourceLabel", "workflowSubmitNote", "workflowCapabilityHint", "workflowBundleError",
    "workflowChainAdd", "workflowChainClear", "workflowDefaultRow", "workflowTaskRow", "workflowEditToggle", "classLiveNote"];
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of keys) assert.match(text, new RegExp(`"polyforge\\.settings\\.${key}": "\\S`), `${locale} must carry ${key}`);
    assert.doesNotMatch(text, /workflowAttrBatch/, `${locale} still carries the removed attr label`);
    assert.match(text, /"polyforge\.settings\.workflowSubmitNote": "`pf submit --models`/);
  }
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  for (const cls of ["polyforge-workflow-refresh", "polyforge-workflow-row", "polyforge-workflow-id", "polyforge-workflow-live", "polyforge-workflow-summary", "polyforge-workflow-bundle-suffix", "polyforge-workflow-editor"]) {
    assert.match(css, new RegExp(`\\.${cls}[ ,{.]`), `${cls} is styled`);
  }
  assert.doesNotMatch(css, /\.polyforge-workflow-note[ ,{.]/, "the retired row note class is gone");
});

// a long non-divergent chain wraps by segment, never overflowing.
test("the step row's summary wraps by segment (lead, effective chain, suffix) and its chains shrink", async () => {
  const rowStart = workflowCard.indexOf('className="polyforge-workflow-row"');
  const rowBody = workflowCard.slice(rowStart, workflowCard.indexOf("</li>", rowStart));
  const lead = rowBody.slice(rowBody.indexOf('<span className="polyforge-workflow-lead">'), rowBody.indexOf('<span className="polyforge-workflow-effective">'));
  assert.ok(lead.length > 0, "the lead segment wraps id, kind and declared chain before the effective segment");
  for (const part of ["polyforge-workflow-id", "polyforge-workflow-kind", "workflowDeclared", "renderChips(row.declaredModels", "polyforge-workflow-inherit"]) {
    assert.ok(lead.includes(part), `${part} sits in the lead segment`);
  }
  assert.ok(rowBody.indexOf("polyforge-workflow-bundle-suffix") > rowBody.indexOf("polyforge-workflow-effective"), "the bundle suffix stays the last item");
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  const rule = (selector) => {
    const at = css.indexOf(`\n${selector} {`);
    assert.ok(at >= 0, `${selector} has a rule`);
    return css.slice(at, css.indexOf("}", at));
  };
  assert.match(rule(".polyforge-workflow-summary"), /flex-wrap: wrap;/, "the summary wraps at every width");
  assert.doesNotMatch(rule(".polyforge-workflow-summary"), /nowrap/);
  assert.match(css, /\.polyforge-workflow-lead,\n\.polyforge-workflow-summary \.polyforge-workflow-effective,\n\.polyforge-workflow-summary \.polyforge-config-chips \{ flex: 0 1 auto; min-width: 0; max-width: 100%; \}/, "the segments and their chips shrink and wrap inside");
  assert.match(rule(".polyforge-workflow-lead"), /flex-wrap: wrap;/);
  const suffix = rule(".polyforge-workflow-bundle-suffix");
  for (const decl of ["display: inline;", "margin-left: auto;", "white-space: pre;"]) assert.ok(suffix.includes(decl), `the suffix keeps ${decl}`);
  assert.match(css, /\.polyforge-workflow-summary\.is-divergent \{ flex-wrap: wrap; \}/, "the divergent rule stays");
  assert.match(css, /@container \(max-width: 640px\) \{\n  \/\* a phone row \(a narrow pane\) may wrap inside its summary container\. \*\/\n  \.polyforge-workflow-summary \{ flex-wrap: wrap; \}/, "the <=640 rule stays (a pane-width query since)");
});

test("the machine-scope hint has its own class and title; .config-scope-switch-reason and its other users are unchanged", async () => {
  // naming the Cell's own file
  assert.match(source, /const machineScope = t\("polyforge\.settings\.configMachineScope", \{ file \}\);/);
  assert.match(source, /<p className="config-machine-scope-hint" title=\{machineScope\}>\{machineScope\}<\/p>/);
  assert.doesNotMatch(source, /className="config-scope-switch-reason">\{machineScope\}/);
  // adds one call site: the summarize floor hint in the cli.toml card.
  // the workflow submit note leaves for its own class, so 7 remain.
  // adds two: a Cell's no-workspace reason (the default Cell's head, a named Cell's workspace card).
  // those two become one (every Cell's 工作区 card) and the file card's api_key set state joins.
  assert.ok((source.match(/className="config-scope-switch-reason"/g) ?? []).length === 9, "the shared class keeps its other call sites (owner-intended)");
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  assert.match(css, /\n\.config-scope-switch-reason \{\n  max-width: 320px;\n  color: var\(--text-dim\);\n  font-size: 11px;\n  line-height: 1\.4;\n\}/, "the shared rule is untouched");
  assert.match(css, /\n\.config-machine-scope-hint \{\n  min-width: 0;\n  color: var\(--text-dim\);\n  font-size: 11px;\n  line-height: 1\.4;\n  white-space: nowrap;\n  overflow: hidden;\n  text-overflow: ellipsis;\n\}/, "desktop: one ellipsised line");
  const phone = css.slice(css.indexOf("  .config-machine-scope-hint {"));
  // the page's <=640px block is a pane-width container query.
  assert.ok(css.lastIndexOf("@container (max-width: 640px) {", css.indexOf("  .config-machine-scope-hint {")) > css.indexOf("\n.config-machine-scope-hint {"), "the phone rule sits in a <=640px block");
  const body = phone.slice(0, phone.indexOf("}"));
  for (const decl of ["white-space: normal;", "overflow: visible;", "text-overflow: clip;", "text-wrap: balance;", "overflow-wrap: break-word;"]) assert.ok(body.includes(decl), `phones: ${decl}`);
});

// the workflow submit note is a full sentence, so it must not
// inherit the short-reason class's 320px cap (that forced two lines at >=768px).
test("the workflow submit note has its own uncapped class; .config-scope-switch-reason is untouched", async () => {
  assert.match(workflowCard, /<p className="polyforge-workflow-submit-note">\{t\("polyforge\.settings\.workflowSubmitNote"\)\}<\/p>/);
  assert.doesNotMatch(workflowCard, /className="config-scope-switch-reason">\{t\("polyforge\.settings\.workflowSubmitNote"\)\}/);
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  const rule = (selector) => {
    const at = css.indexOf(`\n${selector} {`);
    assert.ok(at >= 0, `${selector} has a rule`);
    return css.slice(at, css.indexOf("}", at));
  };
  const note = rule(".polyforge-workflow-submit-note");
  assert.match(note, /font-size: 11px;/, "the note keeps the dim 11px type");
  assert.match(note, /white-space: normal;/, "the note wraps normally");
  assert.doesNotMatch(note, /max-width|nowrap|ellipsis|overflow: hidden/, "the note carries no cap and no truncation");
  assert.match(rule(".config-scope-switch-reason"), /max-width: 320px;/, "the shared rule is untouched");
});

// connection-key protection.
test("a connection-key save waits for a confirmation outside every fold; confirm writes once, cancel writes nothing", async () => {
  const saveFn = source.slice(source.indexOf("const save = useCallback("), source.indexOf("const confirmSave = useCallback("));
  // Validation first, then the connection keys stop at the confirmation (no POST in save itself).
  assert.ok(saveFn.indexOf("configValueError(item.key, value, item.schema)") < saveFn.indexOf("CONNECTION_KEYS.includes(item.key)"), "the draft is validated before the confirmation opens");
  assert.match(saveFn, /if \(CONNECTION_KEYS\.includes\(item\.key\)\) \{\s*\n\s*setRowFeedback\(\{\}\);\s*\n\s*setConfirming\(\{ item, action: "set", value \}\);\s*\n\s*return;/);
  // a connection key's Unset asks the same way
  assert.match(saveFn, /if \(CONNECTION_KEYS\.includes\(item\.key\)\) \{\s*\n\s*setRowFeedback\(\{\}\);\s*\n\s*setConfirming\(\{ item, action: "unset" \}\);\s*\n\s*return;/);
  assert.doesNotMatch(saveFn, /fetch\(/, "save itself never posts");
  assert.match(saveFn, /await commit\(item\.key, "set", value\);/, "every other key saves from its own row");
  const confirmFn = source.slice(source.indexOf("const confirmSave = useCallback("), source.indexOf("const cancelConfirm = () =>"));
  assert.equal((confirmFn.match(/commit\(/g) ?? []).length, 1, "confirm issues exactly one write");
  // Cancel clears the pending confirmation only; the draft stays (never the api_key's).
  const cancelFn = source.slice(source.indexOf("const cancelConfirm = () =>"), source.indexOf("const writeMapping = async"));
  assert.match(cancelFn, /if \(confirming\) dropKeyDraft\(confirming\.item\.key\);\s*\n\s*setConfirming\(null\);/);
  assert.doesNotMatch(cancelFn, /commit\(|fetch\(/, "cancel writes nothing");
  assert.match(source, /const dropKeyDraft = \(key: string\) => \{\s*\n\s*if \(key === "api_key"\) setDrafts/, "only the api_key draft is dropped");
  assert.match(source, /<ConfigButton size="small" onClick=\{cancelConfirm\}>\s*\n\s*\{t\("polyforge\.settings\.configConfirmCancel"\)\}/);
  // Rendered page-level (after the file card, before the 工作区 card), never inside a fold body.
  const confirm = source.slice(source.indexOf("the connection-key confirmation renders"), source.indexOf("{cards && (() => {"));
  assert.match(confirm, /\{confirming && \(\s*\n\s*<div role="alertdialog" aria-labelledby=\{`\$\{regionId\}-confirm`\} className="polyforge-config-confirm" data-pf-confirm=\{confirming\.item\.key\}>/);
  assert.doesNotMatch(confirm, /polyforge-disclosure-body|hidden=/, "no fold can hide the confirmation");
  assert.match(confirm, /t\("polyforge\.settings\.configConfirmConnection", \{ key: confirming\.item\.key \}\)/);
  // A pending confirmation keeps its zone open like a draft.
  assert.match(source, /confirming\?\.item\.key === id/);
  // A save never reloads, shuts down or re-ensures the daemon: the reload route is only the Reload button's.
  const commitFn = source.slice(source.indexOf("const commit = useCallback("), source.indexOf("const save = useCallback("));
  assert.doesNotMatch(commitFn, /reload|shutdown|ensure/i);
  assert.equal((source.match(/\/api\/polyforge\/config\/reload/g) ?? []).length, 1, "one reload call site: the explicit control");
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["configConfirmSave", "configConfirmCancel", "configConnectionNotice"]) {
      assert.match(text, new RegExp(`"polyforge\\.settings\\.${key}": "\\S`), `${locale} must carry ${key}`);
    }
    assert.match(text, /"polyforge\.settings\.configConfirmConnection": "[^"]*\{key\}/, `${locale}'s confirmation names the key`);
  }
  const en = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");
  const consequence = en.match(/"polyforge\.settings\.configConfirmConnection": "([^"]*)"/)?.[1] ?? "";
  assert.match(consequence, /next pf call/, "states that the panel's next pf call uses the new value");
  assert.match(consequence, /until it restarts/, "states that the daemon keeps the old value until a restart");
});

test("a saved connection key shows an inline notice with a one-click rollback through the history route (never for api_key)", () => {
  const commitFn = source.slice(source.indexOf("const commit = useCallback("), source.indexOf("const save = useCallback("));
  assert.match(commitFn, /if \(CONNECTION_KEYS\.includes\(key\) && entry\) setNotices\(\(current\) => \(\{ \.\.\.current, \[key\]: entry \}\)\);/, "the write's history entry is kept per key");
  const notice = source.slice(source.indexOf("const renderNotice = (key: string) => {"), source.indexOf("// A region holding an unsaved draft"));
  assert.match(notice, /<div className="polyforge-config-row-notice" role="status">/);
  assert.match(notice, /t\("polyforge\.settings\.configConnectionNotice"\)/);
  assert.match(notice, /\{entry\.rollbackable && entry\.key !== "api_key" && \(/, "api_key gets the notice but no rollback");
  assert.match(notice, /onClick=\{\(\) => void rollback\(entry, entry\.key\)\}/, "the same CAS rollback as the history, its result under the row");
  // the Cell pane's notice rollback (the history's one is the Host pane's)
  const cellPane = source.slice(source.indexOf("function CellSettings("), source.indexOf("function HostSettingsRegion("));
  const rollbackFn = cellPane.slice(cellPane.indexOf("const rollback = useCallback("), cellPane.indexOf("const onWorkflowWritten"));
  assert.match(rollbackFn, /await postRollback\(entry, cwd\)/);
  const postFn = source.slice(source.indexOf("async function postRollback("), source.indexOf("/** One browser-stored numeric preference"));
  assert.match(postFn, /fetch\("\/api\/polyforge\/config\/history", \{/, "the history route's CAS write");
  assert.match(rollbackFn, /delete next\[entry\.key\]/, "a rolled-back row drops its notice and its draft");
});

// the workflow card's project union.
test("the workflow card unions every mapped workspace's projects, labels their yaml, degrades a failed workspace to one line", async () => {
  assert.match(workflowCard, /const union = workflowUnion\(read\);/);
  assert.match(workflowCard, /const projects = union\.projects\.map\(\(entry\) => entry\.project\);/);
  assert.match(workflowCard, /const owner = projectOwner\(union, project\);/);
  assert.match(workflowCard, /workflowUnion\(body\)\.sources\.some\(\(entry\) => entry\.workflows\?\.daemon_reachable === true\)/, "one readable workspace is a good read");
  // One line per failed workspace, carrying its reason; its projects never enter the union.
  assert.match(workflowCard, /\{union\.failures\.map\(\(entry\) => \(\s*\n\s*<p key=\{entry\.workspace\} className="config-scope-switch-reason is-error">\s*\n\s*\{t\("polyforge\.settings\.workflowWorkspaceFailed"\)\}: \{entry\.yamlPath\} — \{entry\.reason\}/);
  // Each readable workspace's yaml path labels the projects it contributes.
  assert.match(workflowCard, /<span className="polyforge-workflow-yaml"><BreakableToken text=\{entry\.yamlPath\} \/><\/span>/);
  assert.match(workflowCard, /t\("polyforge\.settings\.workflowProjectSource"\)/);
  // The one fresh read per click is the route's; the card never fans out per project.
  assert.equal((workflowCard.match(/fetch\(`\/api\/polyforge\/workflows/g) ?? []).length, 1);
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["workflowProjectSource", "workflowWorkspaceFailed"]) {
      assert.match(text, new RegExp(`"polyforge\\.settings\\.${key}": "\\S`), `${locale} must carry ${key}`);
    }
  }
});

test("the summarize floor hint, the write refusal and the card hints are in all three locales, localized and naming the versions", async () => {
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["summarizeFloorHint", "summarizeWriteRefused"]) {
      const line = text.split("\n").find((entry) => entry.includes(`"polyforge.settings.${key}"`));
      assert.ok(line, `${locale} lacks ${key}`);
      assert.ok(line.includes("{required}") && line.includes("{found}"), `${locale} ${key} names both versions`);
      if (locale !== "en") assert.match(line.split(": ").slice(1).join(": "), /[㐀-鿿]/, `${locale} ${key} is not English-only`);
    }
    const unparseable = text.split("\n").find((entry) => entry.includes('"polyforge.settings.versionUnparseable"'));
    assert.ok(unparseable, `${locale} lacks versionUnparseable`);
    // The card hints no longer place summarize/memory_policy in the workspace yaml.
    const workspaceHint = text.split("\n").find((entry) => entry.includes('"polyforge.settings.configGroupWorkspaceHint"'));
    assert.doesNotMatch(workspaceHint, /summarize|polyforge\.yaml/, `${locale} 工作区 hint is mapping-only`);
    const flatHint = text.split("\n").find((entry) => entry.includes('"polyforge.settings.configGroupFlatHint"'));
    assert.match(flatHint, /summarize/, `${locale} cli.toml hint names summarize`);
  }
});

test("the Cell lifecycle reads /api/polyforge/cells, gates on the capability, keeps the key write-only and offers no cleanup", () => {
  // the Cells card became the page's list (the registry read
  // moved up) and its Add Cell pane; the lifecycle controls sit in each
  // Cell's own pane.
  assert.match(source, /fetch\("\/api\/polyforge\/cells"\)/);
  const add = source.slice(source.indexOf("function AddCellPane("), source.indexOf("function WorkflowCard("));
  assert.ok(add.length > 0, "the Add Cell pane exists");
  assert.match(add, /read && !supported && \(/, "a pf without the capabilities shows the upgrade hint");
  assert.match(add, /\{supported && \(/, "no write control without the capabilities");
  assert.match(add, /type="password" autoComplete="off" aria-label=\{t\("polyforge\.cells\.apiKey"\)\}/, "the key input is write-only");
  assert.match(add, /setDraft\(\(current\) => \(\{ \.\.\.current, apiKey: "" \}\)\)/, "the key is cleared on every submit");
  assert.match(source, /<AddCellPane\s*\n\s*read=\{cellsRead !== null\}\s*\n\s*supported=\{cellsSupported\}/, "the pane gets the capability gate");
  assert.match(source, /\{cell && cellsSupported && \(\s*\n\s*<ConfigDetailActions>/, "repair/remove only with the capabilities");
  assert.match(source, /profile !== POLYFORGE_DEFAULT_PROFILE && \(confirmRemove === profile \?/, "remove needs a confirmation and never offers default");
  assert.doesNotMatch(source, /cells\/[^"`]*cleanup|"cleanup"/, "no permanent-cleanup control on the page");
});

test("the page is layered — a Host region of Host-only keys, each Cell's file card through the scoped writer, a write-only api_key, and no in-place server edit", () => {
  // the Host is the one layered region left; a named Cell's
  // keys are its file card (CellSettings), like the default Cell's.
  const region = source.slice(source.indexOf("function HostSettingsRegion("), source.indexOf("function AddCellPane("));
  assert.ok(region.length > 0, "the Host region component exists");
  assert.match(region, /fetch\(HOST_REGION_READ_URL\)/, "the region reads the Host scope, never the cwd's Cell");
  assert.match(region, /body: JSON\.stringify\(hostRegionWriteBody\(action, key, value, read\?\.file\?\.sha256 \?\? null\)\)/, "writes go through the scoped writer with the file CAS");
  assert.match(region, /HOST_ONLY_KEYS\.map\(\(key\) => \(/);
  assert.doesNotMatch(source, /ScopedSettingsRegion|CELL_REGION_KEYS|regionEditableKeys/, "no Cell region is left");
  // a Cell's api_key: write-only in its file card — a password input, its set state, never its value
  const cell = source.slice(source.indexOf("function CellSettings("), source.indexOf("function HostSettingsRegion("));
  assert.match(cell, /type=\{item\.key === "api_key" \? "password"/);
  assert.match(cell, /data-pf-api-key-state=\{item\.set \? "set" : "unset"\}/);
  assert.match(source, /function itemValue\(item: PolyforgeConfigItem\): unknown \{\s*\n\s*if \(item\.key === "api_key"\) return "";/, "the key's value is never read into the input");
  assert.match(cell, /const declared = item\.key === "api_key" \? item\.set === true : item\.source === "file";/, "Unset of api_key only while a key is set");
  // the identity keys are shown with the new-profile guidance, never edited
  assert.match(cell, /IDENTITY_KEYS\.includes\(item\.key\) && \(/);
  assert.match(cell, /t\("polyforge\.cells\.identityNewProfile"\)/);
  // the Host region lives in the Host pane, each Cell's settings in its own pane.
  const hostPane = source.slice(source.indexOf("const renderHostPane = () =>"), source.indexOf("const renderCellPane = (profile: string) =>"));
  const cellPane = source.slice(source.indexOf("const renderCellPane = (profile: string) =>"), source.indexOf("// A pane mounts on its first selection"));
  assert.match(hostPane, /data-pf-host-region>\s*\n\s*\{cellsSupported \? \(\s*\n\s*<HostSettingsRegion title=/, "one Host region, behind the capability");
  assert.match(cellPane, /<CellSettings\s*\n\s*profile=\{profile\}/, "one settings pane per Cell");
  assert.doesNotMatch(hostPane + cellPane, /cleanup/i, "no permanent-cleanup control on the page");
});

test(" (review s17#2): a region's connection-key write waits for the confirmation, then shows the notice with its rollback — the same safety as the cli.toml card", () => {
  const region = source.slice(source.indexOf("function HostSettingsRegion("), source.indexOf("function AddCellPane("));
  const writeFn = region.slice(region.indexOf("const write = (pending: RegionWrite) =>"), region.indexOf("const confirmWrite"));
  assert.match(writeFn, /if \(CONNECTION_KEYS\.includes\(pending\.key\)\) \{\s*\n\s*setMessage\(null\);\s*\n\s*setConfirming\(pending\);\s*\n\s*return;/, "a connection key posts nothing before its confirmation");
  assert.match(region, /const confirmWrite = \(\) => \{\s*\n\s*if \(!confirming\) return;\s*\n\s*const pending = confirming;\s*\n\s*setConfirming\(null\);\s*\n\s*void post\(pending\);/, "confirm posts exactly one write");
  // the api_key lives in a Cell's file card, whose cancel never leaves the key in the form
  assert.match(source, /if \(confirming\) dropKeyDraft\(confirming\.item\.key\);/, "cancel never leaves the key in the form");
  assert.match(region, /if \(CONNECTION_KEYS\.includes\(key\) && entry\) setNotices/, "a saved connection key keeps its entry for the notice");
  assert.match(region, /entry\.rollbackable && entry\.key !== "api_key" && \(/, "one-click rollback, never for api_key");
  assert.match(region, /await postRollback\(entry, ""\)/, "the rollback is the history route's CAS write");
  // unsetting a named Cell's api_key (its file card) says what it does
  assert.match(source, /named && confirming\.item\.key === "api_key" && confirming\.action === "unset"\s*\n\s*\/\/[^\n]*\n\s*\? t\("polyforge\.cells\.confirmApiKeyUnset"\)/, "unsetting a named Cell's api_key says what it does");
  // every write path of the region goes through write(): no direct post bypasses the confirmation
  const posts = region.match(/void post\(/g) ?? [];
  assert.equal(posts.length, 2, "only write() (a non-connection key) and the confirmation post");
  assert.match(writeFn, /void post\(pending\);/);
});

test(" (review s17#2): each key has one layered surface — the cli.toml card drops the Host-only keys, the default Cell has no second region", () => {
  assert.match(source, /groupConfigItems\(\(config\.items \?\? \[\]\)\.filter\(\(item\) => !HOST_ONLY_KEYS\.includes\(item\.key\)\)\)/, "the Host-only rows live in the Host region only");
  // the default Cell's pane is its file card; so is every named Cell's —
  // one component, no second Cell surface.
  const cell = source.slice(source.indexOf("function CellSettings("), source.indexOf("function HostSettingsRegion("));
  assert.ok(cell.includes("polyforge-config-card polyforge-config-cli"), "a Cell's settings are its file card");
  assert.doesNotMatch(cell, /<HostSettingsRegion/, "no region opens in a Cell's pane");
  assert.match(source, /\{profiles\.map\(\(profile\) => pane\(`cell:\$\{profile\}`, \(\) => renderCellPane\(profile\)\)\)\}/, "every Cell's pane is the same");
  assert.doesNotMatch(source, /renderDefaultPane|renderNamedPane/, "no per-kind Cell pane is left");
});

test("the page is master-detail — a Host entry, every Cell with its state, Add Cell; the detail shows the selected entry only", () => {
  // The shared split view: list, then detail, then the footer below both.
  assert.match(source, /<ConfigSplitView>\s*\n\s*<ConfigSidebar>/);
  const list = source.slice(source.indexOf("<ConfigSidebar>"), source.indexOf("</ConfigSidebar>"));
  const host = list.indexOf("data-pf-host-entry");
  const cells = list.indexOf("data-pf-cell-entry={profile}");
  const add = list.indexOf("data-pf-add-entry");
  assert.ok(host > 0 && cells > host && add > cells, "Host first, then the Cells, then Add Cell");
  assert.match(list, /\{profiles\.map\(\(profile\) => \{/, "one entry per Cell");
  assert.match(source, /const profiles = \[POLYFORGE_DEFAULT_PROFILE, \.\.\.\(cells \?\? \[\]\)\.map\(\(cell\) => cell\.profile\)\.filter\(\(profile\) => profile !== POLYFORGE_DEFAULT_PROFILE\)\];/, "the default Cell is always listed, first");
  assert.match(list, /const state = cell \? polyforgeCellListState\(cell\) : null;/, "each entry shows pf's state through the one mapping");
  assert.match(list, /\{state && <span className=\{`polyforge-cell-badge is-\$\{tone\}`\}>\{stateLabel\(state\)\}<\/span>\}/);
  // One pane per entry, only the selected one shown; a pane stays mounted once visited.
  assert.match(source, /<div key=\{key\} className="polyforge-pane" data-pf-pane=\{key\} hidden=\{shownKey !== key\}>/);
  assert.match(source, /const shown: PaneSelection = selection\.kind === "cell" && !profiles\.includes\(selection\.profile\) \? \{ kind: "host" \} : selection;/, "a Cell that left the registry falls back to the Host");
  // Host-level settings live in the Host pane only; a Cell pane carries none of them.
  const hostPane = source.slice(source.indexOf("const renderHostPane = () =>"), source.indexOf("const renderCellPane = (profile: string) =>"));
  const cellPanes = source.slice(source.indexOf("const renderCellHead = (profile: string) =>"), source.indexOf("const renderHostPane = () =>"))
    + source.slice(source.indexOf("const renderCellPane = (profile: string) =>"), source.indexOf("// A pane mounts on its first selection"))
    + source.slice(source.indexOf("function CellSettings("), source.indexOf("function HostSettingsRegion("));
  for (const marker of ["polyforge-config-status", "data-pf-host-region", "polyforge-config-package", "polyforge-config-cards", "polyforge.settings.configHistory", "polyforge-config-install"]) {
    assert.ok(hostPane.includes(marker), `${marker} lives in the Host pane`);
    assert.ok(!cellPanes.includes(marker), `${marker} never renders in a Cell pane`);
  }
  for (const marker of ["polyforge-config-cli", "<WorkflowCard", "<CellSettings", "data-pf-cell-workspaces"]) {
    assert.ok(!hostPane.includes(marker), `${marker} never renders in the Host pane`);
  }
  // A phone keeps the list above the detail on one screen: no one-pane drill-in.
  assert.match(source, /<ConfigDetailNavigationContext\.Provider value=\{null\}>\s*\n\s*<ConfigSplitView>/);
});

// a step its lane declares skippable carries the project's skip
// switch on its row — visible outside the editor fold, enabled whenever the
// card can write, and written through the card's one write path.
test(" config card: a skippable step row shows a skip switch that writes", async () => {
  const rowStart = workflowCard.indexOf('className="polyforge-workflow-row"');
  const rowBody = workflowCard.slice(rowStart, workflowCard.indexOf("</li>", rowStart));
  const switchAt = rowBody.indexOf("{row.skippable && (");
  assert.ok(switchAt > 0, "the switch renders only on a skippable row");
  const control = rowBody.slice(switchAt, rowBody.indexOf(")}", rowBody.indexOf("/>", switchAt)) + 2);
  assert.match(control, /<label className="polyforge-workflow-skip"/, "the switch has its own class");
  assert.match(control, /<input\s+type="checkbox"/, "the switch is a checkbox");
  assert.match(control, /aria-label=\{`\$\{t\("polyforge\.settings\.workflowSkip"\)\} · \$\{row\.id\}`\}/, "its name carries the i18n label and the step id");
  assert.match(control, /checked=\{row\.skip\}/, "it shows the project's current skip");
  // On by default: only a card that cannot write disables it (no fold, no in-flight lock).
  assert.match(control, /disabled=\{!writeCapable\}/);
  assert.doesNotMatch(control, /disabled=\{disabled|writing !== null/, "nothing but the capability disables it");
  // Through the card's write(): set true, or unset (never `false` written).
  assert.match(control, /const key = skipWriteKey\(project \?\? "", row\.id\);/);
  assert.match(control, /event\.target\.checked \? void write\(row\.id, key, "set", true\) : void write\(row\.id, key, "unset"\)/);
  assert.doesNotMatch(control, /fetch\(/, "no write path of its own");
  assert.match(source, /\n  skipWriteKey,\n/, "the key comes from the shared schema");
  // Outside the editor fold, so a closed fold still shows it.
  const editor = workflowCard.slice(workflowCard.indexOf("const renderEditor ="), workflowCard.indexOf("return (\n    <section"));
  assert.doesNotMatch(editor, /skipWriteKey|row\.skippable/, "the switch is not inside the folded editor");
  assert.ok(switchAt < rowBody.indexOf("{renderEditor(row)}"), "the switch sits above the editor");
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    for (const key of ["workflowSkip", "workflowSkipHint"]) {
      assert.match(text, new RegExp(`"polyforge\\.settings\\.${key}": "\\S`), `${locale} must carry ${key}`);
    }
  }
  assert.match(control, /title=\{t\("polyforge\.settings\.workflowSkipHint"\)\}/, "the hint says when a change applies");
  const css = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
  assert.match(css, /\n\.polyforge-workflow-skip \{/, "the switch is styled");
});
