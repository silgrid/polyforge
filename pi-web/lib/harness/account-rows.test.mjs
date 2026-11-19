// carries: lib/harness/account-rows.ts
// polyforge#142 (85a2, AC1/AC7): the Cell-row model the account page renders
// — built purely from the doctor cells facts, the adapter capabilities and
// the per-source pi checks, with the field list of the ticket (状态、来源含
// 「共享 default」、配置根、凭据是否跟随配置目录、步骤能否使用及原因) as the
// independent expectation, and the keychain hint (AC7) when a harness's
// credentials do not follow its config root.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { buildAccountCellRow } = await jiti.import("./account-rows.ts");

const CAPS = {
  pi: { id: "pi", credentialsFollowConfigRoot: true },
  claude: { id: "claude", credentialsFollowConfigRoot: true },
};
const cell = (over: Record<string, unknown>) => ({
  profile: "default",
  harnesses: {
    pi: { configRoot: "/h/default/step-agent", credential: "set", source: "/home/op/.pi/agent", sourceKind: "operator" },
    claude: {
      configRoot: "/h/default/claude-config",
      source: "/home/op/.pi/agent", sourceKind: "operator",
      settingsEnv: { ANTHROPIC_AUTH_TOKEN: "set", ANTHROPIC_API_KEY: "unset", ANTHROPIC_BASE_URL: "unset", CLAUDE_CONFIG_DIR: "unset" },
      credentialsFile: "absent",
    },
  },
  ...over,
});

test("a Cell row carries every field of the ticket's list, per harness", () => {
  const row = buildAccountCellRow(cell({}), CAPS, new Map());
  assert.equal(row.profile, "default");
  assert.equal(row.executionDisabled, null);
  assert.equal(row.probeError, null);
  assert.deepEqual(row.harnessOrder, ["pi", "claude"]);
  const pi = row.harnesses.pi;
  assert.deepEqual(pi, {
    id: "pi", configRoot: "/h/default/step-agent", credential: "set",
    status: "unknown", authType: null, checkReason: null,
    source: "/home/op/.pi/agent", sourceKind: "operator",
    credentialsFollow: true, keychainHint: false,
    credentialKinds: null, credentialsFile: null,
    usable: true, reason: null,
  });
  const claude = row.harnesses.claude;
  assert.deepEqual(claude.credentialKinds, { oauthToken: false, apiKey: false, gateway: false });
  assert.equal(claude.credential, "set");
  assert.equal(claude.credentialsFile, "absent");
  assert.equal(claude.usable, true);
  assert.equal(claude.reason, null);

  // the gateway pair (BASE_URL + AUTH_TOKEN) is its own kind (Q3)
  const gateway = buildAccountCellRow(cell({
    harnesses: {
      pi: { configRoot: "/h/x/step-agent", credential: "set", source: "/s", sourceKind: "directory" },
      claude: { configRoot: "/h/x/claude-config", source: "/s", sourceKind: "directory",
        settingsEnv: { ANTHROPIC_AUTH_TOKEN: "set", ANTHROPIC_BASE_URL: "set", ANTHROPIC_API_KEY: "unset", CLAUDE_CONFIG_DIR: "unset" },
        credentialsFile: "absent" },
    },
  }), CAPS, new Map());
  assert.deepEqual(gateway.harnesses.claude.credentialKinds, { oauthToken: false, apiKey: false, gateway: true });
  // a residual OAuth login keeps the step usable (the daemon still uses it)
  const residual = buildAccountCellRow(cell({
    harnesses: {
      pi: { configRoot: "/h/y/step-agent", credential: "unset", source: "/s", sourceKind: "directory" },
      claude: { configRoot: "/h/y/claude-config", source: "/s", sourceKind: "directory",
        settingsEnv: { ANTHROPIC_AUTH_TOKEN: "unset", ANTHROPIC_BASE_URL: "unset", ANTHROPIC_API_KEY: "unset", CLAUDE_CONFIG_DIR: "unset" },
        credentialsFile: "present" },
    },
  }), CAPS, new Map());
  assert.equal(residual.harnesses.claude.credential, "unset");
  assert.equal(residual.harnesses.claude.usable, true);
  assert.ok(residual.harnesses.claude.reason === null || !residual.harnesses.claude.reason.includes("no credential"));
});

