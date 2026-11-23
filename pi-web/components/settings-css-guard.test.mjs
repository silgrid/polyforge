// carries: app/settings.css, components/SettingsPanel*, components/SettingsUi*, components/HarnessModelSettings*, components/AccountSettings*, components/HarnessApplyTo*, components/UnifiedSetting*, components/McpConfig*, components/SkillsConfig*
//
// polyforge#257 (T2 止血) AC1: every className a settings component renders
// must resolve to a CSS rule. The guard walks SettingsPanel's own local-import
// closure (so a new settings component is scanned without editing this list),
// classifies every file as settings-owned or a general renderer (with its
// reason), and checks each className token against the two stylesheets the
// settings surfaces load. Expected values come from AC1 alone: a class the
// component renders and a rule the stylesheet declares — never from either
// side's output.
//
// A class counts as defined only as a selector: comments and string literals
// are stripped first, so neither can satisfy a class, and a longer class never
// answers for a shorter one (`.missing-class-extra` does not define
// `missing-class`). A value expression is read at its value positions only: a
// conditional's branches are classes, a comparison operand (`phase ===
// "success"`) is not. A composition whose static part is unknown
// (`config-button-${variant}`) is checked by its family — the stylesheet must
// declare a class in that family.
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const componentDir = dirname(fileURLToPath(import.meta.url));
const appDir = join(componentDir, "..", "app");
const STYLESHEETS = ["settings.css", "globals.css"].map((name) => readFileSync(join(appDir, name), "utf8"));
const PLACEHOLDER = "\u0000";
const shown = (text) => text.replaceAll(PLACEHOLDER, "%DYN%");

/**
 * Files in SettingsPanel's closure that belong to another surface: their
 * classes are styled for the chat, Markdown or terminal surfaces, not for the
 * settings dialog. Every other file of the closure is settings-owned and
 * scanned; the guard also fails when an entry here no longer names a file the
 * closure holds.
 */
const GENERAL_RENDERERS = {
  "MarkdownBody.tsx": "通用 Markdown 渲染器（聊天与技能预览共用）",
  "MermaidBlock.tsx": "Markdown 里的 mermaid 子渲染器",
  "SvgBlock.tsx": "Markdown 里的内联 SVG 子渲染器",
  "ImagePreview.tsx": "Markdown 里的图片预览子渲染器",
  "ModelSelector.tsx": "聊天输入框的模型选择器（Polyforge 页只是借用）",
  "SelectorRow.tsx": "聊天模型选择器的行",
  "TerminalPanel.tsx": "终端面板（设置只用它跑一次性登录命令）",
  "TerminalExtraKeys.tsx": "终端面板的额外按键条",
  "terminal-panel-input.ts": "终端面板的输入 helper",
  "terminal-panel-stream.ts": "终端面板的流 helper",
  "terminal-tab-state.ts": "终端面板的标签状态 helper",
  "ProviderIcon.tsx": "Provider 图标（聊天与设置共用，样式在图标自身）",
  "ProviderUsageSummary.tsx": "Provider 用量摘要（聊天侧边栏共用）",
};

/** Utility classes the stylesheets do not declare: Tailwind generates them (`@import "tailwindcss"` in globals.css). */
const UTILITY = {
  "sr-only": "Tailwind 工具类，globals.css 的 `@import \"tailwindcss\"` 在构建时生成",
};

/**
 * Every dynamic className composition in the settings closure and the finite
 * values its source produces: `file:expression` (whitespace collapsed) → those
 * values and where they come from. The guard composes each class name from
 * them and matches it against the stylesheets exactly — a composition without
 * an entry is reported by file and line, never skipped.
 */
