import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const argvModule = await jiti.import("./claude-argv.ts");
const { buildClaudeArgv, buildClaudeEnv } = argvModule;

const STREAM = ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text"];
const RETIRED_FLAGS = ["--bare", "--strict-mcp-config", "--disable-slash-commands", "--permission-mode", "--tools", "--allowedTools", "--cwd"];

test("the first launch's exact reduced argv: stream-json, the full model name, no prompts, --session-id", () => {
  assert.deepEqual(buildClaudeArgv({ sessionId: "s-1", resume: false, model: "claude-haiku-5-5" }), [
    ...STREAM,
    "--model", "claude-haiku-5-5",
    "--permission-prompts", "none",
    "--session-id", "s-1",
  ]);
});

test("a restart carries --resume with the same id and model, never --session-id", () => {
  const first = buildClaudeArgv({ sessionId: "abc", resume: false, model: "claude-sonnet-5-5" });
  const again = buildClaudeArgv({ sessionId: "abc", resume: true, model: "claude-sonnet-5-5" });
  assert.deepEqual(again, [...STREAM, "--model", "claude-sonnet-5-5", "--permission-prompts", "none", "--resume", "abc"]);
  assert.ok(!first.includes("--resume"));
  assert.ok(!again.includes("--session-id"));
});

test("no preset, tool or permission-mode flag and no --cwd; exactly one --model with the full name", () => {
  for (const model of ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"]) {
    for (const resume of [false, true]) {
      const argv = buildClaudeArgv({ sessionId: "s", resume, model });
      for (const flag of RETIRED_FLAGS) assert.ok(!argv.some((arg) => arg === flag || arg.startsWith(`${flag}=`)), flag);
      assert.equal(argv.filter((arg) => arg === "--model").length, 1);
      assert.equal(argv[argv.indexOf("--model") + 1], model);
      assert.equal(argv.length, 14);
    }
  }
  for (const name of ["claudePresetTools", "CLAUDE_READ_ONLY_TOOLS", "CLAUDE_WRITABLE_TOOLS", "CLAUDE_WRITABLE_ALLOWED_TOOLS"]) {
    assert.equal(argvModule[name], undefined, name);
  }
});

test("the child env inherits ANTHROPIC_* and CLAUDE_*, drops only PI_WEB_PASSWORD, CLAUDECODE and the server's NODE_ENV, TMUX and TMUX_PANE, injects only the Pi Web watcher marker and leaves the base untouched", () => {
  const base = {
    PATH: "/bin",
    HOME: "/home/u",
    PI_WEB_PASSWORD: "x",
    // The Next server's mode and the tmux Pi Web runs in: a session's
    // `npm ci` would skip devDependencies, it would run inside Pi Web's tmux.
    NODE_ENV: "production",
    TMUX: "/tmp/tmux-0/default,1,0",
    TMUX_PANE: "%1",
    TMUX_TMPDIR: "/tmp/tmux-root",
    ANTHROPIC_API_KEY: "x",
    ANTHROPIC_BASE_URL: "x",
    CLAUDE_CONFIG_DIR: "/elsewhere",
    CLAUDE_CODE_SIMPLE: "1",
    CLAUDECODE: "1",
    PI_WEB_IDLE_TIMEOUT_MS: "5",
    UNSET: undefined,
  };
  const snapshot = { ...base };
  const env = buildClaudeEnv(base);
  assert.deepEqual(env, {
    PATH: "/bin",
    HOME: "/home/u",
    ANTHROPIC_API_KEY: "x",
    ANTHROPIC_BASE_URL: "x",
    CLAUDE_CONFIG_DIR: "/elsewhere",
    CLAUDE_CODE_SIMPLE: "1",
    PI_WEB_IDLE_TIMEOUT_MS: "5",
    TMUX_TMPDIR: "/tmp/tmux-root",
  });
  assert.deepEqual(base, snapshot);
});

test("without an inherited CLAUDE_CONFIG_DIR the child gets none", () => {
  const env = buildClaudeEnv({ PATH: "/bin", HOME: "/home/u", PI_WEB_PASSWORD: "" });
  assert.deepEqual(env, { PATH: "/bin", HOME: "/home/u" });
  assert.ok(!("CLAUDE_CONFIG_DIR" in env));
  assert.ok(!("PI_WEB_PASSWORD" in env), "withheld by name, even when empty");
});

test("the child gets no variable of Pi Web's own: no polyforge watcher marker is set", () => {
  assert.deepEqual(buildClaudeEnv({ PATH: "/bin" }), { PATH: "/bin" });
});

