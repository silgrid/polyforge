// claude's permission prompts in Pi Web (docs/agents/claude-sessions.md).
// claude asks with `control_request` / `can_use_tool`; the broker turns each request into
// Pi Web's existing extension dialogs (`select` / `input` with `timeout` / `expiresAt`,
// no new field or method), answers claude with one `control_response` stdin line, and
// closes the dialog with `extension_ui_closed`. Pure: the session injects emit / write /
// clock / timers. Rules:
//   - allow once → `allow` with the original input, `user_temporary`;
//   - "always allow in this session" only with claude's `permission_suggestions` and no
//     `suppress_always_allow_rule`; the suggestions go back with every `destination`
//     set to `session` (never a settings file), `user_permanent`;
//   - deny or Esc → `deny`, `user_reject`;
//   - AskUserQuestion: one dialog per question, answered `allow` + `updatedInput.answers`;
//   - unanswered for `CLAUDE_PERMISSION_TIMEOUT_MS` → `deny`, dialog closed, a late answer
//     writes nothing; claude's `control_cancel_request` and `closeAll()` (the child's exit)
//     close dialogs and write nothing.
import type { AgentEventLike } from "./agent-event-wire";
import type { AgentLanguage } from "./agent-language";
import { claudeControlSuccessLine, type ClaudeCanUseToolRequest, type ClaudePermissionDenied } from "./claude-adapter";
import type { ExtensionUiRequest, ExtensionUiResponse } from "./types";

/** How long a permission request waits for an answer before Pi Web denies it. */
export const CLAUDE_PERMISSION_TIMEOUT_MS = 30 * 60_000;

const SUMMARY_LIMIT = 400;
/** The deny messages claude reads (and its model sees): English whatever the session's language. */
const USER_DENIED = "The user denied this in Pi Web";
const TIMED_OUT = "Pi Web denied this: no answer within the permission timeout";

export interface ClaudePermissionTimers {
  set: (callback: () => void, ms: number) => unknown;
  clear: (handle: unknown) => void;
}

export interface ClaudePermissionBrokerOptions {
  emit: (event: AgentEventLike) => void;
  /** Writes one line to claude's stdin. */
  write: (line: string) => void;
  /** The session's Agent language: the dialogs' text (zh-CN Chinese, else English). */
  language: AgentLanguage;
  now?: () => number;
  timeoutMs?: number;
  timers?: ClaudePermissionTimers;
  /** Called once when a new request opens (the session's push notification). */
  onOpen?: (requestId: string, request: ClaudeCanUseToolRequest) => void;
}

const TEXT = {
  en: {
    wants: (tool: string) => `claude wants to use ${tool}`,
    allowOnce: "Allow once",
    allowSession: "Always allow in this session",
    deny: "Deny",
    other: "Other…",
    otherPrompt: "Type your answer",
    multiHint: "Pick one or more, separated by commas",
    denied: (tool: string) => `claude denied ${tool}`,
  },
  "zh-CN": {
    wants: (tool: string) => `claude 请求使用 ${tool}`,
    allowOnce: "允许一次",
    allowSession: "本会话内总是允许",
    deny: "拒绝",
    other: "其他…",
    otherPrompt: "输入你的回答",
    multiHint: "可选多项，用逗号分隔",
    denied: (tool: string) => `claude 拒绝了 ${tool}`,
  },
} as const;

type Text = (typeof TEXT)[keyof typeof TEXT];

function textFor(language: AgentLanguage): Text {
  return language === "zh-CN" ? TEXT["zh-CN"] : TEXT.en;
}

interface AskQuestion {
  question: string;
  header: string;
  multiSelect: boolean;
  options: { label: string; description: string }[];
}

type Step =
  | { kind: "permission"; allowSession: boolean }
  | { kind: "question"; index: number }
  | { kind: "other"; index: number };

