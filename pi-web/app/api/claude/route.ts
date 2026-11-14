import { NextResponse } from "next/server";
import { buildClaudeEnv } from "@/lib/claude-argv";
import { readClaudePermissionConfig } from "@/lib/claude-permission-config";
import { claudePiWebFeatures, probeClaude } from "@/lib/claude-probe";
import { findClaudeBinary } from "@/lib/claude-session";
import { readClaudeSettings } from "@/lib/claude-settings";
import { CLAUDE_RECORDED_VERSIONS, DEFAULT_CLAUDE_MODEL, type ClaudeModelOption } from "@/lib/claude-shared";

export const dynamic = "force-dynamic";

// GET /api/claude - whether the claude harness can be offered (a `claude` on PATH) and
// what that claude reports: its model aliases from its `initialize`
// answer (lib/claude-probe.ts: one claude, no prompt, cached per binary) plus Settings ›
// Claude's custom names, `default` the default; its version, whether the fixtures cover
// it, and its own capabilities verbatim; beside them Pi Web's three claude features with
// their basis (claudePiWebFeatures(): approvals, background tasks, subagent text — from Pi
// Web's own evidence, the child's environment included; approvals from the global
// claude tool permissions, read only — r3). A failed probe still offers `default` (the alias
// the CLI resolves itself) plus the custom names, with the error. Writes nothing, creates
// no directory and never reads a credential. Behind proxy.ts's password gate.
export async function GET() {
  const binary = findClaudeBinary();
  const probe = binary ? await probeClaude(binary) : null;
  const info = probe && !("error" in probe) ? probe : null;
  const aliases: ClaudeModelOption[] = info && info.models.length > 0
    ? info.models.map((model) => ({ ...model, source: "alias" }))
    : [{ value: DEFAULT_CLAUDE_MODEL, source: "alias" }];
  const custom: ClaudeModelOption[] = readClaudeSettings().customModels
    .filter((name) => !aliases.some((alias) => alias.value === name))
    .map((value) => ({ value, source: "custom" }));
  return NextResponse.json({
    available: binary !== null,
    ...(binary ? {} : { reason: "not-on-path" }),
    models: [...aliases, ...custom],
    defaultModel: DEFAULT_CLAUDE_MODEL,
    version: info?.version ?? null,
    versionRecorded: info?.version ? info.recorded : null,
    recordedVersions: CLAUDE_RECORDED_VERSIONS,
    capabilities: info?.capabilities ?? [],
    features: claudePiWebFeatures(info?.version ?? null, buildClaudeEnv(process.env), readClaudePermissionConfig().mode),
    ...(probe && "error" in probe ? { probeError: probe.error } : {}),
  }, { headers: { "Cache-Control": "no-store" } });
}
