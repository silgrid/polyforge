// carries: components/HarnessApplyTo.tsx
// polyforge#259 (T4 AC2): 「应用到」 is one compact chip everywhere — 技能、MCP
// 与模型抽屉共用它。工单「要的结果」定的形态：一个紧凑 chip「应用到：全部 ▾」，
// 展开后是 harness 勾选列表（不支持的置灰并说明原因），**控件本身不负责提交**
// （提交走所在页面的保存模型）。期望值全部来自这句话，不取自实现输出：
//   - chip 的摘要是 `标题：当前模式`，模式词就是「全部」/「指定」；
//   - 展开的列表列出每个目标，禁用的目标带可见原因；
//   - 控件里没有任何提交按钮（没有 <button>，也没有 submit 文本）；
//   - 纯函数仍按工单 85a1b Q6 A：空的指定选择不能提交。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { HarnessApplyTo, applyToTargets, applyToCanSubmit } = await jiti.import("./HarnessApplyTo.tsx");

const h = React.createElement;
const noop = () => {};
const TEXTS = { title: "Apply to", all: "All harnesses", specific: "Specific harnesses" };
const TARGETS = [
  { harness: "pi", label: "pi" },
  { harness: "claude", label: "claude", disabled: true, disabledReason: "claude is not installed" },
];

function render(props = {}) {
  return renderToStaticMarkup(h(HarnessApplyTo, {
    texts: TEXTS,
    targets: TARGETS,
    value: { mode: "all", selected: [] },
    onChange: noop,
    ...props,
  }));
}

test("the target derivation: all, or the specific selection — and empty cannot submit", () => {
  assert.deepEqual(applyToTargets({ mode: "all", selected: [] }, TARGETS), ["pi", "claude"]);
  assert.deepEqual(applyToTargets({ mode: "specific", selected: ["pi"] }, TARGETS), ["pi"]);
  assert.equal(applyToCanSubmit({ mode: "all", selected: [] }, TARGETS), true);
  assert.equal(applyToCanSubmit({ mode: "specific", selected: [] }, TARGETS), false, "an empty specific selection cannot submit");
  assert.equal(applyToCanSubmit({ mode: "specific", selected: ["codex"] }, TARGETS), false, "a selection naming no known target is empty");
});

test("the control is a compact chip whose summary names the current target", () => {
  const all = render();
  // A native <details> chip: its summary is the collapsed control (no state of
  // its own, so it renders and toggles without JavaScript).
  assert.match(all, /<details[^>]*class="harness-apply-to"/, "the control is not a <details> chip");
  assert.match(all, /<summary[^>]*class="harness-apply-to-chip"/, "the chip has no summary");
  const summary = /<summary[^>]*>([\s\S]*?)<\/summary>/.exec(all)?.[1] ?? "";
  assert.match(summary, /Apply to/, "the chip does not name 应用到");
  assert.match(summary, /All harnesses/, "the collapsed chip does not read the current target");

  const specific = render({ value: { mode: "specific", selected: ["pi"] } });
  assert.match(specific, /<details[^>]*data-mode="specific"/, "the chip does not carry the mode");
  const specificSummary = /<summary[^>]*>([\s\S]*?)<\/summary>/.exec(specific)?.[1] ?? "";
  assert.match(specificSummary, /Specific harnesses/, "the chip does not read the specific mode");
});

test("the control never submits: the page's save model does", () => {
  const all = render();
  assert.doesNotMatch(all, /<button/, "the chip renders a button, so it owns an action");
  assert.doesNotMatch(all, /type="submit"/, "the chip submits a form");
  const specific = render({ value: { mode: "specific", selected: ["pi"] } });
  assert.doesNotMatch(specific, /<button/, "the expanded chip renders a button");
});

test("the chip lists every target, and an unsupported one is greyed with its reason in text", () => {
  const all = render();
  assert.match(all, /<input[^>]*type="radio"/, "the mode choice is missing from the chip's menu");
  // Every target is listed whether the chip is open or closed (the details'
  // content is in the markup; CSS shows it while open).
  assert.deepEqual(all.match(/data-harness="[^"]+"/g), ['data-harness="pi"', 'data-harness="claude"']);
  assert.match(all, /claude is not installed/, "the unsupported target gives no reason");
  assert.match(all, /<input[^>]*type="checkbox"[^>]*disabled/, "the unsupported target is not disabled");
  // The reason is visible text, never only a title tooltip.
  assert.doesNotMatch(all, /title="/, "a reason hides in a tooltip");
});

test("the chip shows each target's last result when the page has one", () => {
  const markup = render({ results: [{ harness: "pi", ok: false, text: "pi refused: file is read-only" }] });
  assert.match(markup, /data-harness="pi"[^>]*data-ok="false"/, "the per-target result is missing");
  assert.match(markup, /pi refused: file is read-only/, "the result text is missing");
});

test("an open chip renders open, so a page can show its targets at once", () => {
  const open = render({ open: true });
  assert.match(open, /<details[^>]*\bopen\b/, "the open chip does not render open");
  assert.doesNotMatch(render(), /<details[^>]*\bopen\b/, "the closed chip renders open");
});