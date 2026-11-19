"use client";

// The read-only Claude Code report (polyforge#150 85a4, owner ruling: the
// block the retired Claude page showed is taken over here): claude's version
// and capabilities as it reports them, beside Pi Web's three claude features
// with their basis. Presentational: the page fetches GET /api/claude and
// passes the answer; every word comes from the locale tables.
//
// SKELETON (the pre-test ordinary commit): the component renders nothing yet;
// the report's rows land with the page's implementation commit.

import type { ClaudeFeature } from "@/lib/claude-shared";

/** What the page reads of GET /api/claude: the claude on PATH as it reports itself. */
export interface ClaudeReport {
  available?: boolean;
  version?: string | null;
  versionRecorded?: boolean | null;
  recordedVersions?: string[];
  capabilities?: string[];
  features?: ClaudeFeature[];
  probeError?: string;
}

export function ClaudeCodeReport(_props: { report: ClaudeReport | null; error: string | null }): null {
  return null;
}
