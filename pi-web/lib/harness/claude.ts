// The claude adapter (polyforge#85 85a1): reads claude's native config —
// the settings layers (managed > .claude/settings.local.json >
// .claude/settings.json > $CLAUDE_CONFIG_DIR/settings.json), the state file
// .claude.json, .mcp.json and the skills directories — through the same
// redaction allow-list as pi. The version probe reuses probeClaude()
// (lib/claude-probe.ts), the one probe implementation; CLAUDE_RECORDED_VERSIONS
// keeps its fixture semantics and the write gate uses its own range (Q2 C).
import { join } from "node:path";
import { claudeConfigDir } from "../claude-config-dir";
import { probeClaude } from "../claude-probe";
import { findClaudeBinary } from "../claude-session";
import {
  gatedWrite,
  CLAUDE_KNOWN_RANGE,
  versionInKnownRange,
  type HarnessAdapter,
  type HarnessReadContext,
  type VersionProbeResult,
  type WriteChange,
  type WriteResult,
} from "./adapter";
import type { ConceptId, ConceptPayload } from "./concepts";
import { readJsonRecord } from "./redact";

/** The credential environment variables claude answers, names only. */
export const CLAUDE_AUTOMATION_ENV_KEYS = [
  "ANTHROPIC_API_KEY",
  "ANTHROPIC_AUTH_TOKEN",
  "CLAUDE_CODE_OAUTH_TOKEN",
] as const;

/** The managed settings file of the platform (claude's own policy layer). */
export function managedSettingsPath(): string {
  // stub (85a1 u3): zero-value body, the red run's target
  return "";
}

/** The state file: inside $CLAUDE_CONFIG_DIR when set, else ~/.claude.json. */
export function claudeStateFilePath(ctx?: HarnessReadContext): string {
  // stub (85a1 u3): zero-value body, the red run's target
  return "";
}

export function createClaudeAdapter(): HarnessAdapter {
  const configDirOf = (ctx?: HarnessReadContext): string => ctx?.claudeConfigDir ?? claudeConfigDir();
  return {
    id: "claude",
    capabilities: {
      // stub (85a1 u3): zero-value declaration, the red run's target
      id: "claude",
      scopes: [],
      concepts: [],
      writableConcepts: [],
      readOnlyConcepts: [],
      configRootEnv: null,
      credentialsFollowConfigRoot: false,
      nativeDiagnostics: { declared: true, implemented: false },
    },
    async probeVersion(ctx): Promise<VersionProbeResult> {
      // stub (85a1 u3): zero-value body, the red run's target
      void configDirOf(ctx);
      return { version: null, known: false, source: "claude initialize" };
    },
    async read(concept: ConceptId, ctx): Promise<ConceptPayload | null> {
      // stub (85a1 u3): zero-value body, the red run's target
      void configDirOf(ctx);
      void concept;
      return null;
    },
    async write(concept: ConceptId, change: WriteChange, ctx): Promise<WriteResult> {
      return gatedWrite(this, concept, change, ctx);
    },
  };
}
