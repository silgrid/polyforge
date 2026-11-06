# Settings panels: plugins, skills and shared blocks

## General appearance
- `components/FontSettings.tsx` offers separate interface/chat and monospace (code, tool output, terminal) font-name lists in the Fonts & layout section after Appearance, together with chat font size (which also sets the terminal's, hence its "Chat & terminal font size" label; client-platform.md) and content width; the Chat section keeps only behavior switches. Every row there has one shape: a header line (label, value, reset button in a fixed 28px column) over a full-width slider, so values and resets line up. A font row puts its name field between label and value, and its slider is the weight (100–600, step 100; `aria-valuetext` adds the weight's name). Both font rows share one grid (`subgrid`), so their name fields start at the same x whatever the labels' widths in each locale. The name field is drawn in the font it names, which is the preview; keep one short section description, no separate preview lines or per-control paragraphs. `hooks/useFontPreferences.ts` stores them only in the browser (`pi-ui-font-family`, `pi-mono-font-family`), applies changes immediately through `--font-ui` / `--font-mono`, and syncs other tabs. AppShell keeps the hook mounted so restoring a font never requires opening Settings or a chat. Empty input removes the override; the original stacks stay in `app/globals.css` as `--font-ui-default` / `--font-mono-default`.
- Font names refer to fonts on the **browser's device**, not the server; no enumeration, upload or remote download. `lib/font-preferences.ts` treats commas as separators, accepts optional enclosing quotes, quotes non-generic names as data, and always appends the default stack. Never interpolate font input into a stylesheet or accept it as executable CSS. Keep raw input whitespace while typing so multi-word names remain editable.
- Each font has a separate base weight (100–600 in steps of 100, default Regular/400; it stops at 600 because headings and labels use 600 and bold text, highlighted bold and the terminal's bold use 700, so a heavier base would outweigh them), stored as `pi-ui-font-weight` / `pi-mono-font-weight` and applied through `--font-ui-weight` / `--font-mono-weight`. Missing/invalid stored weights use 400; saving 400 removes the override. Each reset clears that font's name **and** weight in one update, not the other font or chat size/width. The UI weight is inherited by ordinary interface/chat text; code/pre, highlighted code (including its inline theme styles), source-view line numbers and diffs use the mono weight. Headings, Markdown bold/inline code inside emphasis, and other explicitly weighted labels keep their original weights; do not blanket-override them with `!important`.
- General's sidebar layout option (`settings.sidebarLayout`) chooses where the desktop sidebar shows the file browser: below the sessions (`stacked`, the default) or in a Sessions | Files tab (`tabs`), as two radio options in the send key's look (`settings-send-mode-option`). `lib/files-placement.ts` keeps it per browser (`pi-web:files-placement`; storage that is absent, blocked or corrupt reads as the default) and `useFilesPlacement()` subscribes through `useSyncExternalStore` (the server snapshot is the default, so hydration agrees), so Settings and `SessionSidebar` follow the same store. Phones always use tabs, whatever is stored.
- General no longer has a claude section: the read-only claude config-dir section was retired (claude sessions run the system claude with the user's own config); see [claude-sessions.md](claude-sessions.md).
- General's Chat section ends with the switch for pi's own `showCacheMissNotices` (#1265), saved in the global settings.json through `/api/cache-miss-notices`, not in the browser: the notices come from the server, and the pi CLI reads the same key. Its note says a project's `.pi/settings.json` can override it. After a save the open chat remounts (`onSessionReloaded`) to show or drop them; see sessions.md, Cache and cost notices.
- General's **Agent language** item (「Agent 使用语言」, zh-TW 「Agent 使用語言」; `AgentLanguageSettings` in `components/SettingsPanel.tsx`) sits right after the UI language section, as its own radio group in the same `settings-language-option` look: English (the default) or 中文. It is not linked to the UI language: that one stays per browser (`pi-locale`), this one is stored on the server in `<pi agent dir>/pi-web-settings.json` (`{ "version": 1, "agentLanguage": "en" | "zh-CN" }`, `lib/agent-language-config.ts`; other keys kept, mode 0600, a file that is not a JSON object is refused rather than overwritten), so every device and browser reads the same value. `GET /api/agent-language` reads it — absent, malformed or unknown reads as English and never blocks a session — and `PUT /api/agent-language` `{ language }` is its only writer, behind the proxy's `/api/*` authentication (`PI_WEB_PASSWORD` when set) and the route's own trusted-request (403) and JSON (415) checks. The PUT also sets the Polyforge step agents' language: when pf is installed and its default Cell's `pf --profile default --json config list` carries `agent_language`, the route first runs `pf --profile default --json config set agent_language <value>` (`syncAgentLanguageToPolyforge` in `lib/polyforge-config-write.ts`, no shell, `PF_BIN`), and a pf refusal is answered as the save's error with nothing written to `pi-web-settings.json`; no pf, a pf without the key, or a `config list` that fails or does not print JSON saves Pi Web's file only. The route checks `pi-web-settings.json` before pf is touched: a file the local write would refuse (not JSON, not a JSON object, unreadable) refuses the save (500) with no `pf config set` run, so a save never leaves pf and Pi Web apart. At the server's start (`registerNodeInstrumentation` in `instrumentation-node.ts`, in the background, never awaited) Pi Web also backfills pf (`reconcilePolyforgeAgentLanguage` in `lib/polyforge-config-write.ts`): when the default Cell's `config list` item for `agent_language` has `source` default (pf's file does not set it, so pf uses English) and the stored value differs from pf's, it runs the same `pf --profile default --json config set agent_language <value>`; a key pf's file sets is never overwritten, a stored English (or nothing stored) writes nothing, and no pf, a pf without the key (older), or a pin, `config list` or `config set` that fails writes nothing and reports no error. `GET /api/agent-language` only reads (`readPolyforgeAgentLanguage`): it spawns exactly one `pf --profile default --json config list` (no version probe, no capability read, never a `config set`) and answers `{ language, polyforge }`, `polyforge` being pf's language as it is now (`{ language, source: "default" | "file" }`, or null for no pf, a pf without the key, or a list that fails or is not JSON; a failing pf answers 200 all the same) — the backfill runs at the server's start only; the client reads a mismatch as `polyforge && polyforge.language !== language`. Under the radio group, `AgentLanguagePfStatus` (`components/AgentLanguagePfStatus.tsx`, presentational like `SettingsUi.tsx`: no hook, every word a prop) shows that language as `settings.agentLanguagePolyforge` (「Polyforge 步骤 agent 当前使用：…」); when it differs from Pi Web's it adds a hint (`role="status"`, never `alert`) chosen by pf's `source`: `settings.agentLanguagePolyforgeMismatch` when pf's file sets the key (`source` file: never overwritten automatically), `settings.agentLanguagePolyforgeUnset` when it does not (`source` default: the start's backfill did not land — e.g. refused CORE-409 because pf's file changed at the same moment, or pf installed after Pi Web started) and the `settings.agentLanguagePolyforgeSync` button (「同步到 Polyforge」, disabled while a save or sync runs). The button writes Pi Web's current value again through `requestAgentLanguageSync` in the same file — `PUT /api/agent-language` (so the route's `pf config set` overwrites even a key pf's file sets: the user's explicit act), then a fresh GET whose `polyforge` replaces the line — bypassing the radio's unchanged-value no-op; a failure shows the `settings.agentLanguageSaveError` line. An ordinary save also re-reads the GET afterwards so the pf line follows it. With `polyforge` null (no pf, an older pf without the key, a failing pf) the line, hint and button are not shown at all. The backfill's `config set` carries `--expect-state '{"file_sha256": <hash>}'` on the file hash its own `config list` read (null when pf reports the file absent; pf's CAS, `internal/polyforge/cli/config_cas.go`), so a write that lands between that read and the set — a save on this page or a manual `pf config set` — makes pf refuse it (CORE-409) instead of being overwritten; a refused set is never retried, one fresh `config list` answers pf's state then. The sync is one-way (a `pf config set` does not update Pi Web's value), and the key is not in the Polyforge panel's writable allowlist: this item is its one editor in Pi Web. The item appears once the GET settled; a failed load shows English with the visible `settings.agentLanguageLoadFailed` line. Picking a value saves it, and the control shows it only after the PUT succeeded; a failed save keeps the old value and shows the visible `settings.agentLanguageSaveError` line (`role="alert"`). The value applies to sessions created afterwards (pi, claude and their pi subagents); a session keeps the language it was created with, and sessions created before the Agent language existed are English. What a 中文 session receives: [tools.md](tools.md) (pi) and [claude-sessions.md](claude-sessions.md) (claude).
- General's **claude tool permissions** block (「claude 工具授权」; `components/ClaudePermissionSettings.tsx`) sits right after the Agent language item and is built from the same pieces: the global 「授权方式」 as a radio group in the `settings-language-option` look — 弹窗询问 (ask, the default: claude's permission prompts and AskUserQuestion open as Pi Web dialogs) or 静默拒绝 (deny: claude refuses them itself, the behaviour before) — the 「有待授权时推送提醒」 switch (`ConfigSwitch`, on by default), and, only when the open session is a claude session, 「本会话」 as a second radio group: 跟随默认 / 弹窗询问 / 静默拒绝 (the session's override; 跟随默认 clears it). Everything is read and written through `/api/claude-permissions` (`GET ?session=<id>`, `PUT { mode?, push? }`, `PUT { session, override }`; stored in `<pi agent dir>/claude-permissions.json` and the claude index, the route and its rules in [claude-sessions.md](claude-sessions.md#permission-prompts-workbench442)), the same server-side store for every device. The block appears once the GET settled (a failed read shows `settings.claudePermissionLoadFailed`); a value shows only after the PUT succeeded, the controls disabled while one runs. A 409 (the session is busy: a turn, a waiting prompt or an unsettled background subagent) keeps the old value and shows `settings.claudePermissionBusy`; any other refusal shows `settings.claudePermissionSaveError` with the route's error (`role="alert"`). A changed default applies to claude children started afterwards; a session's override restarts its idle child.
- ChatInput remeasures an existing draft when the interface font or weight changes. TerminalPanel subscribes in its existing lifetime effect, updates xterm's family and normal weight (its bold stays xterm's default 700, above every base weight) and refits, never restarting the terminal or its SSE stream for a font change.

