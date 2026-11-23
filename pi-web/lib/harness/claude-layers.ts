// claude's config layers, shared by the adapter's concept reads and the
// effective-value computation (polyforge#85 85a1): the settings layers in
// claude's priority order, the managed settings path and the state file's
// placement. Read-only paths; nothing here writes.
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { claudeConfigDir } from "../claude-config-dir";
import type { HarnessReadContext } from "./adapter";
import type { ScopeId } from "./concepts";
import { readJsonRecord } from "./redact";

/** The managed settings file of the platform (claude's own policy layer). */
export function managedSettingsPath(): string {
  if (process.platform === "darwin") return join("/Library/Application Support/ClaudeCode", "managed-settings.json");
  if (process.platform === "win32") return "C:\\ProgramData\\ClaudeCode\\managed-settings.json";
  return join("/etc/claude-code", "managed-settings.json");
}

/**
 * The state file: inside $CLAUDE_CONFIG_DIR when it is set, else ~/.claude.json
 * (claude's own placement). Read-only display either way; a wrong guess cannot
 * write anything.
 */
export function claudeStateFilePath(ctx?: HarnessReadContext): string {
  if (ctx?.claudeConfigDir !== undefined) return join(ctx.claudeConfigDir, ".claude.json");
  const env = ctx?.env ?? process.env;
  const inherited = env.CLAUDE_CONFIG_DIR?.trim();
  return inherited ? join(claudeConfigDir(env), ".claude.json") : join(homedir(), ".claude.json");
}

/** One settings layer, in claude's priority order (highest first). */
export interface ClaudeSettingsLayer {
  scope: Exclude<ScopeId, "session" | "pi-web">;
  path: string;
  record: Record<string, unknown> | null;
}

/**
 * The custom model names of claude's own native modelPicker, merged across
 * the settings layers (deduped, user layer first; 85a4 — the retired
 * pi-web-stored list migrated into the user layer's options).
 */
export function modelPickerOptions(layers: ClaudeSettingsLayer[]): string[] {
  const names: string[] = [];
  for (const layer of [...layers].reverse()) {
    const picker = layer.record?.modelPicker;
    if (typeof picker !== "object" || picker === null || Array.isArray(picker)) continue;
    const options = (picker as { options?: unknown }).options;
    if (!Array.isArray(options)) continue;
    for (const option of options) {
      const model = typeof option === "object" && option !== null && !Array.isArray(option)
        ? (option as { model?: unknown }).model
        : undefined;
      if (typeof model === "string" && !names.includes(model)) names.push(model);
    }
  }
  return names;
}

/** claude's settings layers for `configDir` and an optional project `cwd`, highest priority first. `managedPath` is the managed settings file (tests override it; default managedSettingsPath()). */
export function claudeSettingsLayers(configDir: string, cwd: string | undefined, managedPath: string = managedSettingsPath()): ClaudeSettingsLayer[] {
  const paths: { scope: ClaudeSettingsLayer["scope"]; path: string }[] = [
    { scope: "managed", path: managedPath },
  ];
  if (cwd) {
    paths.push({ scope: "local", path: join(cwd, ".claude", "settings.local.json") });
    paths.push({ scope: "project", path: join(cwd, ".claude", "settings.json") });
  }
  paths.push({ scope: "user", path: join(configDir, "settings.json") });
  return paths.map((layer) => ({
    ...layer,
    record: existsSync(layer.path) ? readJsonRecord(layer.path) : null,
  }));
}
