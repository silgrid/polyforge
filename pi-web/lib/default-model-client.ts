// Client-safe (85a3-1, AC2): what GET /api/models/default answers and the
// rules a fresh composer applies from it — the combined default's harness
// decides which harness a new session starts on, and claude's native defaults
// (Q2 A) are the initial model and effort a claude composer shows. A carried
// draft choice or the user's own pick always wins; the fetch only fills what
// nothing has touched yet. No Node import: the hook and the composer read it.
import {
  CLAUDE_EFFORT_DEFAULT,
  DEFAULT_CLAUDE_MODEL,
  PI_HARNESS,
  isClaudeEffort,
  isClaudeModelName,
  isSessionHarness,
  type ClaudeEffort,
  type SessionHarness,
} from "./claude-shared";

/** What GET /api/models/default answers (the route builds it server-side). */
export interface DefaultModelFetch {
  /** The stored combined default's harness: the harness a new session starts on. */
  defaultHarness: SessionHarness;
  /** claude's native `model`, "default" when unset — a new claude session's initial value. */
  claudeDefaultModel: string;
  /** claude's native `effortLevel`, "default" when unset — the composer's initial effort. */
  claudeDefaultEffort: ClaudeEffort;
}

/** Whether `body` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Parses the GET answer tolerantly; null when the body is not the shape (the
 * composer then keeps its built-in defaults). A partial body fills its
 * missing fields with the same defaults the route would answer.
 */
export function parseDefaultModelFetch(body: unknown): DefaultModelFetch | null {
  if (!isRecord(body)) return null;
  return {
    defaultHarness: isSessionHarness(body.defaultHarness) ? body.defaultHarness : PI_HARNESS,
    claudeDefaultModel: isClaudeModelName(body.claudeDefaultModel) ? body.claudeDefaultModel : DEFAULT_CLAUDE_MODEL,
    claudeDefaultEffort: isClaudeEffort(body.claudeDefaultEffort) ? body.claudeDefaultEffort : CLAUDE_EFFORT_DEFAULT,
  };
}

/** What a fresh composer still lets the fetch fill: nothing carried, nothing picked. */
export interface ComposerDefaultsGuards {
  /** The carried draft (a folder move) already chose a harness. */
  carriedHarness: boolean;
  /** The carried draft already chose claude's model. */
  carriedClaudeModel: boolean;
  /** The carried draft already chose claude's effort. */
  carriedClaudeEffort: boolean;
  /** The user already switched the harness. */
  pickedHarness: boolean;
  /** The user already picked claude's model. */
  pickedClaudeModel: boolean;
  /** The user already picked claude's effort. */
  pickedClaudeEffort: boolean;
}

/** What the fetch may still apply to the composer's state. */
export interface FetchedComposerDefaults {
  harness?: SessionHarness;
  claudeModel?: string;
  claudeEffort?: ClaudeEffort;
}

/**
 * The apply rules: the fetch fills only the fields no carried choice and no
 * user pick has touched — a late answer never overrides the user.
 */
export function resolveFetchedComposerDefaults(
  fetched: DefaultModelFetch,
  guards: ComposerDefaultsGuards,
): FetchedComposerDefaults {
  const apply: FetchedComposerDefaults = {};
  if (!guards.carriedHarness && !guards.pickedHarness) apply.harness = fetched.defaultHarness;
  if (!guards.carriedClaudeModel && !guards.pickedClaudeModel) apply.claudeModel = fetched.claudeDefaultModel;
  if (!guards.carriedClaudeEffort && !guards.pickedClaudeEffort) apply.claudeEffort = fetched.claudeDefaultEffort;
  return apply;
}