const DYNAMIC_VALUES = {
  "McpAddServer.tsx:mcpImportNoteSeverity(note)": {
    values: ["error", "warning"],
    source: "lib/mcp-import-core.ts MCP_IMPORT_NOTE_SEVERITY（每个提示码映射 error / warning）与它的 warning 兜底",
  },
  "McpConfig.tsx:mcpCodemodeTone(state)": {
    values: ["error", "off", "on"],
    source: "components/mcp-config-helpers.ts mcpCodemodeTone 返回 McpStateTone",
  },
  "McpConfig.tsx:tone": {
    values: ["on", "off", "warning", "error"],
    source: "components/mcp-config-helpers.ts McpStateTone 联合类型",
  },
  "McpConfig.tsx:view.tone": {
    values: ["on", "off", "warning", "error"],
    source: "components/mcp-config-helpers.ts McpStateTone 联合类型",
  },
  "McpConfig.tsx:mcpRowStateTone(state)": {
    values: ["on", "off", "warning", "error"],
    source: "components/mcp-config-helpers.ts mcpRowStateTone 的四个分支",
  },
  "McpSignIn.tsx:lineClass": {
    values: ["mcp-config-line is-error", "mcp-config-line is-warning", "mcp-config-line"],
    source: "McpSignIn.tsx McpSignInOutcome 的三个 tone 分支（mcpSignInOutcomeTone 的 error / warning / 其余）",
  },
  "PolyforgeConfig.tsx:CELL_STATE_TONE[state]": {
    values: ["on", "warning", "error", "off"],
    source: "PolyforgeConfig.tsx CELL_STATE_TONE 的 Cell 状态取值",
  },
  "PolyforgeConfig.tsx:hostTone": {
    values: ["on", "off", "warning", "error"],
    source: "PolyforgeConfig.tsx EntryTone 联合类型（Host 行的徽标）",
  },
  "PolyforgeConfig.tsx:tone": {
    values: ["on", "off", "warning", "error"],
    source: "PolyforgeConfig.tsx EntryTone 联合类型（Cell 行的徽标）",
  },
  "PolyforgeInstallCard.tsx:shownState": {
    values: ["running", "succeeded", "failed", "refused", "lost"],
    source: "lib/polyforge-install-pure.ts PolyforgeInstallState（running / succeeded / failed / refused）加页面自己的 lost",
  },
  "SettingsUi.tsx:tone": {
    values: ["muted", "dim", "error"],
    source: "SettingsUi.tsx ConfigDetailGridRow 的 tone 联合类型，plain 不拼类",
  },
  "SettingsUi.tsx:variant": {
    values: ["primary", "secondary", "danger", "ghost"],
    source: "SettingsUi.tsx ConfigButtonVariant 联合类型",
  },
  "SettingsUi.tsx:size": {
    values: ["small", "default"],
    source: "SettingsUi.tsx ConfigButtonSize 联合类型",
  },
  "SkillsConfig.tsx:harness": {
    values: ["pi", "claude"],
    source: "lib/skills 的 harness 联合类型（pi / claude）",
  },
};

/**
 * An expression that renders nothing of its own: the forwarded `className` prop
 * (the caller's own class, checked at its call sites) and the literals that
 * stand for no class.
 */
const NO_CLASS = new Set(["className", "undefined", "null", "false", "true"]);

/** The expression's text with its whitespace collapsed — the registry's key. */
const normalizeExpr = (expression) => expression.replace(/\s+/g, " ").trim();

/** Every `.file` in the local-import closure of SettingsPanel.tsx, resolved under components/. */
function componentClosure() {
  const resolveLocal = (from, spec) => {
    const base = join(dirname(from), spec);
    for (const candidate of [base, `${base}.tsx`, `${base}.ts`]) {
      if (candidate !== base && existsSync(candidate)) return candidate;
    }
    return null;
  };
  const seen = new Set();
  const queue = [join(componentDir, "SettingsPanel.tsx")];
  while (queue.length > 0) {
    const file = queue.shift();
    if (seen.has(file)) continue;
    seen.add(file);
    const source = readFileSync(file, "utf8");
    for (const match of source.matchAll(/from\s+"(\.\/[^"]+)"/g)) {
      const next = resolveLocal(file, match[1]);
      if (next) queue.push(next);
    }
  }
  return [...seen].sort();
}

