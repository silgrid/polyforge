import type { AgentMessage, CustomMessage } from "./types";
import { userMessageKey } from "./prompt-recovery";

/**
 * React keys of the chat's messages (#1184). A message read from the session
 * file is keyed by its entry id. One added during a run (the sent prompt, each
 * `message_end`) has no id until the file is read again, so it is keyed by its
 * place after the last loaded entry: appends and older pages prepended leave
 * that place alone. When the read lands, `carryMessageKeys()` hands those keys
 * to the entry ids now at the same places, so React keeps those messages mounted,
 * their blocks too (MessageView keys a block by its place, not the entry id): a
 * scrolled prompt bubble, open thinking, a tool card and its output's scroll.
 * Still remounted: a message whose key does not pass (from the first mismatch on,
 * or all of a run longer than the first page), and, at the run's end, the process
 * messages moving into Process details (and an answer that followed thinking in
 * its message, whose blocks move up).
 */
const liveKey = (anchor: string | undefined, offset: number) => `live:${anchor ?? ""}:${offset}`;

export function messageRenderKeys(entryIds: readonly string[], count: number, aliases: ReadonlyMap<string, string>): string[] {
  const anchor = entryIds[entryIds.length - 1];
  const keys: string[] = [];
  for (let i = 0; i < count; i++) {
    const id = entryIds[i];
    keys.push(id === undefined ? liveKey(anchor, i - entryIds.length) : aliases.get(id) ?? id);
  }
  return keys;
}

// The file holds pi's own message, except for the prompt, sent with the
// browser's timestamp, and a custom message, stamped when its entry was written.
// Exported for the hook's late-event reconciliation (#153): a message the stream
// emits after a mid-run read must be recognised as the one the read already
// loaded.
export function sameMessage(live: AgentMessage, loaded: AgentMessage): boolean {
  if (live.role !== loaded.role) return false;
  if (live.role === "user") return userMessageKey(live) === userMessageKey(loaded);
  if (live.role === "custom") return live.customType === (loaded as CustomMessage).customType;
  return live.timestamp === loaded.timestamp;
}

/**
 * The aliases (entry id → key) once `next` replaces `previous`: those whose ids
 * are still loaded, plus one per live message of `previous` that the entries
 * after its anchor line up with, up to the first that does not. The rest keep
 * their id keys and remount. Keys never collide: an alias is its entry's place
 * after the anchor on its branch, and a loaded window is one branch, where each
 * place holds one entry (the reader shows an entry as one message at most).
 */
export function carryMessageKeys(
  previous: { messages: readonly AgentMessage[]; entryIds: readonly string[]; aliases: ReadonlyMap<string, string> },
  next: { messages: readonly AgentMessage[]; entryIds: readonly string[] },
): Map<string, string> {
  const loaded = new Set(next.entryIds);
  const aliases = new Map([...previous.aliases].filter(([id]) => loaded.has(id)));
  const liveStart = previous.entryIds.length;
  const anchor = previous.entryIds[liveStart - 1];
  const start = anchor === undefined ? 0 : next.entryIds.indexOf(anchor) + 1;
  // The window starts after the anchor (a run longer than the first page).
  if (start === 0 && anchor !== undefined) return aliases;
  for (let offset = 0; liveStart + offset < previous.messages.length; offset++) {
    const id = next.entryIds[start + offset];
    if (id === undefined || !sameMessage(previous.messages[liveStart + offset], next.messages[start + offset])) break;
    aliases.set(id, liveKey(anchor, offset));
  }
  return aliases;
}
