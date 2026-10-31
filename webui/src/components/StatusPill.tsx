// StatusPill renders one work-item status as a compact pill in its
// status hue (the status tokens of styles.css), keeping the lowercase
// status word and its status-<status> test hook. Class-only colour —
// never an inline style.
const STATUS_CLASSES: Record<string, string> = {
  queued: 'text-muted',
  running: 'text-ok',
  paused: 'text-warn',
  blocked: 'text-bad',
  wrapped: 'text-fg',
  failed: 'text-bad',
  canceled: 'text-muted',
}

export default function StatusPill({ status }: { status: string }) {
  const cls = STATUS_CLASSES[status] ?? 'text-muted'
  return (
    <span data-testid={`status-${status}`} className={`pill ${cls}`}>
      {status}
    </span>
  )
}
