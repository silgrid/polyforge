import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { parseTerminalTheme, TERMINAL_XTERM_THEMES } from "./terminal-theme.ts";

test("a missing or unknown stored terminal theme falls back to dark", () => {
  assert.equal(parseTerminalTheme("light"), "light");
  assert.equal(parseTerminalTheme("dark"), "dark");
  assert.equal(parseTerminalTheme(null), "dark");
  assert.equal(parseTerminalTheme("pine"), "dark");
});

test("both terminal palettes define every ANSI color", () => {
  const names = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"];
  for (const theme of Object.values(TERMINAL_XTERM_THEMES)) {
    for (const name of names) {
      assert.match(theme[name], /^#[0-9a-f]{6}$/);
      assert.match(theme[`bright${name[0].toUpperCase()}${name.slice(1)}`], /^#[0-9a-f]{6}$/);
    }
  }
});
