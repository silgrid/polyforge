// The Agent language: the language pi-web's own agents reply in.
// Client-safe: no Node import. Settings › General picks it, the server stores it
// (lib/agent-language-config.ts), and a session fixes its own copy at creation.

export const AGENT_LANGUAGES = ["en", "zh-CN"] as const;
export type AgentLanguage = (typeof AGENT_LANGUAGES)[number];

export const AGENT_LANGUAGE_DEFAULT: AgentLanguage = "en";

/**
 * The one instruction a 中文 session carries, byte-exact in both harnesses: claude's
 * `--append-system-prompt`, pi's appended system prompt, and the exact-prompt
 * profiles' appended text. English carries nothing at all.
 */
export const AGENT_LANGUAGE_INSTRUCTION =
  "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

export function isAgentLanguage(value: unknown): value is AgentLanguage {
  return typeof value === "string" && (AGENT_LANGUAGES as readonly string[]).includes(value);
}

/** Absent, corrupt or unknown reads as English. */
export function normalizeAgentLanguage(value: unknown): AgentLanguage {
  return isAgentLanguage(value) ? value : AGENT_LANGUAGE_DEFAULT;
}

/** The instruction for a language, or undefined for English. */
export function agentLanguageInstruction(language: AgentLanguage | undefined): string | undefined {
  return language === "zh-CN" ? AGENT_LANGUAGE_INSTRUCTION : undefined;
}

/** `prompt`, then one blank line and the instruction for 中文; `prompt` unchanged otherwise. */
export function appendAgentLanguageInstruction(prompt: string, language: AgentLanguage | undefined): string {
  const instruction = agentLanguageInstruction(language);
  return instruction === undefined ? prompt : `${prompt}\n\n${instruction}`;
}
