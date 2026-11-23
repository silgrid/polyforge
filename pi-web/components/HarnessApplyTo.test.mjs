// carries: components/HarnessApplyTo.tsx
// polyforge#149 (#85 85a1b, AC3, owner ruling Q6 A): the 「应用到：全部 /
// 指定」 shared component — choosing all or specific harnesses changes only
// the target (never a save by itself), an empty specific selection cannot
// submit, the parent owns the selection and answers per target so a failure
// keeps the draft and is never reported as a total success. Presentational
// like SettingsUi.tsx: every word a prop, rendered without an i18n provider.
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
const { ConfigButton } = await jiti.import("./SettingsUi.tsx");

const h = React.createElement;
const noop = () => {};
const TEXTS = { title: "Apply to", all: "All harnesses", specific: "Specific harnesses", submit: "Apply" };
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
    onSubmit: noop,
    ...props,
  }));
}

test("the target derivation: all, or the specific selection — and empty cannot submit", () => {
  assert.deepEqual(applyToTargets({ mode: "all", selected: [] }, TARGETS), ["pi", "claude"]);
  assert.deepEqual(applyToTargets({ mode: "specific", selected: ["pi"] }, TARGETS), ["pi"]);
  assert.deepEqual(applyToTargets({ mode: "specific", selected: ["claude"] }, TARGETS), ["claude"]);
  assert.equal(applyToCanSubmit({ mode: "all", selected: [] }, TARGETS), true);
  assert.equal(applyToCanSubmit({ mode: "specific", selected: [] }, TARGETS), false, "an empty specific selection can submit");
  assert.equal(applyToCanSubmit({ mode: "specific", selected: ["pi"] }, TARGETS), true);
  // A selection naming no known target is empty for the submit's sake.
  assert.equal(applyToCanSubmit({ mode: "specific", selected: ["codex"] }, TARGETS), false);
});

test("the apply-to control switches between all and specific harnesses", () => {
  const all = render();
  assert.match(all, /All harnesses/);
  assert.match(all, /Specific harnesses/);
  // The mode is a radio pair on the parent-owned value.
  assert.match(all, /role="radiogroup"/);
  assert.match(all, /Apply/, "the submit control is missing");

  const specific = render({ value: { mode: "specific", selected: ["pi"] } });
  // The specific mode lists the harnesses, the disabled one with its visible reason.
  assert.match(specific, /data-harness="pi"/);
  assert.ok(specific.includes("claude is not installed"), "the disabled target's reason is not visible text");

  // An empty specific selection cannot submit: the button is disabled, the
  // parent's submit never fires (the wiring is the thin onClick prop).
  const empty = render({ value: { mode: "specific", selected: [] } });
  assert.match(empty, /disabled/, "an empty target selection can submit");

  // A busy round disables the control while the one submission runs.
  const busy = render({ value: { mode: "all", selected: [] }, busy: true });
  assert.match(busy, /disabled/, "the control stays enabled through a submission");
});

test("the parent's per-target feedback renders as rows, failures kept as drafts", () => {
  const withResults = render({
    value: { mode: "specific", selected: ["pi", "claude"] },
    results: [
      { harness: "pi", ok: true, text: "saved" },
      { harness: "claude", ok: false, text: "refused: version not known" },
    ],
  });
  assert.ok(withResults.includes("saved"), "a successful target's feedback is missing");
  assert.ok(withResults.includes("refused: version not known"), "the failed target's own reason is missing");
  // The draft (the selection) is still shown: a failure never clears it here.
  assert.match(withResults, /data-harness="pi"/);
});

/** Every element of a rendered tree, in order (React elements are plain objects). */
function collect(node, out = []) {
  if (Array.isArray(node)) {
    for (const child of node) collect(child, out);
  } else if (node && typeof node === "object" && typeof node.type !== "undefined" && node.props) {
    out.push(node);
    collect(node.props.children, out);
  }
  return out;
}

function textOf(node) {
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (node === null || node === undefined || typeof node !== "object") return typeof node === "string" ? node : "";
  return textOf(node.props?.children);
}

