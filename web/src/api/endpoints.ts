import { request, requestFile, requestUpload } from './client'
import type {
  AccountList,
  Account,
  CategoryNode,
  CategoryRule,
  CategoryTotal,
  ChatHistory,
  Checkpoint,
  DeviceStatus,
  EntryKind,
  EntryPage,
  FlowPoint,
  Proposal,
  Entry,
  ImportResult,
  ImportRun,
  InvestmentLinkStatus,
  InvestmentSyncResult,
  InvestmentTrade,
  Me,
  Portfolio,
  Reconciliation,
  SecurityStatus,
  Session,
  StatementFileReport,
  TransferSuggestion,
} from './types'

/**
 * One function per endpoint, so a URL or a query-string name appears exactly once
 * in the app. Everything below is a thin wrapper — the interesting behaviour is in
 * the client (refresh, streaming) and in the hooks (caching, invalidation).
 */

export const login = (email: string, password: string) =>
  request<Session>('/api/auth/login', { method: 'POST', body: { email, password } })

export const register = (input: { email: string; password: string; full_name: string }) =>
  request<Session>('/api/auth/register', { method: 'POST', body: input })

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

// --- accounts ---------------------------------------------------------------

export const fetchMe = () => request<Me>('/api/me')

export const fetchAccounts = () => request<AccountList>('/api/accounts')

export const fetchAccount = (id: string) =>
  request<Account>(`/api/accounts/${encodeURIComponent(id)}`)

/** Renames an account, or relabels a bank account as checking or savings. */
export const updateAccount = (
  id: string,
  patch: { alias?: string; type?: 'checking' | 'savings' },
) =>
  request<Account>(`/api/accounts/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: patch,
  })

/** Deletes an account and everything imported into it. */
export const deleteAccount = (id: string) =>
  request<void>(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const fetchCheckpoints = (accountId: string) =>
  request<{ checkpoints: Checkpoint[] }>(
    `/api/accounts/${encodeURIComponent(accountId)}/checkpoints`,
  )

export interface CheckpointInput {
  as_of: string
  /** Signed from the owner's view: a card owing $14.30 is "-14.30". */
  balance: string
  pin?: boolean
  due_on?: string
  minimum_payment?: string
  note?: string
}

export const addCheckpoint = (accountId: string, input: CheckpointInput) =>
  request<Checkpoint>(`/api/accounts/${encodeURIComponent(accountId)}/checkpoints`, {
    method: 'POST',
    body: input,
  })

export const deleteCheckpoint = (accountId: string, checkpointId: string) =>
  request<void>(
    `/api/accounts/${encodeURIComponent(accountId)}/checkpoints/${encodeURIComponent(checkpointId)}`,
    { method: 'DELETE' },
  )

/** Makes a checkpoint the anchor, or with null lets the earliest one anchor. */
export const pinCheckpoint = (accountId: string, checkpointId: string | null) =>
  request<Account>(`/api/accounts/${encodeURIComponent(accountId)}/anchor`, {
    method: 'PUT',
    body: { checkpoint_id: checkpointId },
  })

export const fetchReconciliation = (accountId: string) =>
  request<Reconciliation>(`/api/accounts/${encodeURIComponent(accountId)}/reconciliation`)

// --- movements --------------------------------------------------------------

export interface EntryQuery {
  accountId?: string
  kinds?: EntryKind[]
  from?: string
  to?: string
  search?: string
  categoryId?: string
  uncategorized?: boolean
  limit?: number
  cursor?: string
}

export function entryParams(query: EntryQuery): URLSearchParams {
  const params = new URLSearchParams()
  if (query.accountId) params.append('account_id', query.accountId)
  for (const kind of query.kinds ?? []) params.append('kind', kind)
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.search) params.set('search', query.search)
  if (query.categoryId) params.append('category_id', query.categoryId)
  if (query.uncategorized) params.set('uncategorized', 'true')
  if (query.limit) params.set('limit', String(query.limit))
  if (query.cursor) params.set('cursor', query.cursor)
  return params
}

export function fetchEntries(query: EntryQuery = {}) {
  const qs = entryParams(query).toString()
  return request<EntryPage>(`/api/entries${qs ? `?${qs}` : ''}`)
}

export interface EntryPatch {
  category_id?: string | null
  note?: string
  /** Marks a movement as a transfer between the owner's own accounts, or undoes it. */
  transfer?: boolean
}

export const updateEntry = (id: string, patch: EntryPatch) =>
  request<Entry>(`/api/entries/${encodeURIComponent(id)}`, { method: 'PATCH', body: patch })

/** Downloads the movements matching a query as a CSV file, every page of them. */
export function exportEntriesCSV(query: EntryQuery = {}) {
  const qs = entryParams({ ...query, limit: undefined, cursor: undefined }).toString()
  return requestFile(`/api/entries/export.csv${qs ? `?${qs}` : ''}`)
}

// --- reports -------------------------------------------------------------------

export const fetchCategoryTotals = (query: {
  from?: string
  to?: string
  kind?: 'spend' | 'income'
}) => {
  const params = new URLSearchParams()
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.kind) params.set('kind', query.kind)
  return request<{ totals: CategoryTotal[] }>(`/api/reports/categories?${params}`)
}

export const fetchFlow = (query: {
  from?: string
  to?: string
  granularity?: 'day' | 'month'
}) => {
  const params = new URLSearchParams()
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.granularity) params.set('granularity', query.granularity)
  return request<{ points: FlowPoint[] }>(`/api/reports/flow?${params}`)
}

// --- transfers -----------------------------------------------------------------

export const fetchTransferSuggestions = () =>
  request<{ suggestions: TransferSuggestion[] }>('/api/transfers/suggestions')

export const decideTransfer = (input: {
  out_entry_id: string
  in_entry_id: string
  confirm: boolean
}) => request<void>('/api/transfers/decisions', { method: 'POST', body: input })

// --- rules ---------------------------------------------------------------------

export interface RuleInput {
  match_text: string
  category_id?: string | null
  transfer: boolean
  priority?: number
  apply_to_existing?: boolean
}

export const fetchRules = () => request<{ rules: CategoryRule[] }>('/api/category-rules')

export const createRule = (input: RuleInput) =>
  request<CategoryRule>('/api/category-rules', { method: 'POST', body: input })

export const updateRule = (id: string, input: RuleInput) =>
  request<CategoryRule>(`/api/category-rules/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: input,
  })

