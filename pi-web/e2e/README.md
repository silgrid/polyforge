# CI and browser regression tests

Adapted from @Nuctori's CI/E2E proposal in #617 and #599. The performance
scanner, reverse reader, sidebar changes, and timing benchmarks are not included.

```sh
npm ci
npx playwright install chromium
npm run test:e2e
```

The script starts and stops its own Turbopack dev server on an available
loopback port. Run it in a checkout without an active dev server; Next.js
shares `.next/dev/lock` within a checkout. All fixtures are created before
startup in a temporary `PI_CODING_AGENT_DIR` and removed on completion.
No model credentials or existing Pi sessions are needed. Git is required for
the linked-worktree fixture.

CI runs lint, type checking, and unit tests in one job. A separate job builds
the application in a clean checkout and runs the same browser tests with
`E2E_SERVER_MODE=start` against `next start`. Do not build in a checkout used
for development. (The parallel runner's own build-once mode, below, builds into
`.next/e2e-build` and serves per-suite copies under `.next/e2e/`, so it never
touches the `.next` a running `next dev` uses.)

Coverage:

- A 5,000-message session opens with exactly the last 50 entries, bounded
  detail/context responses, and no browser errors.
- Desktop and mobile scrolling load two consecutive older pages. Each response
  has the expected IDs, no gaps or duplicates, and the messages appear once in
  the chat. A small session checks pagination through the root.
- Branch context follows the selected leaf and excludes the other branch.
- Markdown, code blocks, and real tool-call/tool-result blocks render.
- Chat width and font size persist, existing drafts resize, and short settings
  panels keep every language option reachable on desktop and mobile.
- Interface/chat and monospace font lists and weights apply live, survive
  refresh, sync between tabs and reset independently without changing width or
  font size. Headings and Markdown emphasis retain their weights.
- Unknown sessions and paths outside the fixture project are rejected.
- Settings › Models context windows (`model-context.mjs`, desktop and mobile,
  stubbed config/auth/model routes): a managed model's field shows the
  resolved window as its placeholder on the row's own line, edits wait for
  the shared Save, a saved override reopens, invalid text is refused without
  a write, a failed save keeps the draft, and clearing it restores the file
  with other providers' settings intact.
- The model picker (`model-selector.mjs`), with a synthetic catalog: on a phone, a simulated keyboard
  (visual viewport shrink, pan, a late height with no event) keeps the same
  filter focused and the list in the visible viewport. Native phone keyboards
  still need real-device verification.
- A local extension checks dialog keyboard navigation, Esc cancellation,
  collapse/expand draft preservation, countdown display, and server-side expiry.
- The sidebar archive pass (wi_tN1cVXKf1VIr / wi_uyIF45t806CP) drives the
  client-side archive contract through the real UI: row-menu archive from
  a pre-selected project (grill Batch 1 Q2 ruling b — the two-project
  alpha/beta fixture is kept, one project is pre-selected at test start via
  the global session search, and each main-list disappearance assertion
  targets only the selected project's session; the other project's
  archived session is asserted via search and the archive view),
  localStorage persistence across reload, `已归档 N` entry visibility,
  count, and stability across project switches, replacement-mode archive
  view with back button and project grouping, view-only open, unarchive,
  search badge, external-delete pruning, and zero server mutations.
  Desktop viewport only.

Model prompts, live model streaming, and agent execution are outside this suite.
Failures save a screenshot, Playwright trace, and server log under
`test-results/e2e/`; CI uploads that directory. Open a trace with
`npx playwright show-trace test-results/e2e/trace.zip`.

## Running every suite in parallel

