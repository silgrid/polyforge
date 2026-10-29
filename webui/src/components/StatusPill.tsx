// StatusPill renders one work-item status as a compact coloured pill
// (the confirmed visual language). The colour comes from the status
// token class — never an inline style.
const STATUS_CLASSES: Record<string, string> = {
  queued: 'text-blue border-blue',
  running: 'text-green border-green',
  paused: 'text-amber border-amber',
  blocked: 'text-purple border-purple',
  wrapped: 'text-teal border-teal',
  failed: 'text-red border-red',
  canceled: 'text-muted border-muted',
}

export default function StatusPill({ status }: { status: string }) {
  const cls = STATUS_CLASSES[status] ?? 'text-muted border-muted'
  return (
    <span data-testid={`status-${status}`} className={`inline-block border rounded-full px-2 text-xs ${cls}`}>
      {status}
    </span>
  )
}
