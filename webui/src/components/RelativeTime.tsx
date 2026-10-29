// RelativeTime renders one timestamp as a short relative phrase (the
// viewer's vocabulary: just now / minutes / hours / yesterday / days /
// the plain UTC date for anything older).
export default function RelativeTime({ at }: { at: string }) {
  const t = new Date(at)
  if (Number.isNaN(t.getTime())) return <time>{at}</time>
  const d = Date.now() - t.getTime()
  let label: string
  if (d < 60_000) label = 'just now'
  else if (d < 3_600_000) label = `${Math.floor(d / 60_000)} minutes ago`
  else if (d < 86_400_000) label = `${Math.floor(d / 3_600_000)} hours ago`
  else if (d < 172_800_000) label = 'yesterday'
  else if (d < 14 * 86_400_000) label = `${Math.floor(d / 86_400_000)} days ago`
  else label = t.toISOString().slice(0, 10)
  return (
    <time dateTime={at} title={at}>
      {label}
    </time>
  )
}
