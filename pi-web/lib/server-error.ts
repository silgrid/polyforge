// 服务端错误的中文主文案 + 英文兜底（polyforge#259 T4 AC2，owner 2A）：中文界面里
// 出现的服务端英文串换成 i18n 文案，服务端原文只放进「详情」折叠。设置类 route
// 在原有的 `error` 旁补了 `code`（及 `params`）字段；前端用 `responseError` 把失败
// 响应变成带 code 的 `ServerError`，`serverErrorText` 按 code 取文案；没有 code
// 时按 HTTP 状态取，仍取不到就给一句中文兜底，原文留在 details 里。纯函数，单测
// 不依赖 React。

/** 服务端错误的载荷：code+params 的形状，或一句自由文本。 */
export interface ServerErrorPayload {
  code?: string | null;
  params?: Record<string, string | number> | null;
  /** 自由文本（服务端英文原文，或一条没有 code 的拒绝理由）。 */
  message?: string | null;
}

/** 一条错误在界面上要显示的两半：主文案（中文）与详情折叠里的原文。 */
export interface ServerErrorText {
  /** 主文案的 i18n key；null 表示没有对应文案，调用方用 id 或兜底。 */
  summaryKey: string;
  summaryParams?: Record<string, string | number>;
  /** 原文，只在「详情」折叠里显示；null 表示没有。 */
  details: string | null;
}

/** 已知 code 的文案表（一个 code 一个 key）。 */
const ERROR_SUMMARY_KEYS: Readonly<Record<string, string>> = {
  "http-401": "serverError.unauthorized",
  "http-403": "serverError.forbidden",
  "http-404": "serverError.notFound",
  "http-409": "serverError.conflict",
  "http-500": "serverError.server",
  // pf's own refusal codes, as the settings routes pass them through
  "config-cas-409": "serverError.conflict",
  "core-409": "serverError.conflict",
  // the settings routes' own refusal codes (skills / account / MCP writes)
  "skills-write-refused": "serverError.forbidden",
  "account-write-refused": "serverError.forbidden",
  "mcp-refused": "serverError.forbidden",
};

const FALLBACK_SUMMARY_KEY = "serverError.unknown";

/**
 * 一条服务端/适配器错误文本里的代码（polyforge#259 T4，owner 2A）：route 把它们
 * 已有的拒绝原话补成 `code` 字段，前端就能按 code 说中文。识别不了的返回 null。
 */
export function codeFromError(message: string | null | undefined): string | null {
  if (typeof message !== "string") return null;
  const text = message.trim();
  if (text === "") return null;
  // "CAS" only as its own upper-case word: "case", "lowercase", "cascade" are not it.
  if (/config-cas-409|changed since (it|the file) was read/i.test(text) || /\bCAS\b/.test(text)) return "config-cas-409";
  if (/request-denied|untrusted|forbidden/i.test(text)) return "http-403";
  if (/unauthori[sz]ed|not signed in/i.test(text)) return "http-401";
  const http = /HTTP\s*(\d{3})/i.exec(text);
  if (http) return `http-${http[1]}`;
  if (/not found|no such/i.test(text)) return "http-404";
  if (/conflict|already exists/i.test(text)) return "http-409";
  return null;
}

/** 一条带 code/params 的服务端拒绝：message 是服务端原文（只进详情）。 */
export class ServerError extends Error {
  readonly code: string | null;
  readonly params?: Record<string, string | number>;

  constructor(message: string, code: string | null = null, params?: Record<string, string | number>) {
    super(message);
    this.name = "ServerError";
    this.code = code;
    if (params) this.params = params;
  }
}

function stringOrNumberParams(value: unknown): Record<string, string | number> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const kept: Record<string, string | number> = {};
  for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
    if (typeof item === "string" || typeof item === "number") kept[key] = item;
  }
  return Object.keys(kept).length > 0 ? kept : undefined;
}

/**
 * 一次失败响应的 body（可能为 null）与状态码 → ServerError（owner 2A）：保留 route
 * 发出的 `code`/`params`，没有 code 时按 `http-<status>` 取（表里有才用）；原文
 * （连同 CAS 的 `diff`）留在 message 里，只给详情折叠用。
 */
export function responseError(body: unknown, status: number): ServerError {
  const raw = body && typeof body === "object" ? (body as Record<string, unknown>) : {};
  const error = typeof raw.error === "string" && raw.error.trim() !== "" ? raw.error : `HTTP ${status}`;
  const message = raw.diff !== undefined && raw.diff !== null ? `${error} (${JSON.stringify(raw.diff)})` : error;
  const bodyCode = typeof raw.code === "string" && raw.code.trim() !== "" ? raw.code.trim() : null;
  const statusCode = `http-${status}`;
  const code = bodyCode ?? (ERROR_SUMMARY_KEYS[statusCode] ? statusCode : null);
  return new ServerError(message, code, stringOrNumberParams(raw.params));
}

/** 一条 `{code, params}` / 自由文本 / Error 的服务端错误，拆成主文案与详情原文。 */
export function serverErrorText(payload: ServerErrorPayload | string | Error | unknown): ServerErrorText {
  const raw = toPayload(payload);
  const details = typeof raw.message === "string" && raw.message.trim() !== "" ? raw.message : null;
  const code = typeof raw.code === "string" && raw.code.trim() !== "" ? raw.code.trim() : null;
  // A known code speaks Chinese; an unknown one (or a bare text) gets the
  // generic Chinese line, and whatever English it came with stays in details.
  const summaryKey = (code !== null ? ERROR_SUMMARY_KEYS[code] : null) ?? FALLBACK_SUMMARY_KEY;
  const params = raw.params && Object.keys(raw.params).length > 0 ? { ...raw.params } : undefined;
  return { summaryKey, ...(params ? { summaryParams: params } : {}), details };
}

function toPayload(payload: unknown): ServerErrorPayload {
  if (typeof payload === "string") return { message: payload };
  if (payload instanceof ServerError) return { code: payload.code, params: payload.params ?? null, message: payload.message };
  // A plain Error says only its words; its code is never guessed from the text.
  if (payload instanceof Error) return { message: payload.message };
  if (payload && typeof payload === "object") return payload as ServerErrorPayload;
  return {};
}