/** A stylesheet with comments and string literals removed, so neither can satisfy a class. */
function stripStylesheet(css) {
  return css
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/"(?:\\.|[^"\\])*"/g, '""')
    .replace(/'(?:\\.|[^'\\])*'/g, "''");
}

/** The classes the stylesheets declare in rule preludes (never in declaration bodies). */
function definedClasses(css = STYLESHEETS.join("\n")) {
  const found = new Set();
  for (const chunk of stripStylesheet(css).split(/[{}]/)) {
    if (chunk.trim().startsWith("@")) continue;
    for (const match of chunk.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) found.add(match[1]);
  }
  return found;
}

/** Whether a selector declares exactly this class (a longer class name never answers for a shorter one). */
const resolves = (token, classes) => classes.has(token);

/** Whether some declared class carries `stem` as its start (`is-` for a modifier family, `config-button-` for a variant one). */
function familyExists(stem, classes) {
  for (const name of classes) if (name.startsWith(stem)) return true;
  return false;
}

/** The index of the bracket closing the one at `start` (string- and template-aware). */
function matchingBracket(text, start) {
  const open = text[start];
  const close = { "[": "]", "(": ")", "{": "}" }[open];
  let depth = 0;
  let quote = null;
  for (let index = start; index < text.length; index += 1) {
    const character = text[index];
    if (quote) {
      if (character === "\\") index += 1;
      else if (character === quote) quote = null;
      continue;
    }
    if (character === '"' || character === "'" || character === "`") { quote = character; continue; }
    if (character === open) depth += 1;
    else if (character === close) { depth -= 1; if (depth === 0) return index; }
  }
  return -1;
}

/** The top-level items of a comma-separated list (nested brackets and strings kept whole). */
function splitTopLevel(text, separator) {
  const items = [];
  let depth = 0;
  let quote = null;
  let from = 0;
  for (let index = 0; index < text.length; index += 1) {
    const character = text[index];
    if (quote) {
      if (character === "\\") index += 1;
      else if (character === quote) quote = null;
      continue;
    }
    if (character === '"' || character === "'" || character === "`") { quote = character; continue; }
    if (character === "(" || character === "[" || character === "{") depth += 1;
    else if (character === ")" || character === "]" || character === "}") depth -= 1;
    else if (depth === 0 && character === separator) { items.push(text.slice(from, index)); from = index + 1; }
  }
  items.push(text.slice(from));
  return items;
}

/**
 * The branches of a top-level conditional (`cond ? a : b`, nested conditionals
 * followed), or null when the expression is not one. A branch is a value
 * position; the condition is not, so `phase === "success"` contributes no class.
 */
function conditionalBranches(expression) {
  const question = (() => {
    let depth = 0;
    let quote = null;
    for (let index = 0; index < expression.length; index += 1) {
      const character = expression[index];
      if (quote) {
        if (character === "\\") index += 1;
        else if (character === quote) quote = null;
        continue;
      }
      if (character === '"' || character === "'" || character === "`") { quote = character; continue; }
      if (character === "(" || character === "[" || character === "{") depth += 1;
      else if (character === ")" || character === "]" || character === "}") depth -= 1;
      else if (depth === 0 && character === "?" && expression[index + 1] !== ".") return index;
    }
    return -1;
  })();
  if (question === -1) return null;
  let depth = 0;
  let quote = null;
  let nested = 0;
  for (let index = question + 1; index < expression.length; index += 1) {
    const character = expression[index];
    if (quote) {
      if (character === "\\") index += 1;
      else if (character === quote) quote = null;
      continue;
    }
    if (character === '"' || character === "'" || character === "`") { quote = character; continue; }
    if (character === "(" || character === "[" || character === "{") depth += 1;
    else if (character === ")" || character === "]" || character === "}") depth -= 1;
    else if (depth === 0 && character === "?" && expression[index + 1] !== ".") nested += 1;
    else if (depth === 0 && character === ":") {
      if (nested === 0) return [expression.slice(question + 1, index), expression.slice(index + 1)];
      nested -= 1;
    }
  }
  return null;
}