## Claude section
- Settings › Claude's values live on the server in their own file, `<pi agent dir>/pi-web-claude-settings.json` (keys `launchPermissionMode`, `customModels`, `includeHookEvents`; `lib/claude-settings.ts`, the same tolerant read and serialized atomic 0600 write as the Agent language's file), and `PUT /api/claude-settings` (`app/api/claude-settings/route.ts`) is its only writer. `pi-web-settings.json` stays the Agent language's alone: `/api/agent-language` is still its only writer. What the values do to claude sessions, and the route's refusals: [claude-sessions.md](claude-sessions.md), "Settings › Claude".
- **Place.** `"claude"` in `lib/settings-navigation.ts`'s `SETTINGS_SECTION_VALUES`, right before Polyforge, labelled 「Claude」 in every locale, with its own icon; not in `PROJECT_SECTIONS`, so it opens and restores with no project. It is claude's page alone, apart from pi's sections (General, Models, …): pi's settings never show there and claude's never in General (General's retired claude config-dir section stays gone,).
- **Page** (`components/ClaudeSettings.tsx`, General's section look plus the shared `SettingsUi` blocks — `ConfigDetailGrid`, `ConfigSwitch`, `ConfigButton`, `ConfigNotice`). Top to bottom: the 「Claude」 heading and a note that the values apply to claude sessions created afterwards; **Default permission mode** (`settings.claudeLaunchPermissionMode`), a radio group in the Agent language's look — default labelled `claude.permissionModeDefault` (「默认（按 Claude Code 自身设置）」, the key the composer's control uses, owner ruling Q2=A), acceptEdits, and plan, enabled while's global claude tool permissions (Settings › General's `ClaudePermissionSettings`, read only here) are Ask in a dialog — GET /api/claude-settings' `planAvailable` — and otherwise disabled with `claude.permissionModePlanUnavailable` as visible text in its row, pointing to Settings › General (never only a tooltip; r3, plan D11); **Models**, claude's aliases from GET /api/claude read-only (each with the full name it resolves to) above the custom names, each with a Remove button, and one field + Add: a name that is not `isClaudeModelName` or already offered is refused on the spot as a visible line (`role="alert"`) and nothing is sent; **Show hook activity**, one `ConfigSwitch` with its description; then the read-only **Claude Code** block from GET /api/claude: the version, claude's own `capabilities` verbatim, and a table of Pi Web's three features (approvals, background tasks, subagent text forwarding), each Available / Not available / Unknown with its `basis` code rendered as text (the recorded threshold version,'s global claude tool permissions — Ask in a dialog or Deny silently, the environment variable), plus a `ConfigNotice` when the version is not in `CLAUDE_RECORDED_VERSIONS` (`versionRecorded` false) and visible lines for a claude not on PATH or a failed probe.
- **Saving.** Every change is one `PUT /api/claude-settings` of all three values; a control shows the new value only once the PUT succeeded, and a refusal or failure keeps the old value and shows `settings.claudeSaveError` with the route's reason (`role="alert"`), as the Agent language does. A failed read shows `settings.claudeLoadFailed` and no controls. Only claude sessions created afterwards take the values: an open session keeps its own, and its mode is switched from its composer.

## Plugins and skills
- `/api/plugins` uses pi's `SettingsManager` + `DefaultPackageManager` for global/project package install, remove, update, enable and disable; disabling writes empty `extensions/skills/prompts/themes` arrays to the package entry. Every POST drops the provider routes' shared runtime, since a package can add or drop a provider ([models.md](models.md), Auth and model config).
- `/api/skills` lists through `DefaultResourceLoader`, so settings paths, package skills and project `.agents/skills` appear as the runtime sees them. The loader takes `noExtensions: true` (`lib/skills-service.ts`, #1226): extensions add skills only through `resources_discover` at `session_start`, which a listing never runs, so importing them added nothing and blocked the event loop on every Skills open, skill click and slash-palette open. Skills an extension discovers never showed there, before or since. Even an empty extension load re-keys the SDK's one-cwd factory cache, so a listing for a folder other than the running sessions' still makes their next start re-import. The detail pane lazily reads a selected skill through `/api/skills/content` and renders its post-frontmatter instructions as Markdown; that route permits only an exact file path returned by the same runtime loader and caps the regular-file read at 1 MiB, rather than becoming a general Markdown-file reader. Toggling a skill edits only the `disable-model-invocation` frontmatter key of its `SKILL.md`, so user formatting survives, and `PATCH /api/skills` edits only `.md` files (its roots also hold `auth.json`, settings and project files). `/api/skills/install` shells through `npx skills add ... --agent pi`; project installs run in the selected cwd.
- **Skill documents.** A local link in the instructions (`MarkdownBody`'s `onNavigateLink`) opens that file in the same Instructions field. `resolveSkillLink()` resolves it from the linking document's folder, as pi resolves a skill's relative paths; `SkillDetail` keeps a stack of the documents opened, the last one shown under one Back button and its relative path. Switching skills remounts `SkillDetail`, emptying the stack. A link or Back brings the field's top, or the `#heading`, into view once the document shows: Back lands at the previous document's top, nothing saves a scroll position. A link to the skill file itself reads it without `document`. The client refuses nothing: an absolute path or a `..` past the folder goes to the route, whose message shows under Back.
- `/api/skills/content`'s optional `document` is a path in the loaded skill's `baseDir` (pi's dirname of the skill file): relative, without `..`, `\`, `:` or NUL, `.md`/`.markdown` by name and by real path, inside the folder after symlinks (`isExistingPathWithinRoots()`), a regular file up to 1 MiB. One stat before the bounded read answers a FIFO or a directory with 400 and a larger file with 413; a missing file is 404, a link out of the folder 403, and no refusal or failed read names a path. Without `document` the route reads the loaded file exactly as before.
- **Group switches.** Each sidebar group of the Skills, Plugins and MCP panels (a skill scope, a package scope, an `mcp.json`; not standalone extensions) has a `ConfigSidebarGroupSwitch` in its heading row, so the list's rows keep the heading's line. As with the Models provider switch, it is on only while every row is: a partial group reads as off beside its `n/m` count and one click completes it. A panel whose group can hold rows its switch never turns on passes its own `checked` (MCP: `mcpGroupSwitchChecked()`), or the switch stays off for good. One request carries the list (`PATCH /api/skills` `filePaths`, `POST /api/plugins` `packages`, `POST /api/mcp` `set-enabled` `servers`) and answers per item, so a refused row does not stop the rest; what was left undone shows under the heading (`ConfigSidebarGroupStatus`). An entry already in the requested state is left alone; enabling keeps an entry's own keys such as `autoload`. `SettingsManager` never throws a failed load or write (`flush()` resolves), so the bulk plugin path reads `drainErrors()`, or an unreadable settings.json would report success. Disabling empties the four resource lists and keeps no filters, so a group switched off leaves an enabled filtered package on (the SDK reports every object entry as `filtered`) and says so; the bulk route refuses it.

## Settings on a phone
- At ≤640px Settings fills the screen and shows one level at a time (`SettingsPanel`'s `usePhoneSettingsLevel`): the list of sections, a section, then a row's detail. The plain Settings button opens on the sections (`openAtHome`); the sidebar's Models and Skills buttons and a bare `/mcp` open their section. Above 640px the level is always the section, so a desktop window narrowed to a phone's width keeps the section it shows (the e2e's short settings panel check at 320px relies on it). The header is three columns, Back, title, close, the sides equal so the title stays centered and is cut short in its own column; Back is named after the level above and the title is the shown level's name; over a detail that is the active row's label, reported by `ConfigSidebarItem` / `ConfigListAction` after each render (a rename shows at once), never by rows in a hidden, still-mounted section.
- A split view shows one pane through `ConfigDetailNavigationContext` (`data-pane="list" | "detail"`, the phone CSS in `app/settings.css`). A row's or add button's click opens the detail unless its handler called `preventDefault()`: Models' Add provider does, and its picker's choice calls `openDetail()` (`useConfigDetailNavigation()`). Deleting a provider or a model goes back to the list (`closeDetail()`). Outside the provider (no Settings) both panes stack as before. A section's settings row above its list (Models' enabledModels banner, Sub-agents' switch) shows on the list level only (`data-phone-level` on the section host).
- Each level entered pushes a history entry that copies `history.state` (Next's router state) and keeps the URL, so the system Back goes up one level; the panel's Back calls `history.back()` while it has entries, and closing Settings drops them (`history.go(-n)`). Desktops never push.
- A level change animates with the Web Animations API (`animatePhoneLevelChange`, from a layout effect so the new level never shows in place first): going in, the main area slides in from the right with a fading edge shadow; going back, from 30% left, fading up; the header's Back and title fade in beside it. Only the new level moves (the old one is gone at once) and `.settings-dialog-main` clips it. Nothing moves under `prefers-reduced-motion`.

## Polyforge section (pi#122, configuration panel)

Settings › Polyforge is always visible — not `requiresProject`-gated, openable
and restorable with no project selected (`"polyforge"` in
`lib/settings-navigation.ts`'s `SETTINGS_SECTION_VALUES`, not in
`PROJECT_SECTIONS`). It renders `components/PolyforgeConfig.tsx` from the
shared blocks; its machine-level detection comes from
`GET /api/polyforge/detect` (the route contract and the detection rules are in
[polyforge-panel.md](polyforge-panel.md)).

**Master-detail.** The section is the shared split view: a
list (the Host entry, every Cell with its state, Add Cell) beside one detail
pane, the footer below both; on a phone the list stacks above the detail on
one screen instead of Settings' one-pane drill-in. The layout and the panes
are described in
[polyforge-panel.md](polyforge-panel.md#the-pages-host-and-cells-master-detail-workbench364).

**Order and disclosure.** Each pane keeps the confirmed order
of its part of the page ( split it into two homes). The Host
pane, top to bottom: the machine-status card, the Host region, the
package-switch card, the Jobs card stream card, then the read-outs — recent
panel writes, the install/upgrade command. Every Cell's pane (:
the default Cell's and each named Cell's alike, one `CellSettings`): the
configuration surface (machine-scope note, the two source cards — the one
file card of that Cell's own file (`cli.toml` / `cli-<profile>.toml`) with
its top-level, `[daemon]`, a named Cell's `[model]` and `[summarize]` zones
 and the merged 工作区 / Workspace card —
Reload with its read/error/reason text), then the workflow card
([polyforge-panel.md](polyforge-panel.md#workflow-card-workbench216)). The
footer closes the page below either pane (the read-only declared-candidates
read-out is gone: the workflow card carries those step facts). Nothing is
removed or conditionally hidden; parts are folded:

- **Detection** keeps its card title and shows one visible summary line —
  `pf {version} · {line} · daemon {daemon}` (`polyforge.settings.configSummary`)
  — which is the toggle for the six detail rows below. An abnormal state is
  named in that line as text: pf absent reads 「未安装」 (`notInstalled`, which
  replaces the old standalone paragraph), a version below the minimum appends
  the 「版本过低」 text, an unreachable daemon reads as such. `detectError` and
  Re-detect stay outside the fold; the detection cadence is unchanged.
- **The cli.toml card** is one card holding two file zones,
  each headed by the literal file section it writes — `cli.toml` for the
  top-level keys, `cli.toml [daemon]` for the `[daemon]` table (never
  translated) — over one thin divider rather than a tinted box in a box
  (a later change de-boxed the zones; both keep the page's key track). Every
  non-secret cli.toml key is editable; the key sets are one fact in
  `lib/polyforge-config-schema.ts` (`CLI_COMMON_KEYS`,
  `CLI_ADVANCED_TOP_LEVEL_KEYS`, `CLI_ADVANCED_DAEMON_KEYS`,
  `CONNECTION_KEYS`, `CLI_WRITABLE_KEYS`) that the form and the write
  route's allowlist both consume. Visible without expanding: the five common
  keys `max_concurrent_jobs`, `step_timeout`, `stall_window`, `claude_bin`,
  `api_key` (decided by key, in pf's order). Each zone folds the rest behind
  one counted 「高级 (n)」 toggle (`configAdvancedCount`): `server`,
  `daemon_socket`, `user_id` (3) in the top-level zone and `harness_root`,
  `operator_agent_dir` (2) in `[daemon]`; any key pf adds later joins its
  zone's fold, read-only. The two folds reuse the #212 `cliToml` and `daemon`
  disclosure regions (no new stored field; a stored state keeps applying).
  The daemon-value column and its header exist only in the `[daemon]` zone
  (the #126 ruling). `api_key` stays write-only (a password input whose draft
  is dropped after every confirmation or cancel, never a value; the row says
  whether a key is set); the `[workspace."<path>"]` mapping is
  edited in the 工作区 card (a row's projects with Save or Remove, an add row
  for a new path), and a key the file declares offers Unset (a connection
  key's after the confirmation). The form refuses only pf's schema kinds and an
  empty string/path key (「a value is required」 under the row, before any
  request); every other value goes to pf, whose outcome or diff shows under
  the row.
- **Connection keys.** `server`, `api_key`, `daemon_socket`,
  `user_id`: Save validates the draft, then opens a page-level confirmation
  (`role="alertdialog"`, rendered after the cli.toml card and outside every
  fold, `configConfirmConnection`) stating that the panel's next pf call uses
  the new value while the running daemon keeps the old one until it restarts.
  Confirm issues exactly one write; Cancel writes nothing and keeps the draft
  (never the api_key's);
  a pending confirmation keeps its zone open like a draft. A saved connection
  row then shows an inline notice (`configConnectionNotice`) with a one-click
  Roll back that reuses `POST /api/polyforge/config/history` (a fresh read and
  the same CAS write, recorded as a new entry); `api_key` gets the notice but
  no rollback (its history is set/unset only). Every other key saves from its
  own row. No save reloads, shuts down or re-ensures the daemon: Reload stays
  its own explicit control. Unset of a connection key asks the
  same confirmation.
- **The cli.toml `[summarize]` zone and `[memory_policy]` blocks**,
  superseding the earlier workspace-yaml summarize: the
  three `summarize.*` keys are the cli.toml card's `cli.toml [summarize]`
  zone, all common rows, their value, source, class and schema only from
  `pf --json config list`, written as flat cli.toml keys (no `--cwd`, the
  cli.toml CAS) and editable with an unmapped cwd. The read-only
  `cli.toml [memory_policy]` blocks follow inside the same card, one group
  per workspace path (`default` first, the projects sorted), no write
  control. Below pf 2.20.21 (or an unknown version) both are absent and one
  localized upgrade hint stands in their place.
- **The 工作区 card**, mapping-only, holds
  the pane's Cell file's `[workspace."<path>"]` mapping rows (tagged with
  that file, `cli.toml` or `cli-<profile>.toml`), folded behind its row
  count (`configRowCount`). each row is editable — its
  projects with Save, or Remove — and an add row below the fold maps a new
  absolute path; every write is `pf config set|unset '[workspace."<path>"]'`
  pinned `--profile <name>` on a multi-Cell pf (the default Cell's included;
  an older pf has only cli.toml, so the default Cell's write is unflagged
  there), and the Host's
  overlap refusal shows under the row. In every card the legend
  and each zone's column header sit above the fold, so collapsing hides rows
  only. Expanding yields exactly one row per item with its write control
  unchanged.
- **Read-only rows** share one muted treatment wherever they are: the value,
  the workspace path, chips and the unset placeholder all take
  `--text-muted` under `.polyforge-config-row.is-readonly`, with no write
  control.
- **Read-outs** are closed by default: the history
  title carries its entry count (`configHistoryCount`), the install title no count.
  Each opens to today's content (rollback buttons). The install body is forced open, with no extra disclosure, until
  detection proves pf present and new enough (pending, absent or below the
  minimum); the forced state is never stored. The copy control sits on the
  install title line, so it is reachable without expanding.
- **Inline feedback.** A save, its client-side validation error, or a
  rollback renders its result under its own row/entry (keyed by `item.key` /
  `entry.id`; a connection row's notice rollback under that row), an error
  with `role="alert"`; copy feedback renders in the
  install block. The footer keeps page-level status only: detection, the
  package switch and Reload. The write request shapes are unchanged.
- **Locks.** A region holding an unsaved draft, a pending connection-key
  confirmation or a row error (a cli.toml zone's advanced keys are writable;
  the history block by a failed rollback) renders open
  whatever is stored, its toggle shows a visible marker
  (`configDraftMarker` / `configErrorMarker`) and a toggle attempt writes
  nothing. The marker is runtime state, never stored.
- **Storage.** Each region's open/closed state lives in this browser's
  localStorage under one key, `pi-web:polyforge-config-disclosure`
  (`lib/polyforge-disclosure.ts`, read by `hooks/usePolyforgeDisclosure.ts`
  through `useSyncExternalStore` with the all-closed map as the server
  snapshot): a JSON object of booleans for the seven regions `detect`,
  `cliToml`, `daemon`, `workspace`, `history`, `install` and `workflow`
  (the workflow card's write controls; the retired `summarize`
  and `candidates` regions are gone and a stored value naming them is
  ignored), all closed by default. An absent, blocked, unreadable, corrupt or
  wrong-typed value reads as the defaults, a partial object merges them, an
  unknown region is ignored, and a refused write never throws. A toggle
  storage refuses (blocked, quota) still takes effect: it lives in memory for
  the page's lifetime and is announced like a stored one, so every region stays
  reachable without storage; the next stored write makes storage the truth
  again. Nothing is sent to or read from the server, pf config or
  settings.json.

A "Jobs card stream" card carries exactly two numeric
controls for the dock's Jobs tab: title length (default 60, 10–200;
raised it from 30 so a goal fills the card's two-line clamp) and poll
interval in seconds (default 3, 1–60), each a `ConfigDetailGridRow` with its
input, a Save `ConfigButton` right after it and a visible hint. The values live
in this browser's localStorage only (`pi-web:polyforge-title-length`,
`pi-web:polyforge-poll-interval`, `lib/polyforge-panel-preferences.ts`): no
request, no pf config, no server file. The card renders whether or not pf is
detected; the inputs show the stored value clamped (absent/corrupt/non-positive
→ the default, a positive out-of-range value → its bound), and Save clamps,
stores and dispatches `pi-web:polyforge-preferences` so an open panel applies
it without a reload.

The configuration panel below the detection card is its own surface: the
machine-scope note is always visible, and the keys of `pf --json config list`
render in source cards, each with a title and a visible one-line
hint, in a fixed order — the cli.toml card (its top-level zone,
then its `[daemon]` zone; then its `[summarize]` zone and the
read-only per-path `[memory_policy]` blocks), then the 工作区 card
(the pane Cell's file's workspace mapping only — cli.toml or cli-<profile>.toml —
`configCards()` puts the helpers' summarize group in the cli.toml card). The grouping is presentation only and
lives in the import-free `components/polyforge-config-helpers.ts`: pf marks no
table membership, so the `[daemon]` zone uses a copy of pf's nine `[daemon]` key
names (a key pf adds later shows in the top-level zone until the list is
extended — cosmetic), a `section` item is a workspace mapping, a `summarize.`
key is summarize, and any other key stays in the top-level zone, never dropped.
The detection block and the package switch are cards too, and the detection
rows use the same key-column width as the configuration cards.

Every card and zone declares only its effective columns:
key | applies | file value for the top-level cli.toml zone and the 工作区 card
(a mapping row's value is its projects), key | applies | file value | daemon
value for the `[daemon]` zone. The key and applies tracks are page-level fixed widths
(`--polyforge-col-key`, `--polyforge-col-class` on `.settings-polyforge`; all
zones share them) and
the rows are CSS subgrids, so the value column starts at the same x in every
card and zone whether or not it has a daemon column; no card renders an empty
column, and a row is `is-readonly` (muted value, no write control) without
collapsing a cell.

**No mid-word breaks.** Every data-driven Latin token — key,
workspace path, read-only value, chip, history key/change, workflow source and
the install command — is rendered with `<wbr>` after one of `- _ / . : , ; |`
(`BreakableToken` in `components/PolyforgeConfig.tsx`), and the token styles
use `overflow-wrap: break-word` with `word-break: normal` (never `break-all`
or `overflow-wrap: anywhere`). A line therefore breaks only after one of those
separators or beside CJK with the whole text visible (no ellipsis, no nowrap
horizontal scroll); the sole exemption is one segment between separators that
is itself wider than its box. The install command stays monospace and wraps at
spaces first, a URL after `/` or `.`; its text and copy button are unchanged.
`e2e/polyforge-config-layout.mjs` pins this with a per-character
`Range.getClientRects()` scan at 1280/768/375 × en/zh-CN with every region open,
plus `scrollWidth ≤ clientWidth` for every card and zone.
The page also has one spacing/row scale (card gap, card padding, row height,
row divider), source tags inline with their key when width allows, history rows
aligned `key | old → new | 回滚`, and the workflow card's step blocks as
outlined blocks with a weightier title line and a subordinate editor inset —
hierarchy and spacing only, no content removed ( density pass). A key outside the three live daemon keys has no daemon cell
(its file value takes that width); a live daemon key whose daemon value is
unset shows the unset placeholder in that one cell. The Save button lives
inside the row's value cell, directly beside its input — never in a far-right actions column. A
workspace mapping row shows the workspace path (`shortenPath()`, display only)
with its projects as chips, never the raw `[workspace."…"]` TOML header; a
header that does not parse renders verbatim. pf's reload classes go through
`t()` (`restart-only`, `cli-only`, `live`, `next-claim`) with a per-card legend
and the raw class as the badge's tooltip; an unknown class renders through the
fallback, never as a bare token. List values (`summarize.models`) render as
individual chips with an add input and a remove control per chip, so no
comma-joined long string is displayed, while `formValue()` still splits the
comma string it builds before the write. The three daemon-column keys show the
file value beside the daemon's loaded value with a visible stale reason (and,
when `pf --json ops status` fails, the column says the daemon is not running),
and the summarize values are machine-level cli.toml values from
`pf --json config list` (editable whether or not the cwd is
mapped). Under 640px each row is two columns: the key on
the left with its class badge flush right on the same line (the badge is never
squeezed; a long key wraps instead), then the file value and the daemon value
each across the whole row below; the header hides and nothing overflows
horizontally. Also at ≤640px the Jobs-card preferences put their label above
a full-width control and the workflow chain-add input spans its row
. Wider layouts
keep the shared columns. Below the pf capability floor,
or an old list shape, the write controls are unavailable with visible reason
text while detection keeps working. The step candidates are no longer a
read-out here: the workflow card shows them per project with
their effective chains. The
write controls are schema-derived (numeric/duration bounds from pf, enforced
again client-side before the write so 0/9 cannot be submitted; free-text
language, ordered models, an api_key password field that clears after save,
and plain text for a key pf gives no schema — `server`, `daemon_socket`,
`user_id`, `claude_bin`, `harness_root`, `operator_agent_dir` — refused only
when empty).
That client-side guard lives in `lib/polyforge-config-schema.ts`, a pure,
import-free module: it is the only value the panel imports from the config
layer, so the server read module (`lib/polyforge-config.ts`, which pulls
`node:child_process` and the pi SDK) never enters the browser bundle; the
server read module consumes the same module's schema type. Save, reload and
rollback go through the routes above. The write result text
comes from pf's reload class: a restart-only key says it applies after a daemon
restart, a down daemon says the next start, and a next-claim key says the next
claim.

- **Cadence — never a poll.** Detection runs once each time the section
  becomes active (the section host keeps visited sections mounted and passes
  an `active` signal, so every open re-detects), again on the 「重新检测」
  control (`?force=1`, bypassing the presence cache), and automatically
  after a toggle (non-force). No timer exists in the section, and the dock's
  polling hook (`hooks/usePolyforge.ts`) is deliberately not reused. The
  activation fetch is guarded against React StrictMode's simulated dev remount
  (setup → cleanup → setup), so one open issues exactly one detect request.
- **未安装 state.** pf absent renders a clear 「未安装」 line — no blank
  panel, no error surface — plus the copyable install.sh install/upgrade
  command (`polyforge.settings.installCommand`, a fixed i18n string with
  placeholders only, identical in every locale, naming the release channel
  through `PF_INSTALL_REPO`; a copy button puts it on the clipboard, nothing
  ever executes the copied text). It wraps at spaces and, inside a URL,
  after `/` or `.`. The command is reachable in every
  state: folded while pf is detected healthy, open without a disclosure
  otherwise.
- **Rows** (folded behind the summary line). Version (verbatim, with the non-blocking 「版本过低」 hint below
  v2.18.1), line (内部线/公开线/未知), the pi skill package state
  (已安装/未安装, 已启用/未启用 — global settings only), the two read-only
  Claude Code items (skill symlink present/absent, SessionStart hook
  present/absent — no control either way), and the daemon row
  (可达/不可达 + pf's own stderr/不适用 without pf).
- **The one switch.** The pi package switch renders only when the skill is
  installed (未安装 shows no toggle — only the state and the install command).
  It is the section's only switch, and it writes through the existing global
  path exactly as PluginsConfig's package switch does: `POST /api/plugins`
  `{action: "enable"|"disable", source, scope: "global", cwd}`. The source is
  resolved from that listing's join (`scope === "global" &&
  packageName === "polyforge"`), never from the detect response (which
  carries no paths). With no project selected the switch renders disabled
  with visible reason text (the write path needs a `cwd`); it is never a
  tooltip-only title. Write failures surface as a visible footer status.
  Nothing in the section edits `cli.toml` or writes Claude Code config; the
  one install it runs is the install entry below, through its route.
- **Install / update entry** (`components/PolyforgeInstallCard.tsx`,
  `lib/polyforge-install*.ts`, `app/api/polyforge/install/**`). A Host-pane
  card shows the installed pf (`pf --version`), the release channel's latest
  tag (`gh release list`; the channel is `POLYFORGE_INSTALL_REPO`, equal to
  `bin/pi-web-release-channel.json` and the copyable command — a unit test
  keeps them equal), the running daemon's own version (`/status` `version`),
  the jobs an upgrade would be refused for (install.sh's rule: every status
  but `paused` and `end_pending`) and Pi Web's own version; a newer release,
  a daemon still on the old binary and Pi Web out of sync with pf are each a
  visible line. One button (`POST /api/polyforge/install`) fetches the
  channel's `tools/install.sh` with `gh api` into a private temp dir and runs
  `bash <file> - [host]`; the output streams line by line from
  `GET /api/polyforge/install/events` (SSE, replayed from a cursor) and ends
  in a stated conclusion. One run at a time (409); the run belongs to the
  server, so closing the page closes only the stream and a reopened page
  replays the output and the result. The card's line naming
  `tools/install.sh` shows only while a run is relevant
  (`polyforgeInstallRelevant`: pf absent, a newer release, a daemon on
  another binary, or a run in progress, refused or failed) — a healthy,
  current pf shows none of it. While no `cli.toml` carries an
  `api_key` (`needsConfig`, install.sh's `has_config` rule, the one the route
  enforces with a 400) the run writes `cli.toml`, so the card asks for the
  aihub host and the api key — on a first install and on an update of an
  installed pf alike — and the start's browser-side check asks for both
  before posting; with a key in `cli.toml` neither input renders. The key input is
  write-only (cleared on every submit) and the key reaches install.sh on
  stdin only — never argv, env, output, response or log (masked if a script
  ever prints it). The installer's environment is the server's minus its
  host runtime variables (`sanitizeProjectCommandEnvironment`:
  `PI_WEB_PASSWORD`, `PORT`, `NODE_ENV`, `NEXT_*`, `TMUX`), with the channel
  pinned; an inherited `PF_INSTALL_FORCE` / `PF_HOST` never reaches a run.
  install.sh's refusal that PF_INSTALL_FORCE=1 lifts is its exit 3 together
  with its remedy text naming PF_INSTALL_FORCE=1 (an exit 3 without it is a
  failure): the card shows its reason and jobs, and a forced update takes a
  second, separate confirmation that re-reads and lists the jobs running
  now and states that their agent trees are terminated. The route accepts
  `force` only with `refusedRun` naming that refusal, while it is still the
  last run and at most 10 minutes old, re-checked after every await. A
  stream the server refuses (401, 404) leaves the running state with a
  reason. The card reads once per open and never polls. Updating Pi Web itself is out of
  scope (it needs a restart that ends every session): the card only says
  when Pi Web is out of sync.

## Shared blocks
- Panels are built from the shared blocks in `components/SettingsUi.tsx` (`ConfigScopeTag`, `ConfigScopeSwitch`, `ConfigSaveTarget`, `ConfigAddSourceHeading`, `ConfigAddSourcePanel`, `ConfigDetailGrid`, `ConfigFooterStatus`, `ConfigTrustNotice`), `itemsToSwitch()` in `components/settings-ui-helpers.ts`, and `lib/display-path.ts`; a new settings section reuses them instead of copying a panel. `SettingsUi.tsx` takes every word as a prop and holds no state, so it renders outside the i18n provider; panels pass `t()` strings.
- **Every add pane chooses global or project in one place:** a `ConfigSaveTarget` (scope switch aria-labelled `config.saveTo`, plus the path that choice writes to) first under the pane's title, where a detail pane shows its `ConfigScopeTag` and path. Skills, Plugins and MCP get it through `ConfigAddSourceHeading` (title, the catalog links at its right, then the save target); Sub-agents put it in the detail header in place of the tag while creating. The submit button stays where its flow needs it (Install under the source box or on each skill result, Save in the footer, Add at the end of the MCP form), with why it waits beside it, not under the save target. The path is shown whole, never cut short: it wraps after a separator (`<wbr>`), then anywhere. Sub-agents offer both scopes whatever the folder's trust: profiles are not project resources that wait for it.
- **One inset, one scroll region.** The detail pane (`.config-detail`) keeps the shared 20 px inset on every edge and is the one vertical scroll region; a section page adds no extra bottom padding of its own ( removed Settings › Polyforge's `padding-bottom: 24px`, which made a 44 px bottom gap), and `e2e/polyforge-config-viewport.mjs` pins 20 ± 1 px on all four edges.
- **Why a control is unavailable is visible text** — a scope switch's `disabledReason` (under the switch's whole line, so controls passed as its `children` stay level with it), the note under a detail header, a footer summary that opens its diagnostics — never only a `title` tooltip, which a touch screen cannot show.
- `ConfigTrustNotice` shows its button only when given `onTrust`; Settings › MCP passes AppShell's trust dialog through `SettingsPanel` (see **Trust from Settings** in [mcp-settings.md](mcp-settings.md)). Skills and Plugins take the page's `trust` and reload after a decision but have no Trust button; adding one needs `onOpenTrustDialog` passed to them and `mcpProjectTrustable()`'s rule.