interface PendingRequest {
  requestId: string;
  request: ClaudeCanUseToolRequest;
  deadline: number;
  timer: unknown;
  /** AskUserQuestion's questions; null for an ordinary permission prompt. */
  questions: AskQuestion[] | null;
  answers: Record<string, string>;
  step: Step;
  dialog: ExtensionUiRequest;
  dialogSeq: number;
}

function truncate(text: string, limit = SUMMARY_LIMIT): string {
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}

function compactJson(value: unknown): string {
  try {
    return JSON.stringify(value) ?? "";
  } catch {
    return "";
  }
}

/** The input line a dialog shows: Bash's command, a file tool's path, else compact JSON. */
function inputSummary(toolName: string, input: Record<string, unknown>): string {
  if (toolName === "Bash" && typeof input.command === "string") return truncate(input.command);
  if (typeof input.file_path === "string" && ["Edit", "Write", "Read", "MultiEdit", "NotebookEdit"].includes(toolName)) {
    return truncate(input.file_path);
  }
  const json = compactJson(input);
  return json === "{}" ? "" : truncate(json);
}

function reasonText(reason: unknown): string {
  if (typeof reason === "string") return reason;
  if (reason && typeof reason === "object") {
    const record = reason as Record<string, unknown>;
    if (typeof record.reason === "string") return record.reason;
    return truncate(compactJson(reason), 200);
  }
  return "";
}

function askQuestions(request: ClaudeCanUseToolRequest): AskQuestion[] | null {
  if (request.tool_name !== "AskUserQuestion" || !Array.isArray(request.input.questions)) return null;
  const questions: AskQuestion[] = [];
  for (const raw of request.input.questions as unknown[]) {
    if (!raw || typeof raw !== "object") return null;
    const item = raw as Record<string, unknown>;
    if (typeof item.question !== "string" || !item.question || !Array.isArray(item.options)) return null;
    const options = (item.options as unknown[])
      .filter((option): option is Record<string, unknown> => !!option && typeof option === "object" && typeof (option as Record<string, unknown>).label === "string")
      .map((option) => ({ label: option.label as string, description: typeof option.description === "string" ? option.description : "" }));
    if (options.length === 0) return null;
    questions.push({
      question: item.question,
      header: typeof item.header === "string" ? item.header : "",
      multiSelect: item.multiSelect === true,
      options,
    });
  }
  return questions.length > 0 ? questions : null;
}

function questionTitle(question: AskQuestion): string {
  return question.header ? `${question.header}: ${question.question}` : question.question;
}

function optionText(option: { label: string; description: string }): string {
  return option.description ? `${option.label} — ${option.description}` : option.label;
}

export class ClaudePermissionBroker {
  private readonly requests = new Map<string, PendingRequest>();
  private readonly dialogs = new Map<string, PendingRequest>();
  private readonly text: Text;
  private readonly now: () => number;
  private readonly timeoutMs: number;
  private readonly timers: ClaudePermissionTimers;

  constructor(private readonly options: ClaudePermissionBrokerOptions) {
    this.text = textFor(options.language);
    this.now = options.now ?? Date.now;
    this.timeoutMs = options.timeoutMs ?? CLAUDE_PERMISSION_TIMEOUT_MS;
    this.timers = options.timers ?? {
      set: (callback, ms) => {
        const handle = setTimeout(callback, ms);
        handle.unref?.();
        return handle;
      },
      clear: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
    };
  }

  /** The claude requests waiting for an answer. */
  get size(): number {
    return this.requests.size;
  }

  /** Opens claude's request as a dialog; a request id already pending opens nothing. */
  open(requestId: string, request: ClaudeCanUseToolRequest): void {
    if (this.requests.has(requestId)) return;
    const questions = askQuestions(request);
    const suggestions = request.permission_suggestions;
    const pending: PendingRequest = {
      requestId,
      request,
      deadline: this.now() + this.timeoutMs,
      timer: undefined,
      questions,
      answers: {},
      step: questions
        ? { kind: "question", index: 0 }
        : { kind: "permission", allowSession: Array.isArray(suggestions) && suggestions.length > 0 && request.suppress_always_allow_rule !== true },
      dialog: undefined as unknown as ExtensionUiRequest,
      dialogSeq: 0,
    };
    pending.timer = this.timers.set(() => this.expire(requestId), this.timeoutMs);
    this.requests.set(requestId, pending);
    this.showStep(pending);
    this.options.onOpen?.(requestId, request);
  }

