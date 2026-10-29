// Console data shapes — the private /web/api face (no /api/v1 contract,
// no stability promise). Field names are pinned by the plan; the server
// projects them (sanitized HTML, redacted payloads, id-only unreadable
// references).

export interface ProjectRef {
  id: string
  name: string
}

export interface Context {
  user_id: string
  role: string
  admin: boolean
  projects: ProjectRef[]
}

export interface WorkItemRow {
  id: string
  slug: string
  seq: number
  goal: string
  status: string
  priority: string
  created_at: string
}

export interface WorkItemList {
  items: WorkItemRow[]
  next_cursor?: string
}

export interface DepEdge {
  id: string
  kind: string
  slug?: string
  status?: string
}

export interface TimelineEvent {
  created_at: string
  actor: string
  type: string
  payload: Record<string, unknown>
}

export interface WorkItemDetail {
  id: string
  slug: string
  seq: number
  goal: string
  status: string
  priority: string
  milestone?: string
  bundle?: string
  labels: string[]
  reporter: string
  assignee?: string
  created_at: string
  updated_at: string
  closed_at?: string
  html: string
  blocked_by: DepEdge[]
  blocking: DepEdge[]
  events: TimelineEvent[]
}

export interface MemoryRow {
  id: string
  title?: string
  type: string
  visibility: string
  tags: string[]
  updated_at: string
  work_item_id?: string
}

export interface MemoryList {
  items: MemoryRow[]
  next_cursor?: string
  refine_hint: boolean
}

export interface MemoryDetail {
  id: string
  title?: string
  type: string
  visibility: string
  tags: string[]
  updated_at: string
  created_at: string
  work_item_id?: string
  html: string
}

export interface SearchResults {
  work_items: (WorkItemRow & { content?: string })[]
  memories: MemoryRow[]
}

export interface MachineLiveWI {
  id: string
  slug?: string
}

export interface Machine {
  id: string
  operator_id: string
  role: string
  mode: string
  runtime_version: string
  launcher: string
  last_heartbeat_at: string
  heartbeat_age_seconds?: number
  stale: boolean
  superseded: boolean
  live_wis: MachineLiveWI[]
}

export interface MachinesSnapshot {
  stale_after_seconds: number
  machines: Machine[]
}
