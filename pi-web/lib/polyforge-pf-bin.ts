/**
 * The one pf executable every call site spawns.
 *
 * A service process usually carries `PF_BIN` — deploy-piweb.sh resolves it and
 * pins it inside the restart loop, so a hostile `respawn-window` cannot drop
 * it — while its `PATH` is whatever shell last respawned the window. Spawning
 * the bare `pf` would then miss a perfectly reachable binary and make the dock
 * report "Polyforge is not installed". Resolve the configured absolute path
 * here, at spawn time (never cached at module load), and keep the old PATH
 * lookup for an install that has no PF_BIN.
 */
export function polyforgePfBin(): string {
  const configured = process.env.PF_BIN?.trim();
  return configured || "pf";
}