// The registered adapters: every route and the compare module ask here, so
// the harness set lives in one place (polyforge#85 85a1). New harness = new
// adapter, registered here.
import { listHarnessAdapters, registerHarness, type HarnessAdapter } from "./adapter";
import { createPiAdapter } from "./pi";
import { createClaudeAdapter } from "./claude";

const registered = new Set<string>();

/** The default adapters, each registered once per process (a later registration replaces an earlier one). */
export function ensureDefaultHarnesses(): HarnessAdapter[] {
  if (!registered.has("pi")) {
    registerHarness(createPiAdapter());
    registered.add("pi");
  }
  if (!registered.has("claude")) {
    registerHarness(createClaudeAdapter());
    registered.add("claude");
  }
  return listHarnessAdapters();
}
