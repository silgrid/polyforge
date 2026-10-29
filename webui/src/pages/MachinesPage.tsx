import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { MachinesSnapshot, Machine } from '../types'
import NotFound from '../components/NotFound'
import RelativeTime from '../components/RelativeTime'

// MachinesPage is the admin-only health snapshot: one card per machine
// — id, operator, role, mode, runtime, launcher, heartbeat age with
// its colour band (green fresh, yellow stale within an hour, red
// older), the stale and superseded marks, and the live work-item
// links. No public key, no capability, and no refresh: the page is a
// snapshot; nothing updates until the human reloads.
export default function MachinesPage() {
  const [snap, setSnap] = useState<MachinesSnapshot | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    setSnap(null)
    setMissing(false)
    apiGet<MachinesSnapshot>('/web/api/machines')
      .then(setSnap)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) setMissing(true)
      })
  }, [])

  if (missing) return <NotFound />
  if (snap === null) return <p className="text-muted">Loading…</p>

  return (
    <div data-testid="machines" className="max-w-prose">
      <h1 className="text-xl font-semibold mb-1">Machines</h1>
      <p className="text-muted text-sm mb-4">
        Stale after {snap.stale_after_seconds}s without a heartbeat · snapshot, reload to refresh
      </p>
      {snap.machines.length === 0 ? (
        <p className="text-muted">No machines registered.</p>
      ) : (
        <div className="flex flex-col gap-3">
          {snap.machines.map((m) => (
            <MachineCard key={m.id} machine={m} />
          ))}
        </div>
      )}
    </div>
  )
}

// MachineCard renders one machine with its heartbeat band. The band is
// a CSS class, never an inline style: green = not stale; yellow =
// stale within an hour; red = stale for more than an hour.
function MachineCard({ machine }: { machine: Machine }) {
  const band = machine.stale
    ? (machine.heartbeat_age_seconds ?? 0) > 3600
      ? 'border-red text-red'
      : 'border-amber text-amber'
    : 'border-green text-green'
  return (
    <section
      data-testid="machine-card"
      data-machine-id={machine.id}
      className={`border rounded p-3 bg-tint ${band}`}
    >
      <h2 className="text-base font-semibold mb-1 font-mono">
        <span data-testid="machine-band" className={band}>
          ●
        </span>{' '}
        {machine.id}
        {machine.stale ? <span className="ml-2 text-xs border border-line rounded px-1">stale</span> : null}
        {machine.superseded ? (
          <span className="ml-2 text-xs border border-line rounded px-1" data-testid="superseded-mark">
            superseded
          </span>
        ) : null}
      </h2>
      <p className="text-sm text-muted">
        operator {machine.operator_id || '—'} · {machine.role} · mode {machine.mode || '—'} ·{' '}
        {machine.runtime_version || '—'} · launcher {machine.launcher || '—'}
      </p>
      <p className="text-sm text-muted">
        last heartbeat <RelativeTime at={machine.last_heartbeat_at} />
        {machine.heartbeat_age_seconds !== undefined ? ` (${machine.heartbeat_age_seconds}s)` : ''}
      </p>
      {machine.live_wis.length > 0 ? (
        <p className="text-sm mt-1">
          <span className="text-muted">live: </span>
          {machine.live_wis.map((wi) =>
            wi.slug ? (
              <a key={wi.id} className="text-accent mr-2" href={href(slugProject(wi.slug), 'work-items', wi.id)}>
                {wi.slug}
              </a>
            ) : (
              <span key={wi.id} className="text-muted font-mono mr-2">
                {wi.id}
              </span>
            ),
          )}
        </p>
      ) : null}
    </section>
  )
}

// slugProject answers the project name part of a "<project>#<seq>"
// slug — a readable live work item links into ITS project's module.
function slugProject(slug: string): string {
  const cut = slug.lastIndexOf('#')
  return cut > 0 ? slug.slice(0, cut) : ''
}
