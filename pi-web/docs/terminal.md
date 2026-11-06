# Workspace Terminals

The Explorer terminal action opens or focuses a terminal for its selected cwd
in the right panel's existing tab bar. Each terminal tab keeps the cwd it was
created with. Files still mount only their active viewer; terminal panels stay
mounted behind inactive tabs, hidden panels, and session or project switches.

## Lifecycle

- Each new tab generates a random terminal ID before creation. Creation with
  the same ID and cwd is idempotent, including React Strict Mode's repeated
  effects. An existing ID cannot be reused for another cwd.
- `sessionStorage` retains terminal IDs, cwds, and the active terminal layout
  across refresh. Restored tabs first check the existing server instance and
  never silently start replacement processes after expiry or server restart.
- A new PTY gets a 120-second connection lease. Subscribing cancels expiry;
  the last subscriber leaving starts a new 120-second grace period. This also
  collects creations that never establish their initial connection.
- Hiding a panel, switching tabs, and unmounting a component only disconnect
  clients. Explicitly terminating a tab waits for creation and in-flight input
  before deleting the PTY. Restart waits for termination before creating a new
  ID. Failed termination leaves the tab available to retry.
- A shell exit closes the SSE stream and retains its output and exit code in
  the browser. Unobserved server records expire after the same grace period.
- Explicit termination and expiry signal the shell, escalating to SIGKILL after
  two seconds if it ignores SIGHUP. Server shutdown force-kills shells immediately.

## Transport

Output events carry a monotonically increasing UTF-16 offset in SSE `id`.
Reconnections use `Last-Event-ID` (or `after` on an explicit reconnect) and send
only the missing suffix. The server keeps at most 128 KiB of UTF-16 code units;
an older cursor triggers a terminal reset and bounded history replay. This is
bounded output history, not a serialized full-screen terminal snapshot. Slow
SSE consumers are disconnected once their response queue fills.

Input and resizes are serialized. Pending adjacent input is batched so remote
connections do not require one round trip per keystroke; large pastes are split
without splitting Unicode characters. Keystrokes typed before the server-side
terminal exists are held by the panel (`components/terminal-panel-input.ts`) and
flushed in order once creation (or attach) resolves, so the settle window no
longer swallows input. Failed input is not retried because its delivery may be
ambiguous. Reconnect attaches to the same process with a fresh writer; when the
terminal behind an explicit Reconnect has expired (server restart, lease
expiry), the tab is revived in place with the same id and a fresh shell —
restores themselves never silently spawn a replacement. Restart explicitly
replaces the process.

Soft-keyboard input that reaches the page only as `input` events (no keydown,
no composition — iOS/WKWebView keyboards, some IME commits) is forwarded by
the panel, because xterm's screenReaderMode drops that path. The suppression
of insertions xterm already delivered through its own keydown/keypress paths
is scoped to the actual event sequence: keyup and blur end it, so an arrow
key cannot make a later soft-keyboard insertion disappear. Compositions are
coordinated with xterm's own delivery rather than with candidate strings:
xterm's CompositionHelper delivers the commit itself from the textarea's
final value on a deferred timeout (a value that can differ from every
composition candidate), so the bridge forwards nothing while that delivery
is pending — not just a single commit echo some browsers fire after
compositionend, but every insertion up to xterm's own delivery, because
xterm reads the textarea's value at delivery time and a keystroke typed in
that window is already part of what xterm is about to send (swallowing only
the first such event would double-send the rest). That suppression ends on
the same deferred turn as xterm's own delivery — which for a cancelled or
empty composition sends nothing at all — or earlier at the keyup/blur/new-
composition boundaries: a finished IME commit cannot swallow later input
(including the very first character typed after an aborted composition),
and the commit is never sent twice.

The output stream (SSE) is independent of the input path: an SSE error or
reconnect leaves stdin enabled and typing keeps flowing through the writer,
which serializes delivery; a genuinely dead shell surfaces through the input
writer's failure path (stdin disabled, error banner) on the next keystroke.
Exit, failed input delivery, page hide/offline and terminal close still
disable stdin. Hiding the page or going offline also suspends the panel's
own forwarding — keystrokes that arrive while suspended are retained and
flush in order when the stream reconnects; a closing tab's buffer is dropped.
A failed shell start or failed input delivery drops the retained buffer as
well, so input typed around a dead start is never silently replayed into
the fresh shell a later Reconnect spawns. The stream itself lives in the
panel's lifecycle controller (`components/terminal-panel-stream.ts`): while
the page is suspended it refuses to open — or to recover, if its `open`
fires after the hide — even when `navigator.onLine` is still true (bfcache
pagehide), so retained input only ever flushes into a visible page once the
matching `pageshow`/`online` reconnects.

`bin/prepare-terminal.js` repairs node-pty 1.1.0's macOS spawn-helper executable
bits during installation, including published/npm-installed Pi Web packages.

Pi Web pins node-pty to `1.2.0-beta.15`, which includes Linux x64 and ARM64
prebuilt binaries. Native module loading is deferred until terminal creation,
so missing or incompatible binaries produce a JSON error with repair instructions.
Empty or non-JSON API errors show the HTTP status and direct users to the server log.

If a native binary cannot load, run
`npm rebuild node-pty --build-from-source --ignore-scripts=false --foreground-scripts` from the
installation directory (for npx, the cache directory containing `node_modules`).
On Debian/Ubuntu, install `python3` and `build-essential` first. This forces a
source build instead of reusing a missing or incompatible prebuilt binary.
Restart Pi Web after repair.

## Verification

Run `npm test` for native PTY, lease, output cursor, input queue, and storage
checks. `npm run test:terminal` starts an isolated development server and runs
desktop/mobile browser checks using generated session fixtures. Install the
Playwright Chromium browser first with `npx playwright install chromium`.
The browser check prints the temporary location of its screenshots and log.