`npm run test:e2e:all` (`e2e/all.mjs`) runs every standalone `e2e/*.mjs` suite
(same discovery as the release verify-scope engine: no helper modules, no
`exclude=` entries from `.polyforge/verify-scope.conf`) and is the conf's
`full_command`. Since this package joined the Polyforge repository
(`pi-web/`), the runner, the suites and their `// carries:` headers stay
rooted at the package directory; `lib/e2e-carries.test.mjs` (`npm test`)
fails on a header glob that names no tracked path under `pi-web/`, and on a
tracked path declared by more than 3 suites, naming those suites
(the locale tables `lib/i18n/*` are declared only by
`agent-language-293` and `polyforge`, the shell components only by `run`,
`session-stall-recovery` and `polyforge`). It also pins the light tier's bound:
the changes of, and a locale-tables-only change
each select at most 8 browser suites. The repository's root bar runs
`npm run test:e2e:all` from `pi-web/`. The standalone repository's release, deploy-lane and sync suites were retired with
the tooling they exercised (its release/deploy now live in the repository
root's `.polyforge/` scripts, tested there).

- Install once: `npm ci` and `npx playwright install chromium`. The runner
  never installs; it only checks both exist and shares them across suites
  (the Chromium install as one running browser server, see "Shared browser").
- Each browser suite starts its own `next dev` (in the default dev mode; see
  "Build-once mode" below for `next start`) with `PI_WEB_DIST_DIR=.next/e2e/<suite>`
  (`next.config.ts` `distDir`), so suites do not fight over `.next/dev/lock` and
  each one's compiled output lives in its own dir. Once every suite has
  settled, the runner removes the dist dir of each suite that passed (about
  360 MB each); a failed suite's dir is kept so its re-run
  (`npm run test:e2e:all -- <suite>`) starts warm, and the final green run
  clears it. An interrupted run (SIGINT/SIGTERM) removes nothing, and only
  `.next/e2e/<suite>` is ever removed: `.next/dev` and `.next` are left alone.
  Without the variable every suite uses `.next` as
  before. With it, `next.config.ts` points Next at `tsconfig.e2e.json` (it
  extends the repo config), so `next dev` never rewrites the checkout's
  `tsconfig.json`.
- Browser suites run `E2E_JOBS` at a time (default `min(6, max(2, floor(cpus/2)))`,
  `e2e/runner/jobs.mjs`, where cpus is `os.availableParallelism()` — the CPUs the
  runner may use, so an 8-cpu container counts 8, not the host's cores: 4 in the
  8-cpu verification container, 6 from 12 cores up, 2 on 4 cores or fewer. The
  former `floor(cpus/4)` (2 on 8 cpus) dates from every suite compiling its own
  `next dev`, when 4 made suites hit their page-load timeouts on a 12-core box;
  the bar now runs build-once `next start`, and at pool 2 used about 1.4 of 8
  cores); all other
  suites (tmux, `pgrep`, scratch HOME) run one at a time in a separate lane.
  The first run after a clean checkout compiles cold and is slower and more
  sensitive to the suites' page-load timeouts; lower `E2E_JOBS` if a loaded
  machine times out.
- The suites' scaffolding lives in one helper module, `e2e/harness.mjs`
: the scenario runner (`createCheck`), the bounded polls
  (`createWaitFor`, `createWaitUntil`, `eventually`), one suite's browser and
  server (`createHarness`: `launchBrowser`, `openPage`, `withPage`,
  `startServer`, `close`), the `/api/polyforge/*` route mock (`mockPolyforge`) and the context-usage mock,
  the host-session and claude-stub fixtures. A suite states its parameters
  (viewport, locale, storage, timeouts, failure-line shape) instead of copying
  a helper. It gets its browser through `h.launchBrowser(() => chromium.launch())`
: with the runner's shared browser named in `E2E_BROWSER_WS` it
  connects to it and `h.close()` drops only its own connection; without it (a
  suite run alone) its own `chromium.launch()` runs as before. That literal
  stays the runner's browser-lane marker. A change to the module selects every
  suite that imports it.
- Shared browser: the runner starts one Chromium server
  (`chromium.launchServer()`, default options) before the first browser suite
  and names its ws endpoint in `E2E_BROWSER_WS` for every browser suite, the
  `e2e/all: isolated` ones included; serial-lane suites never get the
  variable (an operator's own value is dropped). Each suite connects and
  opens its own contexts, so the suites on it share one browser, GPU and
  utility process instead of one set each (the two suites below that keep
  their own launch still start theirs); renderers and each suite's server stay
  per suite. Closing a suite drops only its contexts and connection; the runner
  closes the server once every lane has settled (or on SIGINT/SIGTERM: the
  runner owns those signals, so the server starts with Playwright's own
  handlers off, and a browser suite still waiting for the server when the
  signal comes is not started). A
  suite run alone (`node e2e/<suite>.mjs`, no variable) launches its own
  Chromium as before; `npm run test:e2e:all -- <suite>` starts the shared
  server for that one suite. `manual-fetch.mjs` (its touch input goes
  through CDP; the ticket keeps it on its own browser) and
  `pdf-page-fragment.mjs` keep their own launch (the latter needs the full Chromium's PDF viewer,
  `channel: "chromium"`, which the shared headless server does not have).
  If the server dies mid-run, a suite connected through `E2E_BROWSER_WS`
  whose connection drops without its own close fails with the scenario marker
  `FAIL: shared-browser`, indented below it `- the shared browser
  (E2E_BROWSER_WS=<ws>) disconnected while this suite ran: the runner's
  Chromium died`, so the failures section lists `FAIL e2e/<suite>:
  shared-browser — …` instead of only Playwright's "Target page, context or
  browser has been closed". The runner, which sees the server process exit,
  adds `[all.mjs] shared browser crashed (Target crashed): its server process
  exited (code …, signal …) while this suite ran` to each suite running on it
  at that moment, inside that suite's `===== FAIL <suite> =====` block, where
  verify classifies the failure as `browser_crash` (units.py `BROWSER_CRASH`)
  rather than an assertion; it names every crash before the summary
  (`e2e/all: the shared browser crashed N time(s): …`), and the next browser
  suite starts a new server, so one crash does not fail the rest of the run.
  `e2e/shared-browser.mjs` drives the suite side against a real
  `chromium.launchServer()`: closing one suite leaves the server and the other
  suites' pages running; SIGKILLing the server fails a connected suite with
  that marker; `lib/e2e-all-shared-browser.test.mjs` pins the runner side.
- Page-load and action ceilings live in one place, `e2e/timeouts.mjs`; a
  suite imports them instead of spelling its own number. The two page-load
  ceilings bound a single await (`page.goto`/`page.reload` or the first-text
  wait after it); all three stay bounded waits:
  - `COLD_MS` (env `E2E_COLD_MS`, default `480000`, 8 min): the warm-up load
    that compiles the route cold.
  - `NAV_MS` (env `E2E_NAV_MS`, default `180000`, 3 min): every other page
    load and first-text wait (the former 90 s and 60 s ceilings).
  - `ACTION_MS` (env `E2E_ACTION_MS`, default `30000`, 30 s): a suite's
    `page.setDefaultTimeout`, the window in which Playwright retries an
    action's actionability (a click waiting for its control to become visible,
    stable and enabled). It bounds no assertion and no wait that spells its
    own timeout; it is the load-sensitive bound
    because a busy host delays rendering, not correctness: the former 8 s of
    `polyforge-work-tab-163.mjs` timed out `locator.click` in the parallel bar;
    `polyforge-panel-213.mjs` (former 10 s) takes it
    as a precaution, since it shares that suite's dock click path, the shared
    `openPolyforgeDock()` in `e2e/polyforge-dock.mjs`. Other suites still spell their own default.

  Override in milliseconds, e.g.
  `E2E_COLD_MS=540000 E2E_NAV_MS=240000 E2E_ACTION_MS=45000 npm run test:e2e:all`.
  A blank or unset variable keeps the default; `0`, a negative or a non-numeric value is
  refused: the suite fails at start with an error naming the variable instead
  of running with a different ceiling. Keep `E2E_COLD_MS` below
  `E2E_SUITE_TIMEOUT_MS`, which still kills a hung suite. On a busy machine
  raise the ceilings before lowering `E2E_JOBS`; on a shared host already under
  someone else's load, lower `E2E_JOBS` first, since a longer ceiling does not
  reduce the load.
- A suite that asserts a wall-clock responsiveness bound, or whose scenarios
  are proven load-sensitive on a shared host, opts in with the
  `e2e/all: isolated` marker in its source: `session-stall-recovery.mjs` (its
  10s bound is the acceptance), `polyforge-work-tab-163.mjs`
  (8 s click timeouts in the parallel bar) and
  `polyforge-panel-213.mjs` (a precaution: named earlier and sharing
  work-tab-163's dock click path, `openPolyforgeDock()` in
  `e2e/polyforge-dock.mjs`; its S213-12 failures were a
  context-read race, fixed in the suite, not a load bound), and
  `polyforge-271.mjs` (proven load-sensitive: S271-17's former 1 s
  first-screen figure read 1288 ms at loadavg 67; its budget is now the
  renderer's frame/chunk cadence — first paint at most one 200-line chunk
  within 2 animation frames of the answer, at most one chunk per frame, no
  entry dropped — not a wall clock,). The
  runner starts those suites only after both lanes have drained, one at a
  time, so another `next dev` + Chromium cannot push the bound over while the
  suite passes alone.
- Output is buffered and printed in sorted suite order with a summary; a
  failing suite does not stop the others. `E2E_SUITE_TIMEOUT_MS` (default
  10 min) kills a hung suite and reports it as a FAIL.
- When anything failed, a `===== failures =====` section before the summary
  names every failing unit in one line:
  - `FAIL e2e/<suite>: <scenario> — <first message line>` for each scenario
    marker in the suite's output (`FAIL: <id>` with its message indented below,
    or `FAIL <id>: <message>` on one line);
  - `FAIL e2e/<suite>: no scenario marker — <line>` for a failing suite that
    printed no marker (a crash, a timeout kill or a spawn error), quoting the
    runner's own `[all.mjs]` trailer, else the suite's last output line;
  - a suite's own tally of red scenarios (`FAIL <suite>: <n> scenario(s)
    failed`, any `FAIL` line counting `<n> … scenario(s)`) is a count, not a
    scenario, and is not listed;
  - `host loadavg 1m/5m/15m: <x> / <y> / <z>` once per failing suite: the host's
    load average when that suite settled, two decimals each, the evidence a
    load-sensitive flake classification needs;
  - `re-run: npm run test:e2e:all -- <suite>` once per failing suite: the
    targeted re-run of exactly that suite, to copy as is (also on a base commit).
- Re-run only some suites: `npm run test:e2e:all -- polyforge.mjs run.mjs`.
  To reproduce failures on a base commit, re-run just the failing suites there,
  and reuse an installed `node_modules` when `package-lock.json` is unchanged
  rather than running `npm ci` per reproduction (`cp -al` the directory; a
  symlinked `node_modules` is rejected by Turbopack).

## Build-once mode

`E2E_SERVER_MODE` chooses how every browser suite gets its server
. Unset or `dev` is the default described above: each suite
runs its own `next dev` and compiles every route it touches. With `start`,
`e2e/all.mjs` runs `next build --webpack` (the package's `npm run build`)
once into `.next/e2e-build` before the lanes start, and every suite's server
is `next start` on that build:

```sh
E2E_SERVER_MODE=start npm run test:e2e:all                 # build once, then every suite
E2E_SERVER_MODE=start npm run test:e2e:all -- run.mjs      # a targeted re-run builds again
E2E_SERVER_MODE=start E2E_PREBUILT_DIST=.next/e2e-build npm run test:e2e:all -- run.mjs  # reuse a build
```

- The switch is process-wide and the only one: `e2e/next-server.mjs` reads
  `E2E_SERVER_MODE` itself and takes no per-suite mode, so every suite that
  starts its server through `startNextServer` (all 44 browser suites) follows
  it, a new suite included. The serial-lane `polyforge-config-panel.mjs` (no
  browser) still spawns its own `next dev` in either mode, under the
  `PI_WEB_DIST_DIR=.next/e2e/polyforge-config-panel` the runner hands it.
- Isolation is unchanged: each suite keeps its own port, `HOME`,
  `PI_CODING_AGENT_DIR`, tmux root and dist dir. The helper copies the build
  (without webpack's `cache/`: 54 MB, about 50 ms) into the suite's own
  `.next/e2e/<suite>` and serves that copy, because `next start` writes its
  route cache into the dir it serves (`server/route-cache`, the meta file with
  a plain non-atomic write), so two servers must not share one. A passing
  suite's copy is removed with its dir as in dev mode.
- `.next/e2e-build` itself is never removed by the runner (about 860 MB, of
  which 809 MB is webpack's cache); the next start-mode run rebuilds over it.
  `E2E_PREBUILT_DIST` (relative to this package) names an existing build and
  skips the build, but only a build of this tree: the runner stamps each build
  (`e2e-build-stamp.json`: the package's git tree, a hash of its uncommitted
  changes, and of `package-lock.json` and `next.config.ts`), and a dir whose
  stamp does not match, that has no stamp or no `BUILD_ID` is refused before
  any suite runs, naming what changed. A failed build (or one that exits 0
  without `BUILD_ID`) prints its output and fails the run with no suite started.
- CI's job is unchanged: `npm run build` into `.next`, then
  `E2E_SERVER_MODE=start npm run test:e2e`; without `E2E_PREBUILT_DIST` the
  helper serves the suite's own dist dir (`.next`) in place.
- Production behaviour differs from `next dev` only in timing so far: pages
  load in well under a second, which exposed reads that raced the server's
  1 s `pf ops status` reuse (`polyforge-cards` S1) or the job's watch snapshot
  (`polyforge-210` S210-02/S210-13, `polyforge-238` S238-04/S238-05); those
  scenarios now wait (bounded) for the data they assert on, in both modes.
- The production service worker registers only in this mode
  (`components/PwaRegistration.tsx`). It answers a navigation with
  `offline.html` when the fetch has no answer within 8 s (`public/sw.js`), and
  on a 2-CPU host a page's navigation fetch has stalled that long in the
  browser while the server answered the same URL in milliseconds, so the next
  page was "Pi Web is offline" and its controls never appeared
  (`polyforge-config-290` S290-8,). A suite that does not test
  the worker should create its contexts with `serviceWorkers: "block"`, as
  `next dev` never has one; so far only `polyforge-config-290` does.
  `public/sw.test.mjs` covers the worker itself.
- Leftovers: the runner kills each suite's process group, but check with both
  `pgrep -af 'next (dev|start)'` and `pgrep -af next-server`: a running
  `next start` retitles itself `next-server (v<version>)`, which the first
  pattern misses.

Verification runs in this mode. The repository root conf's pi-web bar is
`full_command=cd pi-web && E2E_SERVER_MODE=start npm run test:e2e:all` (a
`cd … &&` list, so the verification bar derives no targeted re-run from it); this
package's own conf, used when the engine runs from `pi-web/`, has
`full_command=E2E_SERVER_MODE=start npm run test:e2e:all` and keeps
`e2e_runner=npm run test:e2e:all --` unprefixed, because the engine
recognizes the runner file only in a value that begins `npm run <script>`
(a prefixed one would make `e2e/all.mjs` an undeclared suite and force the
full tier), so suites it selects still run on `next dev`.

Measured 2026-11-07 on a shared 32-vCPU x86_64 dev host (Node 24.22.2,
Next 16.4.4), `E2E_JOBS=3`, host load average 1–16 from other work during the
runs; each figure is one run unless noted:

| What | dev (`next dev` per suite) | start (one build, `next start` per suite) |
|---|---:|---:|
| `next build --webpack`, clean `.next` | — | 50.5 s (into `.next`), 51.6 s (into `.next/e2e-build`), 49–56 s inside the runner |
| rebuild with webpack's cache kept | — | 44.0 s |
| build size | — | 859 MB: 809 MB `cache/`, 50 MB servable |
| server ready (`GET /api/sessions` 200) | — | 0.84 s; `/` 200 in 10 ms |
| `polyforge-cards.mjs` alone | 108 s (twice) | 84 s |
| `chat-minimap-heading-334.mjs` alone | 13 s | 4 s |
| `run.mjs` alone (runs `chat-appearance`, `model-selector`) | 141 s | 71 s |
| those three in parallel: wall (per suite) | 147.6 s (110 / 13 / 147) | 85.0 s (85 / 13 / 72)¹ |
| every suite (53): wall | 1001 s, 53/53 | 561 s including the 58 s build, 52/53² |
| every suite: sum of per-suite seconds | 2723 s | 1335 s (−51 %); no suite slower |

¹ `chat-minimap-heading-334` still pinned `mode: "dev"` in that run.
² `polyforge-238` S238-05 read the summary before the watch snapshot landed;
  with the wait above it then passed 3/3 in start mode and 1/1 in dev.
