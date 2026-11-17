import type { MemoryRow } from './types'

// attribution.ts — the console's one attribution formatter (polyforge#120,
// AC2): the author's display name and the writer on the memory rows and the
// detail header. A missing value renders its explicit unknown placeholder
// (the owner's Q3 ruling: the user can tell "no data" from "no
// implementation") — never the current user, never an id, never a blank.

// authorName answers the row's author display name, or the explicit
// placeholder when the server names none (a pre-name row).
export function authorName(memory: Pick<MemoryRow, 'owner_display_name'>): string {
  return memory.owner_display_name || 'unknown author'
}

// WRITER_DETAIL_ORDER is the fixed order the writer string shows the valued
// known details in — the daemon's two stamps (session: machine/cell; step:
// work_item/step/attempt/model).
const WRITER_DETAIL_ORDER = ['machine', 'cell', 'work_item', 'step', 'attempt', 'model']

// writerLabel answers the writer attribution: the kind plus only the valued
// known details (`step (work_item=wi_9, step=build, attempt=1, model=…)`), a
// kind with no details alone, and the explicit placeholder when the row
// carries no writer (a pre-writer row) — details are never invented.
export function writerLabel(memory: Pick<MemoryRow, 'writer_kind' | 'writer_details'>): string {
  const kind = memory.writer_kind ?? ''
  if (!kind) return 'unknown writer'
  const parts: string[] = []
  const details = (memory.writer_details ?? {}) as Record<string, unknown>
  for (const key of WRITER_DETAIL_ORDER) {
    const value = details[key]
    if (typeof value === 'number' && Number.isFinite(value)) parts.push(`${key}=${value}`)
    else if (typeof value === 'string' && value.trim()) parts.push(`${key}=${value.trim()}`)
  }
  return parts.length ? `${kind} (${parts.join(', ')})` : kind
}

// memoryAttribution answers the one attribution line the rows and the detail
// header share: `author · writer`.
export function memoryAttribution(memory: Pick<MemoryRow, 'owner_display_name' | 'writer_kind' | 'writer_details'>): string {
  return `${authorName(memory)} · ${writerLabel(memory)}`
}
