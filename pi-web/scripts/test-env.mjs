// The environment a test process runs in, defined once. `npm test`
// (scripts/tmp-guard.mjs) and the setup every test file imports first
// (scripts/test-env-setup.mjs) both build it here.
//
// A test must not depend on the runner's machine: no PI_WEB_* setting (an
// idle timeout of 0 breaks unrelated tests), no PF_* key (PF_BIN would route
// every pf call to the runner's pf, task markers would leak in), no tmux
// server, and no config read from the runner's HOME or XDG dirs (pf reads
// $XDG_CONFIG_HOME, else ~/.config, for its cli.toml). This is environment
// hygiene, not an OS sandbox.
import { join } from "node:path";

const DROPPED = new Set([
  "TMUX", "TMUX_PANE",
  "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
]);

/** A copy of `env` without PI_WEB_*, PF_*, tmux and XDG keys, with HOME and XDG_CONFIG_HOME under `home`. */
export function isolatedTestEnv(env, home) {
  const next = {};
  for (const [key, value] of Object.entries(env)) {
    if (key.startsWith("PI_WEB_") || key.startsWith("PF_") || DROPPED.has(key)) continue;
    next[key] = value;
  }
  next.HOME = home;
  next.XDG_CONFIG_HOME = join(home, ".config");
  return next;
}