export const deleteRule = (id: string) =>
  request<void>(`/api/category-rules/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- proposals -----------------------------------------------------------------

export const fetchProposals = () => request<{ proposals: Proposal[] }>('/api/proposals')

export const applyProposal = (id: string) =>
  request<Proposal>(`/api/proposals/${encodeURIComponent(id)}/apply`, { method: 'POST' })

export const rejectProposal = (id: string) =>
  request<Proposal>(`/api/proposals/${encodeURIComponent(id)}/reject`, { method: 'POST' })

// --- chat -------------------------------------------------------------------

export const fetchChatHistory = () => request<ChatHistory>('/api/chat')

export const clearChat = () => request<void>('/api/chat', { method: 'DELETE' })

// --- investments -------------------------------------------------------------

/** Links an IBKR account, creating its brokerage account on first sight. */
export const linkIBKR = (input: {
  ibkr_account_id: string
  flex_query_id: string
  flex_token: string
}) =>
  request<{ account_id: string }>('/api/investments/link', { method: 'POST', body: input })

export const relinkIBKR = (
  accountId: string,
  input: { flex_query_id: string; flex_token: string },
) =>
  request<void>(`/api/investments/accounts/${encodeURIComponent(accountId)}/link`, {
    method: 'PUT',
    body: input,
  })

export const fetchInvestmentLink = (accountId: string) =>
  request<InvestmentLinkStatus>(
    `/api/investments/accounts/${encodeURIComponent(accountId)}/link`,
  )

export const syncInvestmentAccount = (accountId: string) =>
  request<InvestmentSyncResult>(
    `/api/investments/accounts/${encodeURIComponent(accountId)}/sync`,
    { method: 'POST' },
  )

export const fetchPortfolio = (accountId: string) =>
  request<Portfolio>(`/api/investments/accounts/${encodeURIComponent(accountId)}/portfolio`)

export const fetchInvestmentTrades = (accountId: string, limit?: number) =>
  request<{ trades: InvestmentTrade[] }>(
    `/api/investments/accounts/${encodeURIComponent(accountId)}/trades${limit ? `?limit=${limit}` : ''}`,
  )

// --- imports -----------------------------------------------------------------

/**
 * Imports a statement file. Given an account, a file for any other account is
 * refused instead of being filed elsewhere.
 */
export const importStatement = (file: File, accountId?: string) =>
  requestUpload<ImportResult>(
    '/api/imports',
    file,
    accountId ? { account_id: accountId } : undefined,
  )

/** Parses a statement file and reports what it says, without importing it. */
export const dryRunStatement = (file: File) =>
  requestUpload<StatementFileReport>('/api/imports/dry-run', file)

export const fetchImportRuns = (accountId: string) =>
  request<{ runs: ImportRun[] }>(`/api/imports?account_id=${encodeURIComponent(accountId)}`)

// --- categories ---------------------------------------------------------------

export const fetchCategories = () =>
  request<{ categories: CategoryNode[] }>('/api/categories')
