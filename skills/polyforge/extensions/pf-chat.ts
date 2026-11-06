// pf-chat: keeps the local polyforge daemon up for a human's pi session and
// asks for this machine's interactive role. The file name is historical —
// tools/install.sh and `pf doctor` reference the extension by this path.
//
// On session_start it runs, in the background (the session never waits):
//   1. `pf daemon ensure --timeout 30s` (polyforge#174), bounded by
//      ENSURE_MS — after a container or machine restart nothing else brings
//      the daemon back. pi replays session_start when pi-web resumes its
//      sessions after a restart; ensure is idempotent and file-locked, so
//      many sessions resuming at once start one daemon.
//   2. when ensure succeeded, `pf ops role interactive --if-unset`
//      (bounded by ROLE_MS; never `--force` — taking the role over from
//      another machine is the human's explicit act; `--if-unset` leaves a
//      role an operator chose explicitly alone — the daemon keeps that choice
//      across restarts — so `pf ops role runner` is not undone by the next
//      session start). A refusal is ignored.
// One attempt per session start; there is no retry loop.
//
// What it no longer does: it used to give the session a polyforge chat id
// (`pf chat init`), prefix every bash tool call with
// `PF_CHAT_ID=<id> export PF_CHAT_ID; `, run the chat's resident watcher,
// render its progress file as a widget and turn each decision line into an
// agent turn (sendMessage + triggerTurn). The chat
// subscriptions are retired: human decisions are read from the daemon's
// decision feed and handled in Pi Web's Polyforge panel, never by waking the
// agent.
//
// Step agents never load this file (the daemon runs them against its own
// agent dir, without the user's packages); PF_WI_ID below is the second
// fence.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const ENSURE_MS = 40000;
const ROLE_MS = 15000;

export default function (pi: ExtensionAPI) {
  // Step agents (spawned by the daemon with PF_WI_ID in their env) are
  // workers, not the owner's session: they never start the daemon.
  if (process.env.PF_WI_ID) return;
  let running = false;

  async function ensureThenRole() {
    try {
      const res = await pi.exec("pf", ["daemon", "ensure", "--timeout", "30s"], { timeout: ENSURE_MS });
      if (res.code !== 0) return;
      await pi.exec("pf", ["ops", "role", "interactive", "--if-unset"], { timeout: ROLE_MS });
    } catch {
      // pf missing or the daemon unreachable: nothing to do
    }
  }

  pi.on("session_start", async () => {
    if (running) return; // a previous session start's attempt is still in flight
    running = true;
    void ensureThenRole().finally(() => {
      running = false;
    }); // not awaited: never delays the session
  });
}