test("the default effort keeps today's argv; a level adds exactly --effort <level> after --model", () => {
  const plain = buildClaudeArgv({ sessionId: "s", resume: false, model: "claude-opus-5-5" });
  assert.deepEqual(buildClaudeArgv({ sessionId: "s", resume: false, model: "claude-opus-5-5", effort: "default" }), plain);
  assert.ok(!plain.includes("--effort"));
  for (const effort of ["low", "medium", "high", "xhigh", "max"]) {
    for (const resume of [false, true]) {
      const argv = buildClaudeArgv({ sessionId: "s", resume, model: "claude-opus-5-5", effort });
      assert.deepEqual(argv.slice(STREAM.length, STREAM.length + 6), ["--model", "claude-opus-5-5", "--effort", effort, "--permission-prompts", "none"]);
      assert.equal(argv.filter((arg) => arg === "--effort").length, 1);
      assert.equal(argv.length, 16);
      for (const flag of RETIRED_FLAGS) assert.ok(!argv.includes(flag), flag);
    }
  }
});

const INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

test("English or absent Agent language keeps the exact pre-feature argv; 中文 adds --append-system-prompt <fixed text> before --permission-prompts on every launch", () => {
  for (const resume of [false, true]) {
    const tail = resume ? ["--resume", "s"] : ["--session-id", "s"];
    const plain = [...STREAM, "--model", "claude-haiku-5-5", "--permission-prompts", "none", ...tail];
    assert.deepEqual(buildClaudeArgv({ sessionId: "s", resume, model: "claude-haiku-5-5" }), plain);
    assert.deepEqual(buildClaudeArgv({ sessionId: "s", resume, model: "claude-haiku-5-5", agentLanguage: "en" }), plain);
    const zh = buildClaudeArgv({ sessionId: "s", resume, model: "claude-haiku-5-5", agentLanguage: "zh-CN" });
    assert.deepEqual(zh, [...STREAM, "--model", "claude-haiku-5-5", "--append-system-prompt", INSTRUCTION, "--permission-prompts", "none", ...tail]);
    const withEffort = buildClaudeArgv({ sessionId: "s", resume, model: "claude-opus-5-5", effort: "high", agentLanguage: "zh-CN" });
    assert.deepEqual(withEffort.slice(STREAM.length, STREAM.length + 8), ["--model", "claude-opus-5-5", "--effort", "high", "--append-system-prompt", INSTRUCTION, "--permission-prompts", "none"]);
    for (const flag of RETIRED_FLAGS) assert.ok(!zh.includes(flag), flag);
    assert.equal(zh.filter((arg) => arg === "--append-system-prompt").length, 1);
  }
});

test("every launch forwards subagent text with --forward-subagent-text", () => {
  // claude 2.1.322 forwards a subagent's text and thinking only with this flag (its tool
  // rounds stream either way); 2.1.312 and 2.1.319 accept it too.
  const launches = [
    { sessionId: "s", resume: false, model: "claude-haiku-5-5" },
    { sessionId: "s", resume: true, model: "claude-haiku-5-5" },
    { sessionId: "s", resume: false, model: "claude-opus-5-5", effort: "high" },
    { sessionId: "s", resume: true, model: "claude-opus-5-5", effort: "max", agentLanguage: "zh-CN" },
    { sessionId: "s", resume: false, model: "claude-haiku-5-5", agentLanguage: "zh-CN" },
    { sessionId: "s", resume: true, model: "claude-haiku-5-5", resumeAt: "t1" },
  ];
  for (const options of launches) {
    const argv = buildClaudeArgv(options);
    assert.equal(argv.filter((arg) => arg === "--forward-subagent-text").length, 1, JSON.stringify(options));
    assert.equal(argv[argv.indexOf("--include-partial-messages") + 1], "--forward-subagent-text", JSON.stringify(options));
  }
});

