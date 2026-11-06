import { execFileAsync } from "./exec-file";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { PolyforgeError } from "./polyforge-status";
import {
  sanitizePolyforgeDecisionFeed,
  type PolyforgeDecisionFeed,
  type PolyforgeDismissRequest,
} from "./polyforge-decisions-pure";

/**
 * Server-side pf calls of the Polyforge panel's decision feed.
 * The daemon keeps one machine-level feed of every human decision (every Cell
 * of the Host); pf is the only way in, as for every other panel read:
 *
 * - read: `pf --json watch decisions` → `GET /decisions` verbatim,
 *   `{decisions:[…], attention:N}`; the answer leaves only through
 *   `sanitizePolyforgeDecisionFeed` (lib/polyforge-decisions-pure.ts).
 * - dismiss: `pf --json watch dismiss <id>...` or `pf --json watch dismiss
 *   --all` → the daemon's answer verbatim, exactly `{"dismissed":N}` (the Go
 *   side pins it: TestWatchDismissJSONIsTheDaemonAnswer). A refusal is pf's
 *   non-zero exit; an answer without the count is no success either. The ids
 *   are validated before this is called.
 *
 * No `--profile`: the feed is Host-level. Failure text is pf's own bounded
 * stderr (`PolyforgeError.fromSpawnError`), never argv or `process.env`.
 */

const READ_TIMEOUT_MS = 10_000;
/** 300 retained entries, each with a task summary of any length. */
const READ_MAX_BUFFER = 16 * 1024 * 1024;

async function runPf(argv: string[]): Promise<string> {
  try {
    const { stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: READ_TIMEOUT_MS, maxBuffer: READ_MAX_BUFFER });
    return stdout;
  } catch (error) {
    throw PolyforgeError.fromSpawnError(error);
  }
}

/** The machine's decision feed, sanitized. Throws PolyforgeError (pf's stderr,
 *  or an unreadable answer). */
export async function readPolyforgeDecisions(): Promise<PolyforgeDecisionFeed> {
  const stdout = await runPf(["--json", "watch", "decisions"]);
  let parsed: unknown;
  try {
    parsed = JSON.parse(stdout);
  } catch {
    throw new PolyforgeError("pf answered an undecodable decision feed");
  }
  const feed = sanitizePolyforgeDecisionFeed(parsed);
  if (!feed) throw new PolyforgeError("pf answered an undecodable decision feed");
  return feed;
}

/** Marks open entries dismissed; answers how many the daemon dismissed.
 *  Throws PolyforgeError on a refusal or an answer without the count. */
export async function dismissPolyforgeDecisions(request: PolyforgeDismissRequest): Promise<number> {
  const targets = "all" in request ? ["--all"] : request.ids.map(String);
  const stdout = await runPf(["--json", "watch", "dismiss", ...targets]);
  let parsed: { dismissed?: unknown } | null = null;
  try {
    parsed = JSON.parse(stdout) as { dismissed?: unknown };
  } catch {
    parsed = null;
  }
  const n = parsed?.dismissed;
  if (typeof n !== "number" || !Number.isSafeInteger(n) || n < 0) {
    throw new PolyforgeError("pf answered an undecodable dismiss count");
  }
  return n;
}
