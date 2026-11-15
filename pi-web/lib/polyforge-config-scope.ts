import { probePolyforgeVersion } from "./polyforge-status";
import {
  POLYFORGE_CELLS_UPGRADE_HINT,
  polyforgeScopeArgv,
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
 * names the Host. Host commands (`pf ops status`, `pf cell …`, the presence
 * probe) need no pin: pf scopes them to the Host unless an explicit
 * `--profile` targets a Cell. Reach: pi-web's own server routes only;
 * nothing here is a daemon or AIHub entry point.
 */

/** The protocol capabilities of the installed pf, as the dock's presence
 *  probe last answered them (`probePolyforgeVersion` in
 *  `lib/polyforge-status.ts` — the one detection implementation; its single
 *  spawn's machine line carries them, local and answerable with the daemon
 *  down); [] when pf is absent or reports none (an older pf). Cached with
 *  the probe (60 s present, 5 s absent): a route that must see an upgrade at
 *  once forces the probe first (every config write's floor check, the config
 *  and workflow reads, 「重新检测」). Spawns nothing itself; never throws. */
export async function readPolyforgeCapabilities(): Promise<string[]> {
  return (await probePolyforgeVersion()).capabilities;
}

/** One scope pinned for this pf: the argv, or why this pf cannot address it. */
export type PinnedPolyforgeScope = ({ ok: true } & PolyforgeScopeArgv) | { ok: false; reason: string };

/** Pin `scope` for the installed pf (the probe's capabilities): a
 *  multi-Cell pf gets `--profile NAME` / `--host` on every call; a pf without
 *  the multi-Cell protocol addresses only the default Cell (its one file,
 *  unflagged) and refuses the Host or a named Cell with the upgrade hint
 *  (`polyforgeScopeArgv`). Never throws; the caller spawns nothing scoped on
 *  a refusal. */
export async function pinPolyforgeScope(scope: PolyforgeConfigScope): Promise<PinnedPolyforgeScope> {
  const argv = polyforgeScopeArgv(scope, await readPolyforgeCapabilities());
  return argv ? { ok: true, ...argv } : { ok: false, reason: POLYFORGE_CELLS_UPGRADE_HINT };
}
