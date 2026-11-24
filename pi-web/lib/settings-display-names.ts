// 原始 id → 显示名（polyforge#259 T4 AC2）：设置页里每一处「以原始 id 作主文
// 字」的地方都改以显示名为主、原始 id 作次行等宽灰字。这里只放 id 到 i18n key
// 的映射，页面负责渲染；映射是纯函数，所以单测不依赖 React 或 i18n provider。

/** 一个原始 id 的显示名。 */
export interface DisplayName {
  /** 显示名的 i18n key；为 null 时没有翻译，调用方直接用 `id`。 */
  labelKey: string | null;
  /** 显示名消息的参数（「Cell {name}」之类）。 */
  params?: Record<string, string>;
  /** 原始 id，永远原样保留，作次行。 */
  id: string;
}

const CLAUDE_MODE_LABEL_KEYS: Readonly<Record<string, string>> = {};

/** claude 审批模式的显示名（`acceptEdits`、`plan` …）。 */
export function claudeApprovalModeName(mode: string): DisplayName {
  return { labelKey: null, id: mode };
}

/** `denyInsteadOfPrompting` 开关的显示名。 */
export const DENY_INSTEAD_OF_PROMPTING: DisplayName = { labelKey: null, id: "denyInsteadOfPrompting" };

/** 账号页凭据写入目标的显示名：`interactive` 与各 Cell 的 profile。 */
export function accountTargetName(target: string): DisplayName {
  return { labelKey: null, id: target };
}

/** 插件侧栏行的主文字：包名，缺包名时用源的末段，绝不是整条安装路径。 */
export function pluginRowName(pkg: { packageName?: string | null; source: string }): string {
  return pkg.source;
}