# Harness adapters (统一 harness 配置层 · #85 85a1)

The harness configuration layer of parent ticket #85: one **unified concept**
vocabulary, one **capability table** per harness, and one **adapter** per
harness that reads (and later writes) the harness's own native config files.
The native files are the single source of truth; data native config has no
place for is stored beside it (step capabilities in pf Host config, UI
preferences in Pi Web). **Adding a harness = adding an adapter**: Codex,
opencode, Gemini CLI and Grok need no change anywhere else.

Not to be confused with `lib/claude-adapter.ts`, the *session converter* that
turns claude's stream-json into pi events ([claude-sessions.md](claude-sessions.md)).
The harness adapters here are configuration adapters under `lib/harness/`.

## The pieces

| File | What it is |
|---|---|
| `lib/harness/concepts.ts` | The neutral vocabulary: concept ids (`auth`, `config-root`, `model`, `permission`, `skills`, `mcp`, and the read-only `env`, `effective-config`), scopes (`managed/user/project/local/session` plus the `pi-web` pseudo-scope), the neutral reasoning-effort enum (`off<minimal<low<medium<high<xhigh<max`) and approval-mode enum, the per-concept payload types, and the `HarnessCapabilities` declaration. Client-safe: never imports a server-only module (85a1b's UI reuses it). |
| `lib/harness/adapter.ts` | The `HarnessAdapter` interface (`probeVersion`, `capabilities`, `read`, `write`, and `diagnose` as a signature only), the adapter registry, the known-version ranges and the **write gate**. |
| `lib/harness/pi.ts` / `lib/harness/claude.ts` | The two adapters. Both read native files directly and build every payload through `lib/harness/redact.ts`'s allow-list helpers. |
| `lib/harness/claude-layers.ts` | claude's settings layers (managed > `.claude/settings.local.json` > `.claude/settings.json` > `$CLAUDE_CONFIG_DIR/settings.json`), the managed-settings path and the state file's placement, shared by the adapter and the effective-value computation. |
| `lib/harness/effective.ts` | Per-layer effective values, each `{value, sourceScope, sourcePath, inferred: true}`. |
| `lib/harness/compare.ts` | The read-back comparison: `consistent` / `inconsistent` (each harness's value listed) / `unsupported`. |
| `lib/harness/redact.ts` | The allow-list: credential files as existence+path, env blocks as key names, MCP entries masked through `lib/mcp-secrets.ts` (the one masking implementation). |
| `lib/harness/diagnostics.ts` | Native diagnostics executed (85a1b, AC6): the capability table's **fixed safe command only** — claude's `claude doctor`, pi answering its visible reason until it has one — run without a terminal (stdin closed at once, so an interactive prompt reads EOF and cannot block), bounded by a deadline that SIGTERM then SIGKILL enforces, capped in lines and bytes, and filtered so no credential or environment variable value is ever echoed — three rules: an environment-variable-shaped pair (`^[A-Z][A-Z0-9_]*$` before `=` or `:`, credential-named or not) masks its whole value; a URL's `user:pass@` userinfo is masked wherever it sits, its host kept; a set secret-named variable's value (at least 8 characters, no whitespace) is masked even bare through the run's env — beside which secret-named pairs and key-looking tokens (`looksLikeSecretValue`) are masked as before, and non-secret-named bare values are left alone. A command that could print a secret is never started at all; the filter is the second layer, not the safety. |
| `app/api/harness/**` | The read routes: the harness list, one concept's read, the comparison, and one harness's native diagnostics (85a1b: `GET /api/harness/[harness]/diagnose`, the adapter's fixed safe command executed with its deadline and through the credential filter). All behind the API guard; `?cwd=` goes through the file-access check. |

## The write gate (owner ruling 2026-11-19, Q1 A + Q2 C)

Every write goes through `writeGate()` before anything is touched:

- **Version probe first.** pi's interactive-session version is the installed
  SDK package's version (`node_modules/@earendil-works/pi-coding-agent`, as
  `next.config.ts` reads at build time); the Cell CLI version joins in 85a2
  once doctor provides it. claude's version comes from `probeClaude()` — the
  one probe implementation (`lib/claude-probe.ts`); no second probe exists,
  and none may be added.
- **A known version** = a major.minor range with a floor (`claude 2.1.x`,
  ≥ 2.1.312; `pi 1.5.x`) **plus** a recognizable file structure: a settings
  file or state file that exists but is not a JSON object refuses the write.
  `CLAUDE_RECORDED_VERSIONS` keeps its own fixture semantics untouched.
- **Refusal answers** `{status:"refused", reason}`; a known version with a
  concept whose write is not implemented answers
  `{status:"not-implemented", reason}`. In 85a1 no concept write is
  implemented (`writableConcepts` is empty), so the gate is the whole write
  path; 85a2–85a3–85a4, 85c and 85d add the concept writes on top of it, and
  85c's claude side lands through the official command, so the write result
  type carries both a file write and a command run.
- **Reads are open to any version.**

## Redaction boundary

No credential value ever reaches a route response: auth is existence and
path only (`Q4 A`: the status itself is `unknown` until 85a2 runs the status
commands), env blocks appear as key names, `.credentials.json` and pi's
`auth.json`/`models.json` keys never leave the process, MCP urls and args go
through `maskUrl`/`maskArgs`, and the adapters never call a command that
prints a secret. The unit routes test scans every concept's response for
sentinel strings planted in exactly those files.

## Effective values are computed, never observed

Each effective value carries `inferred: true` (#85 d-5): claude applies its
settings `env` only after the child is spawned, and every harness merges its
layers its own way, so the layer walk is pi-web's own computation. Until the
Pi Web-stored claude settings migrate back to native files (#147, #150), the
claude side also lists that layer (`pi-web:launch-permission-mode` from
`pi-web-claude-settings.json`, `pi-web:tool-permissions` from
`claude-permissions.json`) under the `pi-web` scope.

## Known gaps (deliberate)

- **pi has no native diagnostic command yet** (85a2's doctor joins later):
  pi's `diagnose` answers `{available: false, reason: "pi has no fixed safe
  diagnostic command yet"}` — the visible reason, never a guessed command.
  claude's is implemented (85a1b): `claude doctor` through
  `lib/harness/diagnostics.ts`, `nativeDiagnostics.implemented: true`.
- **Account status is `unknown`** (Q4 A): `claude auth status` / `pi auth
  check` are 85a2's; the auth read answers only the credential store's
  existence, the automation env variable *names* set, and whether
  credentials follow the config root.
- **Claude's state-file details are best-known, display-only** (analysis
  risk 3): `.claude.json` is read from `$CLAUDE_CONFIG_DIR` when that is set
  (else `~/.claude.json`), and project trust reads the projects entry's
  `hasTrustDialogAccepted`. A wrong guess can only mislabel a display; the
  write gate never writes to the state file.
- **claude's settings layers' `mcpServers` is listed unverified**: claude's
  own MCP config is believed to live in `.claude.json` and `.mcp.json`;
  whether a settings layer's `mcpServers` is also a source has not been
  checked against claude itself. Listing it is read-only — a wrong listing
  shows a server claude would not load, never writes anything. 85a2
  verifies this against the real claude.
- **The `model` concept reads the user layer's defaults** (`defaultProvider`/
  `defaultModel`/`defaultThinkingLevel` of the agent settings), while the
  `effective-config` concept walks the project-over-user layers — so the two
  can differ on the same cwd. The effective view is the merged one; 85a3,
  which writes these keys, settles the display semantics.
- **`GET /api/harness` probes claude on its first call**: the list route asks
  the claude adapter for its version, which goes through `probeClaude`'s
  per-binary cache (one child process per binary per server life, up to 15 s
  when no cached claude answers); later calls read the cache. A UI that
  polls this route (85a1b) rides that cache.
- **Skill state is not read** (the #146 conflict): the skills concept lists
  name/path/source only; claude's native `skillOverrides` pass through as the
  harness's own words, and pi's frontmatter state is never read.

## Adding a harness

1. One adapter file under `lib/harness/` implementing `HarnessAdapter` — the
   probe, the capability declaration, the per-concept reads (through
   `redact.ts`), and `write` composed of `gatedWrite()`.
2. Register it in `lib/harness/adapters.ts` and extend `HarnessId` in
   `concepts.ts`.
3. The routes, the comparison and the capability table need no change; a harness with a fixed safe diagnostic adds its command to `lib/harness/diagnostics.ts` and flips its capability's `implemented`.
