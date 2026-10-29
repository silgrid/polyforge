// The one fetch helper: the SPA's only data source is the private,
// same-origin /web/api face (never /api/v1, never a remote origin).
// The browser attaches the Basic credential the document challenge
// already collected, so no key is stored in the client (no
// localStorage, no sessionStorage, no cookie of ours).

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function apiGet<T>(path: string, params?: Record<string, string>): Promise<T> {
  const url = new URL(path, window.location.origin)
  if (params) {
    for (const [k, v] of Object.entries(params)) url.searchParams.set(k, v)
  }
  const resp = await fetch(url, { credentials: 'same-origin' })
  if (resp.status === 401) {
    throw new ApiError(401, 'authentication required')
  }
  const body = await resp.text()
  if (!resp.ok) {
    let detail = body
    try {
      detail = JSON.parse(body).error || body
    } catch {
      // keep the raw body
    }
    throw new ApiError(resp.status, detail)
  }
  return JSON.parse(body) as T
}
