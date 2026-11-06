import type { InlineExtension } from "@earendil-works/pi-coding-agent";
import { appendAgentLanguageInstruction, type AgentLanguage } from "./agent-language";

export const AGENT_LANGUAGE_EXTENSION_NAME = "pi-web-agent-language";

/**
 * A normal pi session's Agent language: for 中文, every run's system
 * prompt (as earlier handlers left it) gets the fixed instruction appended; English
 * returns nothing, so the prompt stays byte-identical. Registered last, so it appends
 * after pi-web's other prompt handlers. `agent.state.systemPrompt` is never assigned.
 */
export function createAgentLanguageExtension(getLanguage: () => AgentLanguage): InlineExtension {
  return {
    name: AGENT_LANGUAGE_EXTENSION_NAME,
    hidden: true,
    factory: (pi) => {
      pi.on("before_agent_start", (event) => {
        const language = getLanguage();
        if (language !== "zh-CN") return undefined;
        return { systemPrompt: appendAgentLanguageInstruction(event.systemPrompt, language) };
      });
    },
  };
}