test("the default permission mode and hook events off keep the exact argv; acceptEdits or plan add --permission-mode and hook events add --include-hook-events, before --permission-prompts", () => {
  for (const resume of [false, true]) {
    const tail = resume ? ["--resume", "s"] : ["--session-id", "s"];
    const base = { sessionId: "s", resume, model: "claude-haiku-5-5" };
    const plain = [...STREAM, "--model", "claude-haiku-5-5", "--permission-prompts", "none", ...tail];
    // default passes no flag: claude's own permissions.defaultMode applies (owner ruling Q2=A).
    for (const extra of [{}, { launchPermissionMode: "default" }, { includeHookEvents: false }, { launchPermissionMode: "default", includeHookEvents: false }]) {
      assert.deepEqual(buildClaudeArgv({ ...base, ...extra }), plain, JSON.stringify(extra));
    }
    for (const mode of ["acceptEdits", "plan"]) {
      assert.deepEqual(buildClaudeArgv({ ...base, launchPermissionMode: mode }), [...STREAM, "--model", "claude-haiku-5-5", "--permission-mode", mode, "--permission-prompts", "none", ...tail]);
    }
    assert.deepEqual(buildClaudeArgv({ ...base, includeHookEvents: true }), [...STREAM, "--model", "claude-haiku-5-5", "--include-hook-events", "--permission-prompts", "none", ...tail]);
    const all = buildClaudeArgv({ ...base, model: "claude-opus-5-5", effort: "high", agentLanguage: "zh-CN", launchPermissionMode: "acceptEdits", includeHookEvents: true });
    assert.deepEqual(all, [
      ...STREAM, "--model", "claude-opus-5-5", "--effort", "high", "--append-system-prompt", INSTRUCTION,
      "--permission-mode", "acceptEdits", "--include-hook-events", "--permission-prompts", "none", ...tail,
    ]);
    assert.equal(all.filter((arg) => arg === "--permission-mode").length, 1);
    assert.equal(all.filter((arg) => arg === "--include-hook-events").length, 1);
  }
  const edit = buildClaudeArgv({ sessionId: "s", resume: true, resumeAt: "t1", model: "claude-haiku-5-5", launchPermissionMode: "acceptEdits", includeHookEvents: true });
  assert.deepEqual(edit.slice(STREAM.length), ["--model", "claude-haiku-5-5", "--permission-mode", "acceptEdits", "--include-hook-events", "--permission-prompts", "none", "--resume", "s", "--resume-session-at", "t1"]);
});

test("the launch permission mode and the approval mode keep their own words: --permission-mode <mode> and --include-hook-events before claude's permission words, ask gives --permission-prompts host --permission-prompt-tool stdio, deny --permission-prompts none ( with)", () => {
  // owner ruling 2026-11-13T07:40:00Z: the argv carries both sets of words.
  const ASK = ["--permission-prompts", "host", "--permission-prompt-tool", "stdio"];
  const DENY = ["--permission-prompts", "none"];
  for (const resume of [false, true]) {
    const tail = resume ? ["--resume", "s"] : ["--session-id", "s"];
    const head = [...STREAM, "--model", "claude-haiku-5-5"];
    const base = { sessionId: "s", resume, model: "claude-haiku-5-5" };
    assert.deepEqual(
      buildClaudeArgv({ ...base, launchPermissionMode: "acceptEdits", includeHookEvents: true, permissionMode: "ask" }),
      [...head, "--permission-mode", "acceptEdits", "--include-hook-events", ...ASK, ...tail],
    );
    assert.deepEqual(
      buildClaudeArgv({ ...base, launchPermissionMode: "plan", permissionMode: "ask" }),
      [...head, "--permission-mode", "plan", ...ASK, ...tail],
    );
    assert.deepEqual(
      buildClaudeArgv({ ...base, launchPermissionMode: "acceptEdits", permissionMode: "deny" }),
      [...head, "--permission-mode", "acceptEdits", ...DENY, ...tail],
    );
    assert.deepEqual(
      buildClaudeArgv({ ...base, launchPermissionMode: "default", permissionMode: "ask" }),
      [...head, ...ASK, ...tail],
    );
  }
});

test("ask routes permission prompts to the host over stdio; deny keeps the exact --permission-prompts none argv", () => {
  const launches = [
    { sessionId: "s", resume: false, model: "claude-haiku-5-5" },
    { sessionId: "s", resume: true, model: "claude-haiku-5-5" },
    { sessionId: "s", resume: false, model: "claude-opus-5-5", effort: "high", agentLanguage: "zh-CN" },
    { sessionId: "s", resume: true, model: "claude-haiku-5-5", resumeAt: "t1" },
  ];
  for (const options of launches) {
    const today = buildClaudeArgv(options);
    const deny = buildClaudeArgv({ ...options, permissionMode: "deny" });
    const ask = buildClaudeArgv({ ...options, permissionMode: "ask" });
    // deny is today's argv byte for byte.
    assert.deepEqual(deny, today, JSON.stringify(options));
    const at = deny.indexOf("--permission-prompts");
    assert.deepEqual(deny.slice(at, at + 2), ["--permission-prompts", "none"]);
    // ask puts the host and the stdio prompt tool exactly where none was; nothing else moves.
    assert.deepEqual(ask, [
      ...deny.slice(0, at),
      "--permission-prompts", "host", "--permission-prompt-tool", "stdio",
      ...deny.slice(at + 2),
    ], JSON.stringify(options));
    assert.equal(ask.filter((arg) => arg === "--permission-prompts").length, 1);
    for (const argv of [ask, deny]) {
      for (const flag of RETIRED_FLAGS) assert.ok(!argv.some((arg) => arg === flag || arg.startsWith(`${flag}=`)), flag);
    }
  }
});
