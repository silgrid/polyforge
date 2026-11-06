import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const settingsCssSource = await readFile(new URL("../app/settings.css", import.meta.url), "utf8");
const chatWindowSource = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
const chatInputSource = await readFile(new URL("./ChatInput.tsx", import.meta.url), "utf8");
const extensionStatusBarSource = await readFile(new URL("./ExtensionStatusBar.tsx", import.meta.url), "utf8");
const mcpConfigSource = await readFile(new URL("./McpConfig.tsx", import.meta.url), "utf8");
const mcpSignInSource = await readFile(new URL("./McpSignIn.tsx", import.meta.url), "utf8");
const mcpAddSource = await readFile(new URL("./McpAddServer.tsx", import.meta.url), "utf8");

test("every class the keyboard-open rules target exists on its component", () => {
  // Every selector the keyboard rules target must exist on the element it
  // means; a renamed class would otherwise leave the rule silently dead.
  assert.match(chatInputSource, /className="chat-input-controls"/);
  assert.match(chatInputSource, /className=\{compact \? undefined : "chat-input-shell"\}/);
  assert.match(extensionStatusBarSource, /className=\{`extension-status-shelf/);
  assert.match(chatWindowSource, /className="chat-content /);
});

test("every .mcp-* rule in settings.css matches a class the MCP panel renders, and back", () => {
  // Every .mcp-* rule targets a class the panel (its Sign-in row, its add pane) renders, and every
  // class it renders has a rule, so a rename cannot leave a phone rule silently dead.
  const styled = new Set([...settingsCssSource.matchAll(/\.(mcp-[a-z-]+)/g)].map((match) => match[1]));
  const rendered = new Set([...`${mcpConfigSource}\n${mcpSignInSource}\n${mcpAddSource}`.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\})/g)]
    .flatMap((match) => (match[1] ?? match[2]).split(/\s+/))
    .filter((name) => name.startsWith("mcp-")));
  assert.deepEqual([...styled].sort(), [...rendered].sort());
});
