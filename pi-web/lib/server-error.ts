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
};

const FALLBACK_SUMMARY_KEY = "serverError.unknown";

/** 一条 `{code, params}` / 自由文本的服务端错误，拆成主文案与详情原文。 */
export function serverErrorText(_payload: ServerErrorPayload | string | null | undefined): ServerErrorText {
  return { summaryKey: "serverError.unknown", details: null };
}
