import { spawn } from "node:child_process";
import { polyforgePfBin } from "./polyforge-pf-bin";
import {
  POLYFORGE_CELLS_UPGRADE_HINT,
  polyforgeScopeArgv,
  sanitizePolyforgeCapabilities,
  type PolyforgeConfigScope,
  type PolyforgeScopeArgv,
} from "./polyforge-cells-pure";

/**
 * Server-side scope pinning for Settings › Polyforge. pf
 * resolves an unscoped Cell command from its own process — `--profile`, then
 * `PF_PROFILE`, then the cwd's mapped Cell, then `default` — and pi-web
 * spawns pf from its server process, whose cwd and environment the page
 * cannot see. So every per-Cell config read and write the page's routes make
 * names its Cell here (the default Cell included), and a Host config call
 * names the Host. Host commands (`pf ops status`, `pf cell …`,
 * `pf --json version`) need no pin: pf scopes them to the Host unless an
 * explicit `--profile` targets a Cell. Reach: pi-web's own server routes only;
 * nothing here is a daemon or AIHub entry point.
 */

const VERSION_TIMEOUT_MS = 15000;

/** The protocol capabilities of the installed pf (`pf --json version`:
 *  local, answerable with the daemon down); [] when pf is absent, fails or
 *  reports none (an older pf). Fresh on every call, so an upgrade shows on
 *  the next read. Never throws. */
export function readPolyforgeCapabilities(): Promise<string[]> {
  return new Promise((resolve) => {
    let child: ReturnType<typeof spawn>;
    try {
      child = spawn(polyforgePfBin(), ["--json", "version"], { shell: false, stdio: ["ignore", "pipe", "ignore"] });
    } catch {
      resolve([]);
      return;
    }
    let stdout = "";
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      resolve([]);
    }, VERSION_TIMEOUT_MS);
    child.stdout?.setEncoding("utf8");
    child.stdout?.on("data", (chunk: string) => { stdout += chunk; });
    child.on("error", () => {
      clearTimeout(timer);
      resolve([]);
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code !== 0) {
        resolve([]);
        return;
      }
      try {
        resolve(sanitizePolyforgeCapabilities(JSON.parse(stdout)));
      } catch {
        resolve([]);
      }
    });
  });
}

/** One scope pinned for this pf: the argv, or why this pf cannot address it. */
export type PinnedPolyforgeScope = ({ ok: true } & PolyforgeScopeArgv) | { ok: false; reason: string };

/** Pin `scope` for the installed pf (one `pf --json version` read): a
 *  multi-Cell pf gets `--profile NAME` / `--host` on every call; a pf without
 *  the multi-Cell protocol addresses only the default Cell (its one file,
 *  unflagged) and refuses the Host or a named Cell with the upgrade hint
 *  (`polyforgeScopeArgv`). Never throws; the caller spawns nothing scoped on
 *  a refusal. */
export async function pinPolyforgeScope(scope: PolyforgeConfigScope): Promise<PinnedPolyforgeScope> {
  const argv = polyforgeScopeArgv(scope, await readPolyforgeCapabilities());
  return argv ? { ok: true, ...argv } : { ok: false, reason: POLYFORGE_CELLS_UPGRADE_HINT };
}
