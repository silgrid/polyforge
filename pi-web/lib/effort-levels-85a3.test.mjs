// carries: lib/harness/effort-levels.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// 85a3-1 AC3: the unified reasoning-effort mapping. Expected values come from
// the ticket's settled text, not from the implementation: the unified five are
// low, medium, high, xhigh, max; pi's off and minimal are pi's own (单列);
// claude's ultracode (its /effort command's mode, the 2.1.322 initialize
// recording) is claude's own (单列); claude's "default" means unset (未设置),
// never a level. The global default is one write that every harness spelled
// in the unified five reads (一写多读); a pi-specific level never reaches
// claude's side.
const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const effort = await jiti.import("./harness/effort-levels.ts");

test("the unified five levels are low, medium, high, xhigh, max, in order", () => {
  assert.deepEqual(effort.UNIFIED_EFFORT_LEVELS, ["low", "medium", "high", "xhigh", "max"]);
  for (const level of ["low", "medium", "high", "xhigh", "max"]) {
    assert.equal(effort.isUnifiedEffort(level), true, level);
  }
  assert.equal(effort.isUnifiedEffort("off"), false);
  assert.equal(effort.isUnifiedEffort("minimal"), false);
  assert.equal(effort.isUnifiedEffort("ultracode"), false);
  assert.equal(effort.isUnifiedEffort("default"), false);
  assert.equal(effort.isUnifiedEffort("auto"), false);
  assert.equal(effort.isUnifiedEffort(7), false);
});

test("pi's off and minimal are pi's own (单列), never unified", () => {
  assert.deepEqual(effort.PI_SPECIFIC_EFFORTS, ["off", "minimal"]);
  assert.equal(effort.isPiSpecificEffort("off"), true);
  assert.equal(effort.isPiSpecificEffort("minimal"), true);
  assert.equal(effort.isPiSpecificEffort("low"), false);
  assert.equal(effort.isPiSpecificEffort("ultracode"), false);
  // onto the unified five: pi's own spellings answer null, the caller keeps
  // the raw value for display
  assert.equal(effort.unifiedFromPiEffort("off"), null);
  assert.equal(effort.unifiedFromPiEffort("minimal"), null);
  assert.equal(effort.unifiedFromPiEffort("nonsense"), null);
});

test("claude's ultracode is claude's own (单列) and its default means unset (未设置)", () => {
  assert.deepEqual(effort.CLAUDE_SPECIFIC_EFFORTS, ["ultracode"]);
  assert.equal(effort.isClaudeSpecificEffort("ultracode"), true);
  assert.equal(effort.isClaudeSpecificEffort("max"), false);
  assert.equal(effort.isClaudeSpecificEffort("off"), false);
  // claude's unset spelling is a state, not a level
  assert.equal(effort.isClaudeEffortUnset("default"), true);
  assert.equal(effort.isClaudeEffortUnset("low"), false);
  assert.equal(effort.isClaudeEffortUnset("ultracode"), false);
  // onto the unified five: claude's own value and the unset spelling answer null
  assert.equal(effort.unifiedFromClaudeEffort("ultracode"), null);
  assert.equal(effort.unifiedFromClaudeEffort("default"), null);
  assert.equal(effort.unifiedFromClaudeEffort("nonsense"), null);
});

test("the unified five map onto both harnesses' spellings both ways", () => {
  for (const level of ["low", "medium", "high", "xhigh", "max"]) {
    assert.equal(effort.unifiedFromPiEffort(level), level);
    assert.equal(effort.unifiedFromClaudeEffort(level), level);
    assert.equal(effort.piEffortFromUnified(level), level);
    assert.equal(effort.claudeEffortFromUnified(level), level);
  }
});

test("unifiedGlobalEffortWrite: one write reaches both natives; a pi-specific level stays pi's alone", () => {
  for (const level of ["low", "medium", "high", "xhigh", "max"]) {
    assert.deepEqual(effort.unifiedGlobalEffortWrite(level), { unified: level, pi: level, claude: level });
  }
  // pi's own levels (off, minimal) never reach claude's side (单列)
  assert.deepEqual(effort.unifiedGlobalEffortWrite("off"), { unified: null, pi: "off", claude: null });
  assert.deepEqual(effort.unifiedGlobalEffortWrite("minimal"), { unified: null, pi: "minimal", claude: null });
});