/** The input control whose enclosing label's text names `label`. */
function inputByLabel(root, label) {
  return collect(root).find((element) => element.type === "input"
    && collect(root).some((candidate) => candidate.type === "label"
      && textOf(candidate.props.children).includes(label)
      && collect(candidate).includes(element)));
}

test("choosing all or specific harnesses changes only the target — never a save (r1#1)", () => {
  const changes = [];
  let submits = 0;
  const element = HarnessApplyTo({
    texts: TEXTS,
    targets: TARGETS,
    value: { mode: "all", selected: [] },
    onChange: (next) => changes.push(next),
    onSubmit: () => { submits += 1; },
  });
  // The specific radio: a mode choice re-targets and never submits.
  const specific = inputByLabel(element, "Specific harnesses");
  assert.ok(specific, "the specific radio is not rendered");
  specific.props.onChange();
  assert.deepEqual(changes, [{ mode: "specific", selected: [] }], "the mode choice changed more than the target");
  assert.equal(submits, 0, "a mode choice submitted");
  // The all radio from the specific mode: the same, backwards.
  const back = HarnessApplyTo({
    texts: TEXTS,
    targets: TARGETS,
    value: { mode: "specific", selected: ["pi"] },
    onChange: (next) => changes.push(next),
    onSubmit: () => { submits += 1; },
  });
  inputByLabel(back, "All harnesses").props.onChange();
  assert.deepEqual(changes[1], { mode: "all", selected: ["pi"] });
  assert.equal(submits, 0, "a mode choice submitted");
  // A checkbox: only its harness joins or leaves the selection.
  const checking = HarnessApplyTo({
    texts: TEXTS,
    targets: TARGETS,
    value: { mode: "specific", selected: [] },
    onChange: (next) => changes.push(next),
    onSubmit: () => { submits += 1; },
  });
  inputByLabel(checking, "pi").props.onChange({ target: { checked: true } });
  assert.deepEqual(changes[2], { mode: "specific", selected: ["pi"] }, "the checkbox changed more than its own harness");
  assert.equal(submits, 0, "a target choice submitted");
  const unchecking = HarnessApplyTo({
    texts: TEXTS,
    targets: TARGETS,
    value: { mode: "specific", selected: ["pi"] },
    onChange: (next) => changes.push(next),
    onSubmit: () => { submits += 1; },
  });
  inputByLabel(unchecking, "pi").props.onChange({ target: { checked: false } });
  assert.deepEqual(changes[3], { mode: "specific", selected: [] });
  assert.equal(submits, 0, "a target choice submitted");
});

test("an empty specific selection cannot submit: the submit button itself is disabled (r1#1)", () => {
  // A target set with no disabled item, so nothing but the empty selection can
  // hold the button back — the assertion lands on the button element itself
  // (polyforge#257: the submit is the shared ConfigButton, so the element
  // found is that component; its own `disabled` prop is what reaches the
  // native button, asserted rendered in components/settings-buttons.test.mjs).
  const clean = [{ harness: "pi", label: "pi" }, { harness: "claude", label: "claude" }];
  const empty = HarnessApplyTo({ texts: TEXTS, targets: clean, value: { mode: "specific", selected: [] }, onChange: noop, onSubmit: noop });
  const submit = collect(empty).find((element) => element.type === ConfigButton && textOf(element.props.children) === "Apply");
  assert.ok(submit, "the submit button is not rendered");
  assert.equal(submit.props.disabled, true, "an empty target selection can submit");
  const named = HarnessApplyTo({ texts: TEXTS, targets: clean, value: { mode: "specific", selected: ["pi"] }, onChange: noop, onSubmit: noop });
  const submitNamed = collect(named).find((element) => element.type === ConfigButton && textOf(element.props.children) === "Apply");
  assert.notEqual(submitNamed.props.disabled, true, "a named target cannot submit");
});

test("presentational: every word a prop, no i18n provider needed", () => {
  assert.match(render(), /Apply to/);
});