test("the source kinds mark shared-default and independent directories (含「共享 default」)", () => {
  const shared = buildAccountCellRow(cell({
    profile: "beta",
    harnesses: {
      pi: { configRoot: "/h/beta/step-agent", credential: "unset", source: "/home/op/.pi/agent", sourceKind: "shared-default" },
      claude: { configRoot: "/h/beta/claude-config", sourceKind: "shared-default", settingsEnv: {}, credentialsFile: "absent" },
    },
  }), CAPS, new Map());
  assert.equal(shared.harnesses.pi.sourceKind, "shared-default");
  assert.equal(shared.harnesses.claude.sourceKind, "shared-default");
  // unset credentials with no residual login: the step cannot use claude, with the reason
  assert.equal(shared.harnesses.claude.usable, false);
  assert.ok(shared.harnesses.claude.reason && shared.harnesses.claude.reason.includes("no credential"), shared.harnesses.claude.reason);

  const own = buildAccountCellRow(cell({
    profile: "gamma",
    harnesses: {
      pi: { configRoot: "/h/gamma/step-agent", credential: "set", source: "/srv/own", sourceKind: "directory" },
      claude: { configRoot: "/h/gamma/claude-config", source: "/srv/own", sourceKind: "directory", settingsEnv: { CLAUDE_CODE_OAUTH_TOKEN: "set", ANTHROPIC_API_KEY: "unset", ANTHROPIC_AUTH_TOKEN: "unset", ANTHROPIC_BASE_URL: "unset" }, credentialsFile: "absent" },
    },
  }), CAPS, new Map());
  assert.equal(own.harnesses.pi.source, "/srv/own");
  assert.equal(own.harnesses.claude.sourceKind, "directory");
  assert.deepEqual(own.harnesses.claude.credentialKinds, { oauthToken: true, apiKey: false, gateway: false });
});

test("a harness whose credentials do not follow the config root carries the keychain hint (AC7)", () => {
  const caps = { ...CAPS, claude: { id: "claude", credentialsFollowConfigRoot: false } };
  const row = buildAccountCellRow(cell({}), caps, new Map());
  assert.equal(row.harnesses.claude.keychainHint, true);
  assert.equal(row.harnesses.claude.credentialsFollow, false);
  assert.equal(row.harnesses.pi.keychainHint, false);
});

test("an execution-disabled Cell and a probe error are carried whole, never faked", () => {
  const disabled = buildAccountCellRow(cell({ executionDisabled: "no workspace configured — add a mapping" }), CAPS, new Map());
  assert.equal(disabled.executionDisabled, "no workspace configured — add a mapping");
  assert.equal(disabled.harnesses.pi.usable, false);
  assert.equal(disabled.harnesses.claude.usable, false);
  assert.ok(disabled.harnesses.pi.reason && disabled.harnesses.pi.reason.includes("no workspace configured"));

  const broken = buildAccountCellRow({ profile: "delta", probeError: "the daemon does not answer" }, CAPS, new Map());
  assert.equal(broken.probeError, "the daemon does not answer");
  assert.equal(broken.harnesses.pi.configRoot, null);
  assert.equal(broken.harnesses.pi.usable, false);
  assert.equal(broken.harnesses.claude.status, "unknown");
});

test("the per-source pi check feeds the row: ready with a type, not_ready with the reason", () => {
  const checks = new Map([
    ["/home/op/.pi/agent", { sourceDir: "/home/op/.pi/agent", status: "ready", authType: "api_key", reason: null }],
  ]);
  const row = buildAccountCellRow(cell({}), CAPS, checks);
  assert.deepEqual(row.harnesses.pi, {
    ...row.harnesses.pi,
    status: "ready", authType: "api_key", checkReason: null,
  });
  const failed = new Map([
    ["/home/op/.pi/agent", { sourceDir: "/home/op/.pi/agent", status: "not_ready", authType: null, reason: "credentials_not_configured" }],
  ]);
  const row2 = buildAccountCellRow(cell({ profile: "beta" }), CAPS, failed);
  assert.equal(row2.harnesses.pi.status, "not_ready");
  assert.equal(row2.harnesses.pi.checkReason, "credentials_not_configured");
  assert.equal(row2.harnesses.pi.usable, false);
  assert.ok(row2.harnesses.pi.reason && row2.harnesses.pi.reason.includes("credentials_not_configured"));
});
