// 服务端错误的中文主文案 + 英文兜底（polyforge#259 T4 AC2）：中文界面里出现的
// 服务端英文串换成 i18n 文案，服务端原文只放进「详情」折叠。服务端回的是
// `{code, params}` 时按 code 取文案；只有自由文本（本票不改任何 route 的响应
// 形状）时给一句中文兜底，原文留在 details 里。纯函数，单测不依赖 React。

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
  if (/config-cas-409|changed since (it|the file) was read|CAS/i.test(text)) return "config-cas-409";
  if (/request-denied|untrusted|forbidden/i.test(text)) return "http-403";
  if (/unauthori[sz]ed|not signed in/i.test(text)) return "http-401";
  const http = /HTTP\s*(\d{3})/i.exec(text);
  if (http) return `http-${http[1]}`;
  if (/not found|missing|no such/i.test(text)) return "http-404";
  if (/conflict|already exists/i.test(text)) return "http-409";
  return null;
}

/** 一条 `{code, params}` / 自由文本的服务端错误，拆成主文案与详情原文。 */
export function serverErrorText(payload: ServerErrorPayload | string | null | undefined): ServerErrorText {
  const raw = typeof payload === "string" ? { message: payload } : payload ?? {};
  const details = typeof raw.message === "string" && raw.message.trim() !== "" ? raw.message : null;
  const code = typeof raw.code === "string" && raw.code.trim() !== "" ? raw.code.trim() : null;
  // A known code speaks Chinese; an unknown one (or a bare text) gets the
  // generic Chinese line, and whatever English it came with stays in details.
  const summaryKey = (code !== null ? ERROR_SUMMARY_KEYS[code] : null) ?? FALLBACK_SUMMARY_KEY;
  const params = raw.params && Object.keys(raw.params).length > 0 ? { ...raw.params } : undefined;
  return { summaryKey, ...(params ? { summaryParams: params } : {}), details };
}
/** 一条带 code/params 的服务端拒绝（桩）。 */
export class ServerError extends Error {
  code: string | null = null;
  params?: Record<string, string | number>;
}

/** 把一次失败的响应体变成 ServerError（桩）。 */
export function responseError(body: unknown, _status: number): ServerError {
  const error = (body as { error?: unknown } | null)?.error;
  return new ServerError(String(error ?? ""));
}