  /**
   * The page's answer to one of the broker's dialogs. False when the id is not an open
   * dialog of the broker's (already answered, timed out, cancelled, or not claude's).
   */
  respond(response: ExtensionUiResponse): boolean {
    const pending = this.dialogs.get(response.id);
    if (!pending) return false;
    this.closeDialog(pending);
    const value = "value" in response && typeof response.value === "string" ? response.value : undefined;
    if (value === undefined) {
      this.finish(pending, this.denyDecision(pending, USER_DENIED, "user_reject"));
      return true;
    }
    const step = pending.step;
    if (step.kind === "permission") {
      if (value === this.text.allowOnce) {
        this.finish(pending, this.allowDecision(pending, pending.request.input, "user_temporary"));
      } else if (step.allowSession && value === this.text.allowSession) {
        const updatedPermissions = (pending.request.permission_suggestions ?? []).map((update) => ({ ...update, destination: "session" }));
        this.finish(pending, { ...this.allowDecision(pending, pending.request.input, "user_permanent"), updatedPermissions });
      } else {
        this.finish(pending, this.denyDecision(pending, USER_DENIED, "user_reject"));
      }
      return true;
    }
    const questions = pending.questions ?? [];
    const question = questions[step.index];
    if (step.kind === "question" && !question.multiSelect) {
      if (value === this.text.other) {
        pending.step = { kind: "other", index: step.index };
        this.showStep(pending);
        return true;
      }
      const option = question.options.find((candidate) => optionText(candidate) === value);
      pending.answers[question.question] = option ? option.label : value;
    } else if (step.kind === "question") {
      pending.answers[question.question] = value.split(/[,，]/).map((part) => part.trim()).filter(Boolean).join(", ");
    } else {
      pending.answers[question.question] = value.trim();
    }
    if (step.index + 1 < questions.length) {
      pending.step = { kind: "question", index: step.index + 1 };
      this.showStep(pending);
    } else {
      this.finish(pending, this.allowDecision(pending, { ...pending.request.input, answers: pending.answers }, "user_temporary"));
    }
    return true;
  }

  /** claude withdrew the request: its dialog closes, nothing is written. */
  cancel(requestId: string): void {
    const pending = this.requests.get(requestId);
    if (!pending) return;
    this.closeDialog(pending);
    this.drop(pending);
  }

  /**
   * claude ended a turn: the main agent can no longer be waiting on a prompt of its own (it
   * cannot end a turn while it waits), so its requests close and nothing is written — a
   * stopped turn closes its dialog even when claude withdraws nothing. A subagent's request
   * (`agent_id`) may outlive the turn and stays.
   */
  endTurn(): void {
    for (const pending of [...this.requests.values()]) {
      if (!pending.request.agent_id) this.cancel(pending.requestId);
    }
  }

  /** The child is gone: every dialog closes, nothing is written. */
  closeAll(): void {
    for (const pending of [...this.requests.values()]) this.cancel(pending.requestId);
  }

  /** The open dialogs, in the order their requests arrived: what a new listener is replayed. */
  pending(): AgentEventLike[] {
    return [...this.requests.values()].map((pending) => pending.dialog as AgentEventLike);
  }

  private expire(requestId: string): void {
    const pending = this.requests.get(requestId);
    if (!pending) return;
    pending.timer = undefined;
    this.closeDialog(pending);
    this.finish(pending, this.denyDecision(pending, TIMED_OUT));
  }

