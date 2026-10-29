import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { MemoryDetail } from '../types'
import NotFound from '../components/NotFound'
import TypeDot from '../components/TypeDot'
import SanitizedHtml from '../components/SanitizedHtml'
import RelativeTime from '../components/RelativeTime'

// MemoryDetailPage is the #103 header card over the sanitized body:
// type dot + title, visibility badge, work-item and tag chips, the
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
  if (detail === null) return <p className="text-muted">Loading…</p>

  return (
    <article className="max-w-prose" data-testid="memory-detail">
      <header className="border border-line rounded p-4 mb-4 bg-tint">
        <h1 className="text-xl font-semibold mb-2">
          <TypeDot type={detail.type} />
          {detail.title}
        </h1>
        <p className="text-sm text-muted flex flex-wrap gap-2 items-center">
          <span className="border border-line rounded px-2 bg-bg">{detail.type}</span>
          <span className="border border-line rounded px-2 bg-bg">{detail.visibility}</span>
          {detail.work_item_id ? (
            <a className="border border-line rounded px-2 bg-bg no-underline text-accent" href={href(project, 'work-items', detail.work_item_id)}>
              {detail.work_item_id}
            </a>
          ) : null}
          {detail.tags.map((tag) => (
            <span key={tag} className="border border-line rounded-full px-2 bg-bg">
              {tag}
            </span>
          ))}
          <span>
            updated <RelativeTime at={detail.updated_at} />
          </span>
        </p>
      </header>
      <SanitizedHtml html={detail.html} />
      <footer className="id-footer mt-6" data-testid="memory-id-footer">
        {detail.id}
      </footer>
    </article>
  )
}
