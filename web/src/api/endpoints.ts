import { newIdempotencyKey, request, requestFile, requestUpload } from './client'
import type {
  Account,
  AccountType,
  CategoryNode,
  CategorySpend,
  ChatHistory,
  Dashboard,
  DeviceStatus,
  ExternalAccount,
  ExternalTransaction,
  ImportBatch,
  ImportResult,
  InvestmentLinkStatus,
  InvestmentSyncResult,
  InvestmentTrade,
  Me,
  MovementKind,
  Portfolio,
  ReconcileResult,
  SecurityStatus,
  Session,
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

export const loginPin = (pin: string) =>
  request<Session>('/api/auth/login-pin', { method: 'POST', body: { pin } })

/**
 * Asks, before anything is typed, whether this browser is trusted for PIN
 * login. Never throws on "not trusted" — that is the ordinary `{ trusted:
 * false }` response, not a failure.
 */
export const fetchDeviceStatus = () => request<DeviceStatus>('/api/auth/device')

// --- security (PIN) -----------------------------------------------------

export const fetchSecurityStatus = () =>
  request<SecurityStatus>('/api/auth/security/status')

export const setPin = (currentPassword: string, pin: string) =>
  request<void>('/api/auth/security/pin', {
    method: 'POST',
    body: { current_password: currentPassword, pin },
  })

export const removePin = (currentPassword: string) =>
  request<void>('/api/auth/security/pin', {
    method: 'DELETE',
    body: { current_password: currentPassword },
  })

export const enableDeviceForPin = () =>
  request<SecurityStatus>('/api/auth/security/pin/device', { method: 'POST' })

export const disableDeviceForPin = () =>
  request<void>('/api/auth/security/pin/device', { method: 'DELETE' })

// --- accounts ---------------------------------------------------------------

export const fetchMe = () => request<Me>('/api/me')

export const fetchDashboard = () => request<Dashboard>('/api/dashboard/summary')

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

/**
 * Deletes one of the signed-in customer's accounts.
 *
 * The server refuses unless the account is empty, is not the customer's only
 * one, and (for an investment account) has no IBKR link — see
 * accounts.Service.Delete. This never removes any ledger money; it removes
 * the row that lets the app find the account at all.
 */
export function deleteAccount(accountNumber: string) {
  return request<void>(`/api/accounts/${encodeURIComponent(accountNumber)}`, {
    method: 'DELETE',
  })
}

/**
 * Brings a real account's balance to a stated true value ("sincerar saldo"),
 * by posting one corrective deposit or withdrawal for the difference —
 * TigerBeetle has no way to edit or delete a past movement, so this is the
 * only way to fix a balance a bank import got wrong. targetBalance is a
 * decimal string, the same as a movement amount, but unlike one it may be
 * zero or negative (an empty or overdrawn account is a valid true balance).
 */
export function reconcileAccount(
  accountNumber: string,
  targetBalance: string,
  idempotencyKey: string,
) {
  return request<ReconcileResult>('/api/transactions/reconcile', {
    method: 'POST',
    body: { account_number: accountNumber, target_balance: targetBalance },
    idempotencyKey,
  })
}

/**
 * The filters, as the API takes them.
 *
 * Shared by the paginated view and the CSV export so the file can only ever describe
 * the same movements the screen was showing. Built separately, the two would agree
 * until the day a filter was added to one of them.
 */
function historyParams(query: HistoryQuery): URLSearchParams {
  const params = new URLSearchParams()
  if (query.accountNumber) params.set('account_number', query.accountNumber)
  if (query.kind) params.set('kind', query.kind)
  if (query.since) params.set('since', query.since)
  if (query.until) params.set('until', query.until)
  if (query.search) params.set('search', query.search)
  if (query.limit) params.set('limit', String(query.limit))
  if (query.cursor) params.set('cursor', query.cursor)
  return params
}

export function fetchHistory(query: HistoryQuery = {}) {
  const qs = historyParams(query).toString()
  return request<TransactionPage>(`/api/transactions${qs ? `?${qs}` : ''}`)
}

/**
 * Downloads the filtered history as a CSV file.
 *
 * `limit` and `cursor` are dropped rather than forwarded. They belong to reading the
 * history a page at a time, and a statement is a document: sending a cursor would ask
 * the server for a file that silently begins in the middle of somebody's movements.
 */
export function exportHistoryCSV(query: HistoryQuery = {}) {
  const qs = historyParams({ ...query, limit: undefined, cursor: undefined }).toString()
  return requestFile(`/api/transactions/export.csv${qs ? `?${qs}` : ''}`)
}

// --- chat -------------------------------------------------------------------

export const fetchChatHistory = () => request<ChatHistory>('/api/chat')

export const clearChat = () => request<void>('/api/chat', { method: 'DELETE' })

// --- investments --------------------------------------------------------------

export const fetchInvestmentLink = (accountNumber: string) =>
  request<InvestmentLinkStatus>(
    `/api/investments/accounts/${encodeURIComponent(accountNumber)}/link`,
  )

/**
 * Links (or replaces the link of) an investment account to an IBKR Flex Query.
 *
 * The token is sent once and never read back — `fetchInvestmentLink` never
 * returns it, only whether a link exists and how its last sync went.
 */
export function linkInvestmentAccount(
  accountNumber: string,
  input: { ibkr_account_id: string; flex_query_id: string; flex_token: string },
) {
  return request<void>(
    `/api/investments/accounts/${encodeURIComponent(accountNumber)}/link`,
    {
      method: 'PUT',
      body: input,
    },
  )
}

export function syncInvestmentAccount(accountNumber: string) {
  return request<InvestmentSyncResult>(
    `/api/investments/accounts/${encodeURIComponent(accountNumber)}/sync`,
    { method: 'POST' },
  )
}

export function fetchPortfolio(accountNumber: string) {
  return request<Portfolio>(
    `/api/investments/accounts/${encodeURIComponent(accountNumber)}/portfolio`,
  )
}

export function fetchInvestmentTrades(accountNumber: string, limit?: number) {
  const qs = limit ? `?limit=${limit}` : ''
  return request<{ trades: InvestmentTrade[] }>(
    `/api/investments/accounts/${encodeURIComponent(accountNumber)}/trades${qs}`,
  )
}

// --- bank import ------------------------------------------------------------

export const fetchExternalAccounts = () =>
  request<{ accounts: ExternalAccount[] }>('/api/external-accounts')

/**
 * Uploads a bank statement file. Which external account it belongs to is
 * never chosen by hand — the backend reads that straight out of the file
 * itself. accountNumber is a separate, optional choice that only matters the
 * first time a bank-account statement's external account is seen: which of
 * the customer's own real accounts to link it to. Omitted, one opens
 * automatically. Ignored for a card statement and for a repeat import of an
 * account already linked.
 */
export const importBankStatement = (file: File, accountNumber?: string) =>
  requestUpload<ImportResult>(
    '/api/external-accounts/import',
    file,
    accountNumber ? { account_number: accountNumber } : undefined,
  )

/**
 * Deletes a card and everything imported under it. Refused by the server if
 * the id turns out to be a linked bank account's identity rather than a
 * card — that one is deleted through deleteAccount instead.
 */
export const deleteExternalAccount = (externalAccountId: string) =>
  request<void>(`/api/external-accounts/${encodeURIComponent(externalAccountId)}`, {
    method: 'DELETE',
  })

/**
 * Brings a card's declared balance to a stated true value ("sincerar
 * saldo"), by inserting one adjustment row for the difference —
 * declared_balance is always a live sum over external_transactions, never a
 * stored figure, so this never touches the ledger (a card never does).
 */
export const reconcileExternalAccount = (
  externalAccountId: string,
  targetBalance: string,
) =>
  request<ReconcileResult>(
    `/api/external-accounts/${encodeURIComponent(externalAccountId)}/reconcile`,
    { method: 'POST', body: { target_balance: targetBalance } },
  )

export function fetchExternalTransactions(externalAccountId: string, limit?: number) {
  const qs = limit ? `?limit=${limit}` : ''
  return request<{ transactions: ExternalTransaction[] }>(
    `/api/external-accounts/${encodeURIComponent(externalAccountId)}/transactions${qs}`,
  )
}

export const fetchImportHistory = (externalAccountId: string) =>
  request<{ imports: ImportBatch[] }>(
    `/api/external-accounts/${encodeURIComponent(externalAccountId)}/imports`,
  )

export const fetchSpendByCategory = (externalAccountId: string) =>
  request<{ spend: CategorySpend[] }>(
    `/api/external-accounts/${encodeURIComponent(externalAccountId)}/spend-by-category`,
  )

export const setExternalTransactionCategory = (
  transactionId: string,
  categoryId: string | null,
) =>
  request<void>(
    `/api/external-transactions/${encodeURIComponent(transactionId)}/category`,
    {
      method: 'PATCH',
      body: { category_id: categoryId },
    },
  )

// --- categories ---------------------------------------------------------------

export const fetchCategories = () =>
  request<{ categories: CategoryNode[] }>('/api/categories')

export { newIdempotencyKey }
