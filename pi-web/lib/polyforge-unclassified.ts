/**
 * Server-side pf calls of the Host region's unclassified-error list
 * (polyforge#51). pf is the only way in, as for every other panel read:
 * `pf --json ops unclassified` → the daemon's GET /ops/unclassified
 * answer, sanitized through `sanitizePolyforgeUnclassified`. Host-level
 * (no --profile); a step agent can never read it (the daemon refuses its
 * process tree), and neither can this panel's own pf call pretend to be
 * one — pf here runs as the operator session.
 */

import { execFileAsync } from "./exec-file";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { PolyforgeError } from "./polyforge-status";
import {
  sanitizePolyforgeUnclassified,
  type PolyforgeUnclassifiedReport,
} from "./polyforge-unclassified-pure";

const READ_TIMEOUT_MS = 10_000;
/** 100 rows, each with a text capped at 1000 runes — plenty of headroom. */
const READ_MAX_BUFFER = 4 * 1024 * 1024;

/** The Host's unclassified-error report, sanitized. Throws PolyforgeError
 *  (pf's stderr, or an unreadable answer). */
export async function readPolyforgeUnclassified(): Promise<PolyforgeUnclassifiedReport> {
  let stdout: string;
  try {
    const result = await execFileAsync(polyforgePfBin(), ["--json", "ops", "unclassified"], {
      timeout: READ_TIMEOUT_MS,
      maxBuffer: READ_MAX_BUFFER,
    });
    stdout = result.stdout;
  } catch (error) {
    throw PolyforgeError.fromSpawnError(error);
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(stdout);
  } catch {
    throw new PolyforgeError("pf answered an undecodable unclassified report");
  }
  const report = sanitizePolyforgeUnclassified(parsed);
  if (!report) throw new PolyforgeError("pf answered an undecodable unclassified report");
  return report;
}
