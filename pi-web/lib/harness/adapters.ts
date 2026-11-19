// The registered adapters: every route and the compare module ask here, so
// the harness set lives in one place (polyforge#85 85a1). New harness = new
// adapter, registered here.
import { listHarnessAdapters, registerHarness, type HarnessAdapter } from "./adapter";
import { createPiAdapter } from "./pi";
import { createClaudeAdapter } from "./claude";

/** The default adapters, registered once per process. */
export function ensureDefaultHarnesses(): HarnessAdapter[] {
  // stub (85a1 u4): zero-value body, the red run's target
  void listHarnessAdapters;
  void registerHarness;
  void createPiAdapter;
  void createClaudeAdapter;
  return [];
}
