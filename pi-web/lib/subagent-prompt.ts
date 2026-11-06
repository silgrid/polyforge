import { agentLanguageInstruction, type AgentLanguage } from "./agent-language";

export interface SubagentPromptPlan {
  chatOnly: boolean;
  appendSystemPrompt: string[];
  delegatedTask: string;
  exactSystemPrompt?: string;
}

export function buildSubagentPromptPlan(options: {
  profileSystemPrompt: string;
  tools: readonly string[];
  loadSkills?: boolean;
  loadExtensions?: boolean;
  promptMode?: "replace" | "append";
  task: string;
  inheritedParentContext?: string;
  /** The parent session's Agent language. */
  agentLanguage?: AgentLanguage;
}): SubagentPromptPlan {
  const chatOnly = options.tools.length === 0 && !options.loadSkills && !options.loadExtensions;
  const replacePrompt = options.promptMode === "replace";
  const appendSystemPrompt = [options.profileSystemPrompt];
  if (options.inheritedParentContext && !chatOnly) {
    appendSystemPrompt.push(options.inheritedParentContext);
  }
  // Append mode: the instruction is the last appended section. An exact prompt stays the
  // profile's text here; the skills binding appends the instruction after its composition.
  const instruction = agentLanguageInstruction(options.agentLanguage);
  if (instruction !== undefined && !chatOnly && !replacePrompt) appendSystemPrompt.push(instruction);
  return {
    chatOnly,
    appendSystemPrompt,
    delegatedTask: options.inheritedParentContext && chatOnly
      ? `${options.task}\n\n${options.inheritedParentContext}`
      : options.task,
    ...(chatOnly || replacePrompt ? { exactSystemPrompt: options.profileSystemPrompt } : {}),
  };
}
