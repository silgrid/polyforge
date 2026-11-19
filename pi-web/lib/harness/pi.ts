// The pi adapter (polyforge#85 85a1): reads pi's native files directly —
// never through SettingsManager.create(), which locks each file and
// busy-waits (lib/transcript-notices-server.ts) — and declares pi's
// capabilities. The version of an interactive session is the installed SDK
// package's version (owner ruling Q2 C); the Cell CLI version joins in 85a2
// once doctor provides it. Writes all pass the shared gate (pi 1.5.x is the
// known range); the permission concept's write is the project trust decision
// (85a4, polyforge#150) — approval and sandbox are refused as pi's own absences.
import { readFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";
import { defaultAgentDir } from "../http-dispatcher";
import { readModelsConfig } from "../models-config-store";
import { readMcpServerConfigs } from "../mcp-config-read";
import { loadSkillsWithInstallInfo } from "../skills-service";
import { getProjectTrustStatus, mayReadProjectConfigNow } from "../project-trust";
import {
  gatedWrite,
  writeGate,
  PI_KNOWN_RANGE,
  versionInKnownRange,
  type HarnessAdapter,
  type HarnessReadContext,
  type VersionProbeResult,
  type WriteChange,
  type WriteResult,
} from "./adapter";
import {
  neutralReasoningEffort,
  type AuthPayload,
  type ConceptId,
  type ConceptPayload,
  type ConfigRootPayload,
  type EnvPayload,
  type McpPayload,
  type ModelPayload,
  type PermissionPayload,
  type SkillRef,
  type SkillsPayload,
} from "./concepts";
import { piEffectiveConfig } from "./effective";
import {
  automationEnvStatus,
  credentialRef,
  maskedMcpServer,
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
  try {
    const pkg = JSON.parse(readFileSync(join(process.cwd(), "node_modules", "@earendil-works", "pi-coding-agent", "package.json"), "utf8")) as { version?: unknown };
    return typeof pkg.version === "string" ? pkg.version : null;
  } catch {
    return null;
  }
}

/** pi's settings.json in `agentDir`, read without pi's file lock. */
export function piSettingsPath(agentDir: string): string {
  return join(agentDir, "settings.json");
}

/** Whether `value` is a string, narrowed. */
const isString = (value: unknown): value is string => typeof value === "string";

/**
 * The structure probe of the write gate (Q2 C): the files a pi write would
 * touch must have a recognizable top-level structure. Null when recognized,
 * else the reason a write is refused.
 */
export function probePiStructure(agentDir: string): string | null {
  const settingsPath = piSettingsPath(agentDir);
  if (existsSync(settingsPath) && readJsonRecord(settingsPath) === null) {
    return "settings.json has an unrecognized top-level structure (expected a JSON object)";
  }
  try {
    const models = readModelsConfig(join(agentDir, "models.json"));
    if (models.providers !== undefined && (typeof models.providers !== "object" || models.providers === null || Array.isArray(models.providers))) {
      return "models.json providers is not an object";
    }
  } catch {
    return "models.json could not be read as pi reads it";
  }
  return null;
}

export function createPiAdapter(): HarnessAdapter {
  const agentDirOf = (ctx?: HarnessReadContext): string => ctx?.agentDir ?? defaultAgentDir();
  const envOf = (ctx?: HarnessReadContext): NodeJS.ProcessEnv => ctx?.env ?? process.env;

  async function probeVersion(ctx?: HarnessReadContext): Promise<VersionProbeResult> {
    const version = ctx?.piVersion ?? readPiSdkVersion();
    const source = "pi SDK package";
    if (version === null) {
      return { version, known: false, source, reason: "the pi SDK package's version could not be read" };
    }
    if (!versionInKnownRange(version, PI_KNOWN_RANGE)) {
      return { version, known: false, knownRange: PI_KNOWN_RANGE.label, source, reason: `version ${version} is outside the known range ${PI_KNOWN_RANGE.label}` };
    }
    const structureReason = probePiStructure(agentDirOf(ctx));
    if (structureReason !== null) {
      return { version, known: false, knownRange: PI_KNOWN_RANGE.label, source, reason: structureReason };
    }
    return { version, known: true, knownRange: PI_KNOWN_RANGE.label, source };
  }

  return {
    id: "pi",
    capabilities: {
      id: "pi",
      scopes: ["user", "project"],
      concepts: ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"],
      writableConcepts: ["permission"],
      readOnlyConcepts: ["env", "effective-config"],
      configRootEnv: "PI_CODING_AGENT_DIR",
      credentialsFollowConfigRoot: true,
      nativeDiagnostics: { declared: true, implemented: false },
    },
    probeVersion,
    async read(concept: ConceptId, ctx): Promise<ConceptPayload | null> {
      const agentDir = agentDirOf(ctx);
      const env = envOf(ctx);
      switch (concept) {
        case "auth": {
          const payload: AuthPayload = {
            status: "unknown",
            credentialStore: credentialRef(join(agentDir, "auth.json")),
            automationEnv: automationEnvStatus(env, PI_AUTOMATION_ENV_KEYS),
            credentialsFollow: true,
          };
          return payload;
        }
        case "config-root": {
          const payload: ConfigRootPayload = { path: agentDir, env: "PI_CODING_AGENT_DIR", credentialsFollow: true };
          return payload;
        }
        case "model": {
          const settings = readJsonRecord(piSettingsPath(agentDir)) ?? {};
          let providers: ModelPayload["providers"] = [];
          try {
            const models = readModelsConfig(join(agentDir, "models.json")) as { providers?: unknown };
            if (models.providers !== null && typeof models.providers === "object" && !Array.isArray(models.providers)) {
              providers = Object.entries(models.providers as Record<string, unknown>)
                .filter(([, value]) => typeof value === "object" && value !== null && !Array.isArray(value))
                .map(([name, value]) => ({ name, hasApiKey: isString((value as Record<string, unknown>).apiKey) && (value as Record<string, unknown>).apiKey !== "" }));
            }
          } catch {
            providers = [];
          }
          const rawEffort = isString(settings.defaultThinkingLevel) ? settings.defaultThinkingLevel : null;
          const payload: ModelPayload = {
            defaultProvider: isString(settings.defaultProvider) ? settings.defaultProvider : null,
            defaultModel: isString(settings.defaultModel) ? settings.defaultModel : null,
            reasoningEffort: rawEffort === null ? null : { neutral: neutralReasoningEffort(rawEffort), raw: rawEffort },
            providers,
            customModels: [],
          };
          return payload;
        }
        case "permission": {
          // pi does no approval prompting and has no sandbox (AC1's 不做审批 /
          // 无沙箱); its one permission fact is the project trust decision.
          const payload: PermissionPayload = {
            approvalMode: { supported: false, neutral: null, raw: null },
            projectTrust: ctx?.cwd
              ? { trusted: getProjectTrustStatus(ctx.cwd, agentDir).trusted }
              : null,
            sandbox: { supported: false, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] },
            rules: null,
            denyInsteadOfPrompting: null,
            sources: { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null },
          };
          return payload;
        }
        case "skills": {
          // #146: name/path/source only; the state comes from the native
          // config (never frontmatter), and its read lands with that change.
          const listing = await loadSkillsWithInstallInfo(ctx?.cwd ?? agentDir);
          const entries: SkillRef[] = listing.skills.map((skill) => ({
            name: skill.name,
            path: skill.filePath,
            source: skill.sourceInfo.scope === "project" ? "project" : "user",
          }));
          const payload: SkillsPayload = { entries };
          return payload;
        }
        case "mcp": {
          const read = readMcpServerConfigs({
            agentDir,
            project: ctx?.cwd && mayReadProjectConfigNow(ctx.cwd, agentDir)
              ? { cwd: ctx.cwd, allowedRoots: new Set([ctx.cwd]) }
              : undefined,
          });
          const payload: McpPayload = {
            servers: read.servers.map((info) => maskedMcpServer(info.scope === "project" ? "project" : "user", {
              name: info.name,
              enabled: info.enabled,
              url: info.url,
              command: info.command,
              args: info.args,
              headerNames: info.headerNames,
              envNames: info.envNames,
            })),
          };
          return payload;
        }
        case "env": {
          const set = [...PI_AUTOMATION_ENV_KEYS, ...PI_STEERING_ENV_KEYS]
            .filter((name) => typeof env[name] === "string" && (env[name] as string) !== "")
            .sort();
          const payload: EnvPayload = { target: "pi", setKeys: set };
          return payload;
        }
        case "effective-config":
          return piEffectiveConfig(ctx);
        default:
          return null;
      }
    },
    async write(concept: ConceptId, change: WriteChange, ctx): Promise<WriteResult> {
      // The shared gate first (adapter.ts). pi's permission write is the
      // project trust decision alone: pi does no approval and has no sandbox,
      // so those answer refused with pi's own reason, never not-implemented —
      // the absence is a property of pi, not a TODO.
      if (concept !== "permission") return gatedWrite(this, concept, change, ctx);
      const probe = await probeVersion(ctx);
      const gate = writeGate(probe);
      if (!gate.allowed) return { status: "refused", reason: gate.reason };
      const keys = Object.keys(change);
      if (keys.includes("approvalMode")) {
        return { status: "refused", reason: "pi does no approval prompting (不做审批)" };
      }
      if (keys.includes("sandboxEnabled") || keys.includes("autoAllowBashIfSandboxed")) {
        return { status: "refused", reason: "pi has no sandbox (无沙箱)" };
      }
      if (keys.length !== 1 || keys[0] !== "projectTrust") {
        return { status: "refused", reason: `pi writes no ${keys.join(", ") || "empty change"} on permission` };
      }
      const raw = change.projectTrust;
      const trusted = typeof raw === "object" && raw !== null && !Array.isArray(raw)
        ? (raw as Record<string, unknown>).trusted
        : undefined;
      if (trusted !== true && trusted !== false) {
        return { status: "refused", reason: "projectTrust must be { trusted: boolean }" };
      }
      const cwd = ctx?.cwd;
      if (!cwd) return { status: "refused", reason: "a project trust write needs the project folder (cwd)" };
      const agentDir = agentDirOf(ctx);
      if (!getProjectTrustStatus(cwd, agentDir).requiresTrust) {
        return { status: "refused", reason: "the project requires no trust decision (no .pi/mcp.json), so none is recorded" };
      }
      new ProjectTrustStore(agentDir).set(cwd, trusted);
      return { status: "written", summary: `trust.json decision for ${cwd}: ${String(trusted)} (pi ${probe.version})` };
    },
    // pi's CLI declares no fixed safe diagnostic command yet (its doctor joins
    // with a later update): the visible reason, never a guessed command.
    diagnose: async () => ({ available: false, reason: "pi has no fixed safe diagnostic command yet" }),
  };
}
