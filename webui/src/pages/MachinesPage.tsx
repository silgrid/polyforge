import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { MachinesSnapshot, Machine } from '../types'
import NotFound from '../components/NotFound'
import RelativeTime from '../components/RelativeTime'
import { BandIcon, ClockIcon } from '../icons'
import { EmptyState, Muted, PageHeader } from '../components/ui'

// MachinesPage is the admin-only health snapshot: one card per machine
// — id, operator, role, mode, runtime, launcher, heartbeat age with
// its colour band (green fresh, amber stale within an hour, red
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
  if (snap === null) return <Muted>Loading…</Muted>

  return (
    <div data-testid="machines" className="max-w-3xl">
      <PageHeader
        title="Machines"
        caption={`Stale after ${snap.stale_after_seconds}s without a heartbeat · snapshot, reload to refresh`}
      />
      {snap.machines.length === 0 ? (
        <EmptyState>No machines registered.</EmptyState>
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

// MachineCard renders one machine card with its heartbeat band. The
// band is a CSS class, never an inline style: green = not stale; amber
// = stale within an hour; red = stale for more than an hour.
function MachineCard({ machine }: { machine: Machine }) {
  const band = machine.stale
    ? (machine.heartbeat_age_seconds ?? 0) > 3600
      ? 'band-red'
      : 'band-amber'
    : 'band-green'
  return (
    <section data-testid="machine-card" data-machine-id={machine.id} className="card px-4 py-3">
      <h2 className="mb-1 flex flex-wrap items-center gap-2 text-section font-semibold">
        <span data-testid="machine-band" className={`inline-flex ${band}`}>
          <BandIcon />
        </span>
        <span className="min-w-0 break-all font-mono">{machine.id}</span>
        {machine.stale ? <span className={`pill ${band}`}>stale</span> : null}
        {machine.superseded ? (
          <span className="pill text-muted" data-testid="superseded-mark">
            superseded
          </span>
        ) : null}
      </h2>
      <p className="text-muted [overflow-wrap:anywhere]">
        operator {machine.operator_id || '—'} · {machine.role} · mode {machine.mode || '—'} ·{' '}
        {machine.runtime_version || '—'} · launcher {machine.launcher || '—'}
      </p>
      <p className="tabular flex items-center gap-1.5 text-muted">
        <ClockIcon />
        <span>
          last heartbeat <RelativeTime at={machine.last_heartbeat_at} />
          {machine.heartbeat_age_seconds !== undefined ? (
            <span className="font-mono"> ({machine.heartbeat_age_seconds}s)</span>
          ) : null}
        </span>
      </p>
      {machine.live_wis.length > 0 ? (
        <p className="mt-1 [overflow-wrap:anywhere]">
          <span className="text-muted">live: </span>
          {machine.live_wis.map((wi) =>
            wi.slug ? (
              <a key={wi.id} className="mr-2" href={href(slugProject(wi.slug), 'work-items', wi.id)}>
                {wi.slug}
              </a>
            ) : (
              <span key={wi.id} className="mr-2 break-all font-mono text-muted">
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