/** A template literal's text: its static characters, a PLACEHOLDER for each `${…}` whose value is unknown. */
function composeTemplate(template) {
  let text = "";
  const dynamics = [];
  let cursor = 1;
  while (cursor < template.length && template[cursor] !== "`") {
    const character = template[cursor];
    if (character === "\\") { text += template[cursor + 1] ?? ""; cursor += 2; continue; }
    if (character === "$" && template[cursor + 1] === "{") {
      const start = cursor + 2;
      let depth = 1;
      let end = start;
      let quote = null;
      for (; end < template.length && depth > 0; end += 1) {
        const inner = template[end];
        if (quote) {
          if (inner === "\\") end += 1;
          else if (inner === quote) quote = null;
          continue;
        }
        if (inner === '"' || inner === "'" || inner === "`") { quote = inner; continue; }
        if (inner === "{") depth += 1;
        else if (inner === "}") depth -= 1;
      }
      const composed = composeValue(template.slice(start, end - 1));
      if (composed.dynamics.length === 0 && composed.text.trim().length > 0) {
        // a composition of literals (a conditional's branches): its own classes
        text += composed.text;
      } else {
        // an unreadable value: one placeholder, with the expression behind it
        text += PLACEHOLDER;
        dynamics.push(...(composed.dynamics.length > 0 ? composed.dynamics : [template.slice(start, end - 1)]));
      }
      cursor = end;
      continue;
    }
    text += character;
    cursor += 1;
  }
  return { text, dynamics };
}

/**
 * The text one className value expression renders, with a PLACEHOLDER where a
 * value is unknown, and the expressions behind those placeholders. Value
 * positions only: an array's items, a conditional's branches, a literal or a
 * template; a bare identifier (the forwarded `className` prop) renders nothing.
 */
function composeValue(expression) {
  const trimmed = expression.trim();
  if (trimmed.length === 0) return { text: "", dynamics: [] };
  if (trimmed.startsWith("[")) {
    const close = matchingBracket(trimmed, 0);
    if (close === -1) return { text: "", dynamics: [] };
    const items = splitTopLevel(trimmed.slice(1, close), ",");
    return items.reduce((total, item) => {
      const value = composeValue(item);
      return { text: `${total.text} ${value.text}`, dynamics: [...total.dynamics, ...value.dynamics] };
    }, { text: "", dynamics: [] });
  }
  const quote = trimmed[0];
  if (quote === '"' || quote === "'") {
    if (trimmed.length > 1 && trimmed.at(-1) === quote) return { text: trimmed.slice(1, -1), dynamics: [] };
    return { text: "", dynamics: [] };
  }
  if (quote === "`") return composeTemplate(trimmed);
  const branches = conditionalBranches(trimmed);
  if (branches) {
    return branches.reduce((total, branch) => {
      const value = composeValue(branch);
      return { text: `${total.text} ${value.text}`, dynamics: [...total.dynamics, ...value.dynamics] };
    }, { text: "", dynamics: [] });
  }
  // A bare expression — an identifier, a member, a call: its class is not
  // readable here, so it becomes a placeholder the registry must answer for.
  if (NO_CLASS.has(trimmed)) return { text: "", dynamics: [] };
  return { text: PLACEHOLDER, dynamics: [trimmed] };
}

