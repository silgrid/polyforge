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

// apiPost is the ONE write helper of the console's private face: every
// write the SPA performs sets the console's own custom header, which a
// cross-site form cannot carry (and a cross-site fetch carrying one
// meets a CORS preflight this same-origin face never answers), so the
// browser's ambient Basic credential cannot be ridden from another
// origin. Today the face's one write route is the project restore
// (polyforge#30).
export async function apiPost<T>(path: string): Promise<T> {
  const url = new URL(path, window.location.origin)
  const resp = await fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'X-PF-Console-Write': '1' },
  })
  if (resp.status === 401) {
    throw new ApiError(401, 'authentication required')
  }
  const body = await resp.text()
  if (!resp.ok) {
    let detail = body
    try {
      detail = JSON.parse(body).error || JSON.parse(body).detail || body
    } catch {
      // keep the raw body
    }
    throw new ApiError(resp.status, detail)
  }
  return JSON.parse(body) as T
}
