// 85d r2 (review r1#1): the zero-value stub for u2's red evidence — the
// signature with a body that answers nothing, so the unit's tests run at the
// test commit and fail on their own assertions (a missing module is no red).
// The real merge lands with the unit's implementation commit.
import type { SkillInfo, UnifiedSkillRow } from "./api-types";
import type { SkillRef } from "./harness/concepts";

/** claude's half of a listing, as the adapter reports it. */
export interface ClaudeSkillsListing {
  entries: SkillRef[];
  /** claude's native `skillOverrides` (settings layers): name → state word. */
  overrides?: Record<string, string>;
}

/** Answers nothing yet: no rows, so every merge assertion fails on its own. */
export function mergeSkillListings(
  _piSkills: SkillInfo[],
  _claude: ClaudeSkillsListing,
): UnifiedSkillRow[] {
  return [];
}