/** Every className in a source file: its line, its static tokens and its composed (dynamic) tokens. */
function classNameTokens(source) {
  const staticTokens = [];
  const dynamicTokens = [];
  const lineAt = (index) => source.slice(0, index).split("\n").length;
  let index = 0;
  while ((index = source.indexOf("className=", index)) !== -1) {
    const line = lineAt(index);
    const start = index + "className=".length;
    index = start;
    const character = source[start];
    if (character === '"' || character === "'") {
      const end = source.indexOf(character, start + 1);
      for (const token of source.slice(start + 1, end).split(/\s+/).filter(Boolean)) staticTokens.push({ line, token });
      index = end;
      continue;
    }
    if (character !== "{") continue;
    const end = matchingBracket(source, start);
    if (end === -1) { index = start + 1; continue; }
    const composed = composeValue(source.slice(start + 1, end));
    index = end;
    let placeholder = 0;
    for (const token of composed.text.split(/\s+/).filter((entry) => entry.length > 0)) {
      const count = [...token].filter((entry) => entry === PLACEHOLDER).length;
      if (count === 0) { staticTokens.push({ line, token }); continue; }
      const exprs = composed.dynamics.slice(placeholder, placeholder + count).map(normalizeExpr);
      placeholder += count;
      dynamicTokens.push({ line, token, exprs });
    }
  }
  return { staticTokens, dynamicTokens };
}

const classes = definedClasses();
const tree = componentClosure();
const owned = tree.filter((file) => !Object.hasOwn(GENERAL_RENDERERS, relative(componentDir, file)));

test("every className a settings component renders resolves to a CSS rule (AC1)", () => {
  const missing = [];
  for (const file of owned) {
    const name = relative(componentDir, file);
    for (const entry of classNameTokens(readFileSync(file, "utf8")).staticTokens) {
      if (Object.hasOwn(UTILITY, entry.token)) continue;
      if (!resolves(entry.token, classes)) missing.push(`${name}:${entry.line} ${entry.token}`);
    }
  }
  assert.deepEqual(missing, [], `settings class tokens with no CSS rule: ${missing.join(", ")}`);
});

/** Every class name a composed token can render, from the values its placeholders take. */
function composedNames(token, valueLists) {
  const segments = token.split(PLACEHOLDER);
  let names = [segments[0]];
  for (let index = 0; index < valueLists.length; index += 1) {
    names = names.flatMap((prefix) => valueLists[index].map((value) => `${prefix}${value}${segments[index + 1] ?? ""}`));
  }
  return names.flatMap((name) => name.split(/\s+/).filter(Boolean));
}

test("every composed className is composed from the values its source enumerates, and each resolves (AC1)", () => {
  const problems = [];
  for (const file of owned) {
    const name = relative(componentDir, file);
    for (const entry of classNameTokens(readFileSync(file, "utf8")).dynamicTokens) {
      const valueLists = entry.exprs.map((expression) => {
        const registered = DYNAMIC_VALUES[`${name}:${expression}`];
        if (!registered) {
          problems.push(`${name}:${entry.line} ${expression} is a dynamic className the registry does not know — name its finite values and their source in DYNAMIC_VALUES`);
          return null;
        }
        return registered.values;
      });
      if (valueLists.some((values) => values === null)) continue;
      for (const composed of composedNames(entry.token, valueLists)) {
        if (!resolves(composed, classes)) {
          problems.push(`${name}:${entry.line} ${entry.exprs.join(", ")} composes ${composed}, which no rule declares`);
        }
      }
    }
  }
  assert.deepEqual(problems, [], problems.join("; "));
});

test("the settings scan covers the whole closure and no exclusion is stale (AC1)", () => {
  const files = new Set(tree.map((file) => relative(componentDir, file)));
  assert.ok(files.has("SettingsPanel.tsx"), "the closure starts at SettingsPanel.tsx");
  assert.ok(owned.length > 20, `the settings scan covers the panel's own components (scanned: ${owned.length}/${tree.length})`);
  for (const name of Object.keys(GENERAL_RENDERERS)) {
    assert.ok(files.has(name), `${name} is excluded as a general renderer but SettingsPanel's closure no longer holds it — drop the exclusion`);
  }
});

