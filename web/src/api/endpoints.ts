import { newIdempotencyKey, request } from './client'
import type {
  Account,
  AccountType,
  ChatHistory,
  Dashboard,
  Me,
  MovementKind,
  Session,
  Transaction,
  TransactionPage,
} from './types'

/**
 * One function per endpoint, so a URL or a query-string name appears exactly once
 * in the app. Everything below is a thin wrapper — the interesting behaviour is in
 * the client (refresh, streaming) and in the hooks (caching, invalidation).
 */

// --- sessions ---------------------------------------------------------------

export const login = (email: string, password: string) =>
  request<Session>('/api/auth/login', { method: 'POST', body: { email, password } })

export const register = (input: {
  email: string
  password: string
  full_name: string
  account_type?: string
}) => request<Session>('/api/auth/register', { method: 'POST', body: input })

export const logout = () => request<void>('/api/auth/logout', { method: 'POST' })

// --- accounts ---------------------------------------------------------------

export const fetchMe = () => request<Me>('/api/me')

export const fetchDashboard = () => request<Dashboard>('/api/dashboard/summary')

// --- movements --------------------------------------------------------------

export interface MovementInput {
  /** A decimal string, never a number: a float would lose cents. */
  amount: string
  account_number?: string
  to_account_number?: string
  description?: string
  /** Reserve the funds and wait for the customer instead of completing. */
  require_confirmation?: boolean
}

/**
 * submitMovement performs a deposit, withdrawal or transfer.
 *
 * The idempotency key is minted per call rather than per attempt, and the caller
 * reuses the same one when retrying — which is the only way a retry can be told
 * apart from a second deliberate movement of the same amount.
 */
export const submitMovement = (
  kind: 'deposit' | 'withdraw' | 'transfer',
  input: MovementInput,
  idempotencyKey: string,
) =>
  request<Transaction>(`/api/transactions/${kind}`, {
    method: 'POST',
    body: input,
    idempotencyKey,
  })

export const resolveConfirmation = (holdId: string, action: 'confirm' | 'cancel') =>
  request<Transaction>(`/api/transactions/${holdId}/${action}`, { method: 'POST' })

// --- history ----------------------------------------------------------------

export interface HistoryQuery {
  accountNumber?: string
  kind?: MovementKind
  since?: string
  until?: string
  search?: string
  limit?: number
  cursor?: string
}

/**
 * Opens an additional account for the signed-in customer.
 *
 * The owner is never sent: the server takes it from the session. A body carrying a user
 * id would be one forged field away from opening an account in somebody else's name.
 */
export function openAccount(accountType: AccountType, alias = '') {
  return request<Account>('/api/accounts', {
    method: 'POST',
    body: { account_type: accountType, alias },
  })
}

/**
 * Renames one of the signed-in customer's accounts.
 *
 * PATCH because it changes one field and leaves the rest alone; a PUT would imply the
 * body is the whole account, and nobody may replace a balance or a number. An empty
 * alias is a valid request — it is how a name is removed, and the account goes back to
 * being labelled by its type.
 */
export function renameAccount(accountNumber: string, alias: string) {
  return request<Account>(`/api/accounts/${encodeURIComponent(accountNumber)}`, {
    method: 'PATCH',
    body: { alias },
  })
}

export function fetchHistory(query: HistoryQuery = {}) {
  const params = new URLSearchParams()
  if (query.accountNumber) params.set('account_number', query.accountNumber)
  if (query.kind) params.set('kind', query.kind)
  if (query.since) params.set('since', query.since)
  if (query.until) params.set('until', query.until)
  if (query.search) params.set('search', query.search)
  if (query.limit) params.set('limit', String(query.limit))
  if (query.cursor) params.set('cursor', query.cursor)

  const qs = params.toString()
  return request<TransactionPage>(`/api/transactions${qs ? `?${qs}` : ''}`)
}

// --- chat -------------------------------------------------------------------

export const fetchChatHistory = () => request<ChatHistory>('/api/chat')

export const clearChat = () => request<void>('/api/chat', { method: 'DELETE' })

export { newIdempotencyKey }
