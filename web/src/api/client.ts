import type { ApiErrorBody, ChatEvent, Session } from './types'

/**
 * The API client.
 *
 * Two things it owns that nothing else should. The access token lives here in a
 * module-scoped variable and never in localStorage: a token in storage survives
 * the tab and is readable by any script on the page, while this one dies with the
 * tab and is restored on load from the HttpOnly refresh cookie — which script
 * cannot read at all.
 *
 * And it owns the refresh: a 401 triggers exactly one refresh attempt, with
 * concurrent requests waiting on the same attempt rather than each starting their
 * own. Without that, a dashboard firing four requests at once on an expired token
 * would rotate the refresh token four times and invalidate its own session.
 */

/** Thrown for any non-2xx response, carrying the server's own error shape. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields?: Record<string, string>
  readonly requestId?: string

  constructor(status: number, body: ApiErrorBody['error']) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.fields = body.fields
    this.requestId = body.request_id
  }

  /** True when the session is gone for good and the customer must log in again. */
  get isUnauthenticated(): boolean {
    return this.status === 401
  }
}

let accessToken: string | null = null
let refreshInFlight: Promise<boolean> | null = null

/** Called when the session ends, so the app can send the customer to the login. */
type SessionEndedListener = () => void
let onSessionEnded: SessionEndedListener = () => {}

export function setSessionEndedListener(listener: SessionEndedListener): void {
  onSessionEnded = listener
}

export function setAccessToken(token: string | null): void {
  accessToken = token
}

export function getAccessToken(): string | null {
  return accessToken
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  /** An Idempotency-Key, for the endpoints that move money. */
  idempotencyKey?: string
  /** Set on the refresh call itself, which must never try to refresh again. */
  skipRefresh?: boolean
  signal?: AbortSignal
}

async function rawRequest(path: string, options: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (accessToken) headers['Authorization'] = `Bearer ${accessToken}`
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey

  return fetch(path, {
    method: options.method ?? 'GET',
    headers,
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    // The refresh cookie has to travel, and it is scoped to /api/auth so it is
    // not attached to anything else.
    credentials: 'same-origin',
    signal: options.signal,
  })
}

/**
 * request performs a call, refreshing the session once on a 401 and retrying.
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let response = await rawRequest(path, options)

  if (response.status === 401 && !options.skipRefresh) {
    if (await refreshSession()) {
      response = await rawRequest(path, options)
    }
  }

  if (!response.ok) {
    throw await toApiError(response)
  }
  // 204, and any other empty body.
  if (response.status === 204 || response.headers.get('Content-Length') === '0') {
    return undefined as T
  }
  return (await response.json()) as T
}

/**
 * Fetches a file instead of JSON, with the same one-shot refresh on a 401.
 *
 * A download cannot be a plain link here. The session travels as a bearer token in a
 * header, and a browser navigating to an href sends no headers of ours — the request
 * would arrive unauthenticated and come back a 401 rendered as a page. So the file is
 * fetched like any other call and handed to the browser as a blob.
 *
 * The filename comes from the server's Content-Disposition when it sends one, so the
 * name of the file is decided in the same place as its contents rather than guessed at
 * twice.
 */
export async function requestFile(
  path: string,
  options: RequestOptions = {},
): Promise<{ blob: Blob; filename?: string }> {
  let response = await rawRequest(path, options)

  if (response.status === 401 && !options.skipRefresh) {
    if (await refreshSession()) {
      response = await rawRequest(path, options)
    }
  }

  if (!response.ok) {
    throw await toApiError(response)
  }

  const disposition = response.headers.get('Content-Disposition') ?? ''
  const match = /filename="?([^"]+)"?/.exec(disposition)

  return { blob: await response.blob(), filename: match?.[1] }
}

async function toApiError(response: Response): Promise<ApiError> {
  let body: ApiErrorBody['error'] = {
    code: 'unknown',
    message: 'No se pudo completar la operación. Inténtalo de nuevo.',
  }
  try {
    const parsed = (await response.json()) as ApiErrorBody
    if (parsed?.error) body = parsed.error
  } catch {
    // A response that is not JSON — a proxy error page, say. The generic message
    // above is the honest thing to show; the status is still carried.
  }
  return new ApiError(response.status, body)
}

/**
 * refreshSession exchanges the refresh cookie for a new access token.
 *
 * Concurrent callers share one attempt. The listener fires only when the refresh
 * genuinely fails, so a transient network error does not log the customer out.
 */
export async function refreshSession(): Promise<boolean> {
  if (refreshInFlight) return refreshInFlight

  refreshInFlight = (async () => {
    try {
      const session = await request<Session>('/api/auth/refresh', {
        method: 'POST',
        skipRefresh: true,
      })
      accessToken = session.access_token
      return true
    } catch (error) {
      if (error instanceof ApiError && error.isUnauthenticated) {
        accessToken = null
        onSessionEnded()
      }
      return false
    } finally {
      refreshInFlight = null
    }
  })()

  return refreshInFlight
}

/**
 * streamChat posts a message and yields the events the server narrates.
 *
 * fetch rather than EventSource, because EventSource cannot send a body or an
 * Authorization header — it only does GET. Parsing SSE by hand is a dozen lines
 * and buys the ability to authenticate the request properly.
 */
export async function* streamChat(
  message: string,
  signal?: AbortSignal,
): AsyncGenerator<ChatEvent> {
  const response = await rawRequest('/api/chat', {
    method: 'POST',
    body: { message },
    signal,
  })

  if (response.status === 401) {
    if (await refreshSession()) {
      yield* streamChat(message, signal)
      return
    }
  }
  if (!response.ok || !response.body) {
    throw await toApiError(response)
  }

  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
  let buffer = ''

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += value

      // SSE frames are separated by a blank line. A chunk can split a frame, so
      // only whole frames are consumed and the remainder stays buffered.
      let boundary = buffer.indexOf('\n\n')
      while (boundary !== -1) {
        const frame = buffer.slice(0, boundary)
        buffer = buffer.slice(boundary + 2)

        const event = parseFrame(frame)
        if (event) yield event

        boundary = buffer.indexOf('\n\n')
      }
    }
  } finally {
    reader.releaseLock()
  }
}

function parseFrame(frame: string): ChatEvent | null {
  let kind = ''
  let data = ''
  for (const line of frame.split('\n')) {
    if (line.startsWith('event: ')) kind = line.slice(7).trim()
    else if (line.startsWith('data: ')) data += line.slice(6)
  }
  if (!kind) return null

  try {
    const payload = data ? (JSON.parse(data) as Record<string, unknown>) : {}
    return { kind, ...payload } as ChatEvent
  } catch {
    // A malformed frame is dropped rather than breaking the stream: the rest of
    // the reply is still worth showing.
    return null
  }
}

/** newIdempotencyKey makes a retry of the same movement safe. */
export function newIdempotencyKey(): string {
  return crypto.randomUUID()
}