// The checker's own rules: the reds it must be able to report, so a green
// guard is not a guard that passes whatever it reads.
test("the CSS coverage guard rejects comment-only, partial and unregistered definitions (AC1)", () => {
  const commentOnly = definedClasses("/* .missing-class { color: red } */");
  assert.equal(resolves("missing-class", commentOnly), false, "a class named only in a comment is not defined");

  const partial = definedClasses(".missing-class-extra { color: red }");
  assert.equal(resolves("missing-class-extra", partial), true, "the real class resolves");
  assert.equal(resolves("missing-class", partial), false, "a longer class never answers for a shorter one");

  const family = definedClasses(".x:hover { color: var(--text) }");
  assert.equal(resolves("x", family), true, "a class with only a state rule is defined");

  const composed = classNameTokens('const a = <b className={`missing-family-${tone} is-grow`} />;');
  assert.deepEqual(composed.staticTokens, [{ line: 1, token: "is-grow" }], "a template's bare class is a static token");
  assert.deepEqual(composed.dynamicTokens.map((entry) => [entry.token, entry.exprs]), [[`missing-family-${PLACEHOLDER}`, ["tone"]]], "the composed token keeps its static part and the expression behind it");

  // Only a same-prefix neighbour exists: the composed class itself is missing.
  const prefixOnly = definedClasses(".is-warning-extra { color: red }");
  const tokens = classNameTokens('const a = <b className={`is-${severity}`} />;').dynamicTokens;
  const names = composedNames(tokens[0].token, [["warning"]]);
  assert.deepEqual(names, ["is-warning"], "the composed name is the whole class, not the family");
  assert.equal(names.some((entry) => resolves(entry, prefixOnly)), false, "a same-prefix class never answers for the composed one");

  // An identifier the registry does not know is reported, never skipped.
  const bare = classNameTokens('const a = <b className={lineClass} />;').dynamicTokens;
  assert.deepEqual(bare.map((entry) => entry.exprs), [["lineClass"]], "a bare identifier is a composition the registry must answer for");
  assert.equal(Object.hasOwn(DYNAMIC_VALUES, `Nope.tsx:lineClass`), false, "an unregistered bare expression has no values");
  assert.deepEqual(classNameTokens('const a = <b className={className} />;').dynamicTokens, [], "the forwarded className prop renders nothing of its own");

  const conditional = classNameTokens('const a = <b className={phase === "success" ? "is-success" : undefined} />;');
  assert.deepEqual(conditional.staticTokens, [{ line: 1, token: "is-success" }], "a conditional's branch is a class; its comparison operand is not");
  assert.deepEqual(conditional.dynamicTokens, [], "a conditional of literals leaves nothing dynamic");
});

