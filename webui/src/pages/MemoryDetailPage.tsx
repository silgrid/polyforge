import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { MemoryDetail } from '../types'
import NotFound from '../components/NotFound'
import TypeDot from '../components/TypeDot'
import SanitizedHtml from '../components/SanitizedHtml'
import RelativeTime from '../components/RelativeTime'
import { Muted } from '../components/ui'

// MemoryDetailPage is the #103 header card over the sanitized body
// card: kind mark + title, visibility badge, work-item and tag chips, the
// relative time, then the already-inert HTML (links inside bodies
// stayed non-clickable text when the server rendered them) and the
// monospace id footer (#103: the object's id stays visible, verbatim).
export default function MemoryDetailPage({ project, id }: { project: string; id: string }) {
  const [detail, setDetail] = useState<MemoryDetail | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    setDetail(null)
    setMissing(false)
    apiGet<MemoryDetail>('/web/api/memories/' + encodeURIComponent(id), { project })
      .then(setDetail)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) setMissing(true)
      })
  }, [project, id])

  if (missing) return <NotFound />
  if (detail === null) return <Muted>Loading…</Muted>

  return (
    <article className="flex max-w-3xl flex-col gap-4" data-testid="memory-detail">
      <header className="card px-5 py-4">
        <h1 className="mb-2 flex items-center gap-2.5 text-title font-semibold tracking-tight [overflow-wrap:anywhere]">
          <TypeDot type={detail.type} />
          {detail.title}
        </h1>
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted">
          <span className="rounded border border-line px-1.5">{detail.type}</span>
          <span className="rounded border border-line px-1.5">{detail.visibility}</span>
          {detail.work_item_id ? (
            <a className="rounded border border-line px-1.5 font-mono" href={href(project, 'work-items', detail.work_item_id)}>
              {detail.work_item_id}
            </a>
          ) : null}
          {detail.tags.map((tag) => (
            <span key={tag} className="tag-chip">
              {tag}
            </span>
          ))}
          <span>
            updated <RelativeTime at={detail.updated_at} />
          </span>
        </p>
      </header>
      <div className="card px-5 py-4">
        <SanitizedHtml html={detail.html} />
      </div>
      <footer className="id-footer" data-testid="memory-id-footer">
        {detail.id}
      </footer>
    </article>
  )
}
