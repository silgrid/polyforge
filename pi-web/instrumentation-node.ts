import { configureHttpDispatcher, readHttpIdleTimeoutMs } from "@/lib/http-dispatcher";
import { closeAllAgentEventStreams } from "@/lib/agent-event-stream";
import { ensureSessionWatcher } from "@/lib/session-watcher";
import { applyCodemodeWorkerDefault } from "@/lib/codemode-workers";

export function registerNodeInstrumentation(): void {
  // Before any session can run a codemode script (lib/codemode-workers.ts).
  applyCodemodeWorkerDefault();

  // The pi CLI applies settings' `httpIdleTimeoutMs` to the same undici
  // body/header timeouts, so a Web UI session gets the timeout the user
  // configured. Without it the built-in five minutes aborts a provider stream
  // that stays quiet for longer, which a CLI session tolerates.
  configureHttpDispatcher(readHttpIdleTimeoutMs());

  // Start the external-write watcher so TUI (or other pi process) session
  // writes invalidate the session list cache without a manual reload (pi#2).
  ensureSessionWatcher();

  // In production Next 16 answers SIGINT/SIGTERM with server.close() and waits
  // for every connection to end, without a timeout. SSE streams only end when
  // the client disconnects, so close them here or the process never exits.
  const shutdownStreams = () => closeAllAgentEventStreams();
  process.on("SIGINT", shutdownStreams);
  process.on("SIGTERM", shutdownStreams);

  // claude children run in process groups of their own: end each group
  // with the server, or a turn under way would outlive it. The module pulls the session
  // runtime, so it loads in the background like the SDK below; the sessions themselves
  // live in a globalThis registry the route bundles share.
  const claudeSessions = import("@/lib/claude-session");
  claudeSessions.catch(() => {});
  const shutdownClaude = () => {
    void claudeSessions.then((module) => module.shutdownAllClaudeSessions()).catch(() => {});
  };
  process.on("SIGINT", shutdownClaude);
  process.on("SIGTERM", shutdownClaude);

  // Next evaluates a route's code on its first request, so the first
  // /api/sessions used to pay for importing the whole pi SDK, seconds on
  // Windows (#964). Start that import now, unawaited so register() stays fast:
  // the launcher opens the browser only once the first page is served, after
  // it (bin/browser-opener.js), and the routes reuse Node's cached module.
  // A failure is left for the routes' own import to report.
  import("@earendil-works/pi-coding-agent").catch(() => {});

  // an unset pf `[daemon] agent_language` of the default Cell
  // takes Pi Web's stored Agent language, so pf's step agents use it without
  // a manual `pf config set`. A key pf's file sets is never overwritten; no pf,
  // an older pf without the key or a failing pf writes nothing. It spawns pf,
  // so it runs in the background like the imports above and never fails start.
  void Promise.all([import("@/lib/polyforge-config-write"), import("@/lib/agent-language-config")])
    .then(([write, config]) => write.reconcilePolyforgeAgentLanguage(config.readStoredAgentLanguage()))
    .catch(() => {});
}