// polyforge#257 AC2: the settings surfaces carry no direct `config-button` base
// class — every button goes through ConfigButton, which adds the variant and
// size classes the stylesheet styles (the base class alone has no padding or
// height). The acceptance names this text itself, so the source check is the
// behaviour.
test("no settings component names the config-button base class directly (AC2)", () => {
  const offenders = [];
  for (const file of owned) {
    const source = readFileSync(file, "utf8");
    for (const match of source.matchAll(/className=(["'])config-button\1/g)) {
      offenders.push(`${relative(componentDir, file)}:${source.slice(0, match.index).split("\n").length}`);
    }
  }
  assert.deepEqual(offenders, [], `direct config-button base class: ${offenders.join(", ")}`);
});
import ts from "typescript";

/** The class expression of a JSX element, composed the way the token case composes it (null: no className). */
function classExpressionOf(node) {
  const attribute = node.attributes.properties.find((candidate) => ts.isJsxAttribute(candidate) && candidate.name.getText() === "className");
  if (!attribute || !attribute.initializer) return null;
  if (ts.isStringLiteral(attribute.initializer)) return { text: attribute.initializer.text, dynamics: [] };
  const expression = ts.isJsxExpression(attribute.initializer) && attribute.initializer.expression
    ? attribute.initializer.expression.getText()
    : attribute.initializer.getText();
  return composeValue(expression);
}

/** Whether a JSX element carries an inline `style` attribute (the legacy editors style their controls this way). */
function hasInlineStyle(node) {
  return node.attributes.properties.some((candidate) => ts.isJsxAttribute(candidate) && candidate.name.getText() === "style");
}

/**
 * Every native form control in a source file, with its own class expression or
 * inline style and the class expressions of the JSX elements enclosing it. The
 * TypeScript parser walks the real tree, so generics (`useState<Row[]>`),
 * comparisons and strings can never be mistaken for tags.
 */
function nativeControls(source, fileName) {
  const file = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const controls = [];
  const visit = (node, ancestors) => {
    if (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) {
      const opening = ts.isJsxElement(node) ? node.openingElement : node;
      const className = classExpressionOf(opening);
      if (ts.isIdentifier(opening.tagName) && NATIVE_CONTROLS.has(opening.tagName.text)) {
        controls.push({
          line: file.getLineAndCharacterOfPosition(opening.getStart(file)).line + 1,
          tag: opening.tagName.text,
          className,
          inlineStyle: hasInlineStyle(opening),
          ancestors,
        });
      }
      const enclosing = className && className.text.trim().length > 0 ? [...ancestors, className] : ancestors;
      if (ts.isJsxElement(node)) for (const child of node.children) visit(child, enclosing);
      return;
    }
    ts.forEachChild(node, (child) => visit(child, ancestors));
  };
  visit(file, []);
  return controls;
}

const NATIVE_CONTROLS = new Set(["input", "select", "textarea", "button"]);

/**
 * Whether a class expression names a class at all (its tokens are the token
 * and dynamic cases' business): a control styled by a composed class
 * (`config-button-${variant}`) still carries one.
 */
const styled = (expression) => expression.text.replaceAll(PLACEHOLDER, "").trim().length > 0;

/** Every selector the stylesheets declare (rule preludes only, comments and strings stripped). */
const SELECTORS = STYLESHEETS.flatMap((css) => stripStylesheet(css).split(/[{}]/))
  .filter((chunk) => chunk.trim().length > 0 && !chunk.trim().startsWith("@"))
  .flatMap((chunk) => chunk.split(","))
  .map((selector) => selector.trim());

/**
 * Whether the stylesheets style a native control through a container class:
 * a selector that holds `.ancestor` and then the element name as a descendant
 * (`.settings-account-form select`, `.harness-apply-to input`). A container's
 * own rule (`.settings-models`) never counts — it says nothing about the
 * control inside it.
 */
function stylesDescendant(ancestorClass, tag) {
  const element = new RegExp(`(?:^|[\\s>~+])${tag}(?=$|[\\s.:#\\[)])`);
  return SELECTORS.some((selector) => selector.includes(`.${ancestorClass}`) && element.test(selector));
}

// polyforge#257 (T2 止血, review r1#1) AC2: owner Q1=B forbids 「无样式的原生
// 控件」. A token check cannot see a control that carries no className, so this
// case reads the markup: every native form control in a settings component is
// styled by its own class (or its own inline `style`, the legacy editors' way),
// or by an enclosing element's class (a label wrapping its input is the usual
// shape). The expected state is the ticket's own rule, not this render's output.
test("every native form control in a settings component is styled by its own class or an ancestor's (AC2)", () => {
  const unstyled = [];
  for (const file of owned) {
    const name = relative(componentDir, file);
    for (const control of nativeControls(readFileSync(file, "utf8"), name)) {
      if (control.className !== null && control.className.text.trim().length > 0) continue;
      if (control.inlineStyle) continue;
      if (control.ancestors.some((ancestor) => [...new Set(ancestor.text.replaceAll(PLACEHOLDER, " ")
        .split(/\s+/).filter(Boolean))]
        .some((token) => stylesDescendant(token, control.tag)))) continue;
      unstyled.push(`${name}:${control.line} <${control.tag}> has no class, no inline style and no styled ancestor`);
    }
  }
  assert.deepEqual(unstyled, [], `settings controls with no CSS rule: ${unstyled.join(", ")}`);
});
