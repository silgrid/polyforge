// The pi adapter (polyforge#85 85a1): reads pi's native files directly —
// never through SettingsManager.create(), which locks each file and
// busy-waits (lib/transcript-notices-server.ts) — and declares pi's
// capabilities. The version of an interactive session is the installed SDK
// package's version (owner ruling Q2 C); the Cell CLI version joins in 85a2
// once doctor provides it. Writes all pass the shared gate: pi 1.5.x is the
// known range, and no concept has an implemented write yet, so a known
// version answers not-implemented.
import { join } from "node:path";
import { defaultAgentDir } from "../http-dispatcher";
import {
  gatedWrite,
  PI_KNOWN_RANGE,
  versionInKnownRange,
  type HarnessAdapter,
  type HarnessReadContext,
  type VersionProbeResult,
  type WriteChange,
  type WriteResult,
} from "./adapter";
import type { ConceptId, ConceptPayload, ModelPayload } from "./concepts";
import {
  automationEnvStatus,
  credentialRef,
  readJsonRecord,
} from "./redact";

/**
 * The credential environment variables pi's own `--help` documents
 * (`Environment Variables:`). The names are pi's; the values never leave the
 * process — only whether each is set is reported.
 */
export const PI_AUTOMATION_ENV_KEYS = [
  "ANTHROPIC_AUTH_TOKEN",
  "ANTHROPIC_API_KEY",
  "ANTHROPIC_OAUTH_TOKEN",
  "OPENAI_API_KEY",
  "AZURE_OPENAI_API_KEY",
  "DEEPSEEK_API_KEY",
  "NVIDIA_API_KEY",
  "GEMINI_API_KEY",
  "GROQ_API_KEY",
  "CEREBRAS_API_KEY",
  "XAI_API_KEY",
  "FIREWORKS_API_KEY",
  "TOGETHER_API_KEY",
  "OPENROUTER_API_KEY",
  "AI_GATEWAY_API_KEY",
  "ZAI_API_KEY",
  "MISTRAL_API_KEY",
  "MINIMAX_API_KEY",
] as const;

/** The pi environment variables that steer the harness, names only. */
export const PI_STEERING_ENV_KEYS = ["PI_CODING_AGENT_DIR"] as const;

/** The installed pi SDK package's version, as next.config.ts reads it at build time. */
export function readPiSdkVersion(): string | null {
  // stub (85a1 u2): zero-value body, the red run's target
  return null;
}

/** pi's settings.json in `agentDir`, read without pi's file lock. */
export function piSettingsPath(agentDir: string): string {
  return join(agentDir, "settings.json");
}

/**
 * The structure probe of the write gate (Q2 C): the files a pi write would
 * touch must have a recognizable top-level structure. Null when recognized,
 * else the reason a write is refused.
 */
export function probePiStructure(agentDir: string): string | null {
  // stub (85a1 u2): zero-value body, the red run's target
  return "stub";
}

export function createPiAdapter(): HarnessAdapter {
  const agentDirOf = (ctx?: HarnessReadContext): string => ctx?.agentDir ?? defaultAgentDir();
  return {
    id: "pi",
    capabilities: {
      // stub (85a1 u2): zero-value declaration, the red run's target
      id: "pi",
      scopes: [],
      concepts: [],
      writableConcepts: [],
      readOnlyConcepts: [],
      configRootEnv: null,
      credentialsFollowConfigRoot: false,
      nativeDiagnostics: { declared: true, implemented: false },
    },
    async probeVersion(ctx): Promise<VersionProbeResult> {
      // stub (85a1 u2): zero-value body, the red run's target
      return { version: null, known: false, source: "pi SDK package" };
    },
    async read(concept: ConceptId, ctx): Promise<ConceptPayload | null> {
      // stub (85a1 u2): zero-value body, the red run's target
      void agentDirOf(ctx);
      void concept;
      return null;
    },
    async write(concept: ConceptId, change: WriteChange, ctx): Promise<WriteResult> {
      return gatedWrite(this, concept, change, ctx);
    },
  };
}

export type { ModelPayload };