  private allowDecision(pending: PendingRequest, updatedInput: Record<string, unknown>, decisionClassification: string): Record<string, unknown> {
    const toolUseID = pending.request.tool_use_id;
    return { behavior: "allow", updatedInput, ...(toolUseID ? { toolUseID } : {}), decisionClassification };
  }

  private denyDecision(pending: PendingRequest, message: string, decisionClassification?: string): Record<string, unknown> {
    const toolUseID = pending.request.tool_use_id;
    return { behavior: "deny", message, ...(toolUseID ? { toolUseID } : {}), ...(decisionClassification ? { decisionClassification } : {}) };
  }

  private finish(pending: PendingRequest, decision: Record<string, unknown>): void {
    this.drop(pending);
    this.options.write(claudeControlSuccessLine(pending.requestId, decision));
  }

  private drop(pending: PendingRequest): void {
    if (pending.timer !== undefined) this.timers.clear(pending.timer);
    pending.timer = undefined;
    this.requests.delete(pending.requestId);
  }

  private closeDialog(pending: PendingRequest): void {
    const id = pending.dialog.id;
    if (!this.dialogs.delete(id)) return;
    this.options.emit({ type: "extension_ui_closed", id });
  }

  private showStep(pending: PendingRequest): void {
    pending.dialogSeq += 1;
    const id = `claude-permission:${pending.requestId}:${pending.dialogSeq}`;
    const now = this.now();
    const timing = { timeout: Math.max(0, pending.deadline - now), expiresAt: pending.deadline };
    const step = pending.step;
    let dialog: ExtensionUiRequest;
    if (step.kind === "permission") {
      dialog = {
        type: "extension_ui_request",
        id,
        method: "select",
        title: this.permissionTitle(pending.request),
        options: [this.text.allowOnce, ...(step.allowSession ? [this.text.allowSession] : []), this.text.deny],
        ...timing,
      };
    } else {
      const question = (pending.questions ?? [])[step.index];
      if (step.kind === "other") {
        dialog = { type: "extension_ui_request", id, method: "input", title: questionTitle(question), placeholder: this.text.otherPrompt, ...timing };
      } else if (question.multiSelect) {
        dialog = {
          type: "extension_ui_request",
          id,
          method: "input",
          title: [questionTitle(question), ...question.options.map((option) => `- ${optionText(option)}`)].join("\n"),
          placeholder: this.text.multiHint,
          ...timing,
        };
      } else {
        dialog = {
          type: "extension_ui_request",
          id,
          method: "select",
          title: questionTitle(question),
          options: [...question.options.map(optionText), this.text.other],
          ...timing,
        };
      }
    }
    pending.dialog = dialog;
    this.dialogs.set(id, pending);
    this.options.emit(dialog as AgentEventLike);
  }

  private permissionTitle(request: ClaudeCanUseToolRequest): string {
    const name = typeof request.display_name === "string" && request.display_name ? request.display_name : request.tool_name;
    const lines = [this.text.wants(name)];
    for (const part of [request.title, request.description, reasonText(request.decision_reason)]) {
      if (typeof part === "string" && part.trim() && !lines.includes(part.trim())) lines.push(truncate(part.trim()));
    }
    const summary = inputSummary(request.tool_name, request.input);
    if (summary) lines.push(summary);
    return lines.join("\n");
  }
}

/** claude's `permission_denied` as a warning notice naming the tool and claude's reason. */
export function claudePermissionDeniedNotice(denied: ClaudePermissionDenied, language: AgentLanguage): AgentEventLike {
  const text = textFor(language);
  const head = text.denied(denied.toolName);
  const message = denied.reason ? `${head}${language === "zh-CN" ? "：" : ": "}${denied.reason}` : head;
  const notice: ExtensionUiRequest = {
    type: "extension_ui_request",
    id: `claude-permission-denied:${denied.toolUseId}`,
    method: "notify",
    message,
    notifyType: "warning",
  };
  return notice as AgentEventLike;
}
