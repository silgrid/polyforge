// pf-chat: gives every pi session a polyforge chat id (PF_CHAT_ID) and runs
// the resident `pf watch` for it.
//
// On session_start it asks the local daemon, via `pf chat init`, for the
// chat id of this pi session (a resumed session gets the same id). Every
// bash tool call is then prefixed with `PF_CHAT_ID=<id> export PF_CHAT_ID; `
// so pf commands the model runs know which chat they belong to — every
// command in the string, not just the first (polyforge#206). The id is a
// notification label, not a credential.
//
// Watching (v2-converge §3): the extension spawns the resident
// `pf watch --chat <id>`; its stdout carries ONLY fixed-template decision
// lines, each of which wakes the agent (sendMessage + triggerTurn). Progress
// never enters the model context: every 2s the widget is refreshed from
// `pf watch --chat <id> --statusline` (reads the watcher's progress file).
// Without pf or a daemon this does nothing — except that a failed
// `pf chat init` is retried every RESTART_MS until the session ends, so a
// daemon that comes up after the pi session (e.g. after a reboot) is picked
// up without reopening the session.
//
// Self-healing (polyforge#174): when the first `pf chat init` of a session
// fails, the extension runs `pf daemon ensure` once (in the background,
// bounded by ENSURE_MS) — after a container or machine restart nothing else
// brings the daemon back — and retries init right after it; the RESTART_MS
// retry loop stays the fallback. pi replays session_start when pi-web
// resumes its sessions after a restart; ensure is idempotent and
// file-locked, so many sessions resuming at once start one daemon.
//
// Step agents never load this file (the daemon runs them against its own
// agent dir, without the user's packages); PF_WI_ID below is the second
// fence.
import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { isToolCallEventType, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

const WIDGET_KEY = "polyforge";
const POLL_MS = 2000;
const RESTART_MS = 5000;
const ENSURE_MS = 40000;

export default function (pi: ExtensionAPI) {
  // Step agents (spawned by the daemon with PF_WI_ID in their env) are
  // workers, not the owner's chat: no chat id, no resident watcher, no
  // PF_CHAT_ID injection into their bash commands.
  if (process.env.PF_WI_ID) return;
  let chatId = "";
  let watcher: ChildProcess | undefined;
  let poller: ReturnType<typeof setInterval> | undefined;
  let restart: ReturnType<typeof setTimeout> | undefined;
  let initRetry: ReturnType<typeof setTimeout> | undefined;
  // Bumped on every stop; an in-flight or scheduled `chat init` from an
  // older session generation drops its result instead of starting anything.
  let generation = 0;
  let stopped = true;

  function stopWatching(ctx?: { ui: { setWidget(key: string, content: string[] | undefined): void } }) {
    stopped = true;
    generation++;
    if (poller) clearInterval(poller);
    if (restart) clearTimeout(restart);
    if (initRetry) clearTimeout(initRetry);
    poller = restart = initRetry = undefined;
    const child = watcher;
    watcher = undefined;
    if (child && !child.killed) child.kill("SIGTERM");
    ctx?.ui.setWidget(WIDGET_KEY, undefined);
  }

  function startWatcher() {
    if (stopped || !chatId) return;
    const child = spawn("pf", ["watch", "--chat", chatId], { stdio: ["ignore", "pipe", "ignore"] });
    watcher = child;
    child.on("error", () => {
      // pf missing: nothing to watch
      if (watcher === child) watcher = undefined;
    });
    createInterface({ input: child.stdout! }).on("line", (line) => {
      // Only fixed-template decision lines are ever printed on stdout.
      if (!line.startsWith("[polyforge] ")) return;
      pi.sendMessage(
        { customType: "polyforge-decision", content: line, display: true },
        { triggerTurn: true },
      );
    });
    child.on("exit", () => {
      if (watcher === child) watcher = undefined;
      // unexpected end (lock race, crash): retry unless the session ended
      if (!stopped) restart = setTimeout(startWatcher, RESTART_MS);
    });
  }

  pi.on("session_start", async (_event, ctx) => {
    stopWatching(ctx);
    chatId = "";
    const gen = generation;
    let ensured = false;

    // ensureThenInit starts the daemon if it is down (once per session
    // start), then asks for the chat id again; a failure falls through to
    // the RESTART_MS retry loop.
    const ensureThenInit = async () => {
      let ok = false;
      try {
        const res = await pi.exec("pf", ["daemon", "ensure", "--timeout", "30s"], { timeout: ENSURE_MS });
        ok = res.code === 0;
      } catch {
        // pf missing: the retry loop below keeps trying
      }
      if (gen !== generation) return;
      if (ok) {
        await tryInit();
      } else {
        initRetry = setTimeout(tryInit, RESTART_MS);
      }
    };

    // tryInit asks the daemon for this session's chat id. On failure (pf
    // missing, daemon down) it schedules itself again after RESTART_MS —
    // like the watcher's restart loop — until it succeeds or the session
    // ends. Retrying is safe: the daemon maps (harness, harness-session) to
    // the same chat id idempotently.
    const tryInit = async () => {
      initRetry = undefined;
      let id = "";
      try {
        const sessionId = ctx.sessionManager.getSessionId();
        const res = await pi.exec("pf", ["chat", "init", "--harness", "pi", "--harness-session", sessionId]);
        const out = res.stdout.trim();
        if (res.code === 0 && /^chat_[A-Za-z0-9_]+$/.test(out)) id = out;
      } catch {
        // pf missing or daemon down: retry below
      }
      if (gen !== generation) return; // session ended or restarted meanwhile
      if (!id) {
        if (!ensured) {
          ensured = true;
          void ensureThenInit(); // not awaited: never delays the session
          return;
        }
        initRetry = setTimeout(tryInit, RESTART_MS);
        return;
      }
      chatId = id;
      stopped = false;
      startWatcher();
      poller = setInterval(async () => {
        try {
          const res = await pi.exec("pf", ["watch", "--chat", id, "--statusline"]);
          const lines = res.code === 0 ? res.stdout.split("\n").filter((l) => l.trim() !== "") : [];
          ctx.ui.setWidget(WIDGET_KEY, lines.length > 0 ? lines : undefined);
        } catch {
          ctx.ui.setWidget(WIDGET_KEY, undefined);
        }
      }, POLL_MS);
    };
    await tryInit();
  });

  pi.on("session_shutdown", async (_event, ctx) => {
    stopWatching(ctx);
  });

  pi.on("tool_call", async (event) => {
    if (chatId && isToolCallEventType("bash", event)) {
      // pi runs the string as one `bash -c` (fallback `sh -c`). A bare
      // `PF_CHAT_ID=<id> cmd` assignment reaches only the first simple
      // command, so in `cmd1; pf submit` pf ran chat-less (polyforge#206).
      // An assignment before the special builtin `export` persists in the
      // shell (POSIX; bash and dash alike) and `export` marks it, so every
      // command in the string inherits the id; an explicit per-command
      // `PF_CHAT_ID=… pf` still wins.
      event.input.command = `PF_CHAT_ID=${chatId} export PF_CHAT_ID; ${event.input.command}`;
    }
  });
}
