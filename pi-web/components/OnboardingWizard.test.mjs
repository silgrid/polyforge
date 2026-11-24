// carries: components/OnboardingWizard*, components/PolyforgeFileTicketDialog*, lib/onboarding-state.ts
// polyforge#167 (91-onboarding) AC1/AC2: the wizard's rendered view is a pure
// function of the server state — four steps in the ticket's order, a skipped
// step shown 「未完成」, the AIHub step's first-boot connection result, the
// account deep link, and the first ticket in the develop lane. Expected
// strings come from the ticket and the English locale table, never from the
// component's own output.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { OnboardingWizard } = await jiti.import("./OnboardingWizard.tsx");
const { PolyforgeFileTicketDialog } = await jiti.import("./PolyforgeFileTicketDialog.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};

const view = (overrides = {}) => ({
  version: 1,
  steps: { aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" },
  nextStep: "aihub",
  dismissed: false,
  connection: null,
  ...overrides,
});

function render(overrides = {}) {
  const props = { view: view(overrides.view), cwd: overrides.cwd ?? null, onComplete: noop, onSkip: noop, onClose: noop, onOpenAccount: noop, onWorkspaceChange: noop };
  return renderToStaticMarkup(h(I18nProvider, null, h(OnboardingWizard, props)));
}

const stepsOf = (html) => [...html.matchAll(/data-onboarding-step="([^"]+)" data-onboarding-status="([^"]+)"/g)].map((match) => [match[1], match[2]]);
const statusTextsOf = (html) => [...html.matchAll(/data-onboarding-status-text="([^"]+)"[^>]*>([^<]*)</g)].map((match) => [match[1], match[2]]);

test("the first open shows the four steps in the ticket's order, all 「未完成」, on the AIHub step", () => {
  const html = render();
  assert.deepEqual(stepsOf(html), [
    ["aihub", "incomplete"],
    ["account", "incomplete"],
    ["workspace", "incomplete"],
    ["ticket", "incomplete"],
  ], "the step order or the unfinished state is wrong");
  assert.deepEqual(statusTextsOf(html), [
    ["incomplete", "Not done"],
    ["incomplete", "Not done"],
    ["incomplete", "Not done"],
    ["incomplete", "Not done"],
  ]);
  assert.match(html, /data-onboarding-current="aihub"/);
  assert.match(html, />Step 1 of 4</);
  assert.match(html, /data-onboarding-step-panel="aihub"/, "the first open does not start on AIHub");
  assert.match(html, /No first-boot record yet/, "a missing first-boot record is not said");
  assert.match(html, /data-onboarding-close/, "the wizard offers no close control");
});

test("a skipped step stays 「未完成」 while a completed one is marked complete, and the next step is the first unfinished", () => {
  const html = render({
    view: view({
      steps: { aihub: "complete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" },
      nextStep: "account",
    }),
  });
  assert.deepEqual(statusTextsOf(html), [
    ["complete", "Complete"],
    ["incomplete", "Not done"],
    ["incomplete", "Not done"],
    ["incomplete", "Not done"],
  ], "a skipped step must read 「未完成」, not complete");
  assert.match(html, /data-onboarding-current="account"/, "the wizard does not start at the first unfinished step");
  assert.match(html, /data-onboarding-step-panel="account"/);
  assert.match(html, /data-onboarding-account-link/, "the account step offers no deep link");
  assert.match(html, /data-onboarding-account-check/, "the account step cannot read the login status");
  assert.match(html, /Open Settings › Account/);
});

test("the AIHub step shows the first-boot connection result, and the team mode offers pf cell add with the key as a password", () => {
  const html = render({
    view: view({
      connection: {
        phase: "done",
        mode: "external",
        defaultCell: "default",
        server: "https://aihub.example.test:8420",
        project: "polyforge",
        userReady: true,
        keyReady: true,
      },
    }),
  });
  assert.match(html, /Team AIHub/);
  assert.match(html, /data-fact="phase">done</);
  assert.match(html, /data-fact="defaultCell">default</);
  assert.match(html, /data-fact="server">https:\/\/aihub\.example\.test:8420</);
  assert.match(html, /data-fact="userReady">ready</);
  assert.match(html, /data-fact="keyReady">ready</);
  assert.match(html, /data-onboarding-team-form/, "the team mode offers no Cell form");
  assert.match(html, /type="password" autoComplete="off"/, "the API key input is not a password field");
  assert.doesNotMatch(html, /type="password"[^>]*value="[^"]+"/, "the API key value must never be rendered back");
});

test("the local mode shows the machine's own Cell and continues without the team form", () => {
  const html = render({
    view: view({
      connection: { phase: "done", mode: "local", defaultCell: "default", server: null, project: null, userReady: true, keyReady: true },
    }),
  });
  assert.match(html, /This machine[\s\S]{0,8}s local AIHub/);
  assert.match(html, /data-onboarding-primary="aihub"/);
  assert.doesNotMatch(html, /data-onboarding-team-form/);
  assert.doesNotMatch(html, /type="password"/, "the local mode asks for no key");
});

test("the workspace step shows the current workspace and the existing selection path", () => {
  const html = render({
    view: view({ steps: { aihub: "complete", account: "complete", workspace: "incomplete", ticket: "incomplete" }, nextStep: "workspace" }),
    cwd: "/workspace/project",
  });
  assert.match(html, /data-onboarding-step-panel="workspace"/);
  assert.match(html, /Current workspace[\s\S]*?<code>\/workspace\/project<\/code>/, "the current workspace is not shown");
  assert.match(html, /data-onboarding-workspace-use/, "the workspace step offers no selection");
});

test("the first-ticket step offers the dialog, and the dialog renders the develop lane for a message-less ticket", () => {
  const html = render({
    view: view({ steps: { aihub: "complete", account: "complete", workspace: "complete", ticket: "incomplete" }, nextStep: "ticket" }),
  });
  assert.match(html, /data-onboarding-step-panel="ticket"/);
  assert.match(html, /data-onboarding-ticket-open/, "the first-ticket step offers no dialog");
  assert.match(html, /develop lane/);

  const dialog = renderToStaticMarkup(h(I18nProvider, null, h(PolyforgeFileTicketDialog, {
    projects: ["polyforge", "other"],
    lane: "develop",
    onClose: noop,
    onCreated: noop,
  })));
  assert.match(dialog, /data-lane="develop"/, "the wizard's ticket dialog does not carry the develop lane");
  assert.match(dialog, /<input[^>]*value=""\/>/, "the message-less dialog does not start with a blank goal");
  assert.match(dialog, /<option value="polyforge" selected="">polyforge<\/option>/, "the dialog does not list the mapped projects with the first one defaulted");
});

test("the message-backed prefill is unchanged when a source message is given", () => {
  const dialog = renderToStaticMarkup(h(I18nProvider, null, h(PolyforgeFileTicketDialog, {
    analysis: { summary: "Do the thing.", project: "other" },
    message: { id: "msg_1", text: "please" },
    projects: ["polyforge", "other"],
    onClose: noop,
    onCreated: noop,
  })));
  assert.match(dialog, /data-lane="none"/, "the Messages path must carry no lane");
  assert.match(dialog, /<option value="other" selected="">/, "the prefill does not follow the analysis project");
  const noAnalysisProject = renderToStaticMarkup(h(I18nProvider, null, h(PolyforgeFileTicketDialog, { analysis: { summary: "S.", project: "gone" }, message: { id: "m", text: "t" }, projects: ["polyforge", "other"], onClose: noop, onCreated: noop })));
  assert.match(noAnalysisProject, /<option value="polyforge" selected="">/, "an unmapped project does not fall back to the first mapped one");
  assert.match(dialog, /消息 msg_1: &quot;please&quot;/, "the source message reference is not prefilled");
  assert.match(dialog, /Do the thing\./);
});
