import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'

import * as api from '@/api/endpoints'
import type { CheckpointInput, EntryPatch, EntryQuery } from '@/api/endpoints'

/**
 * Server state.
 *
 * One rule shapes all of it: anything that changes what the app says about money
 * — an import, a sync, a stated balance, a movement relabelled as a transfer —
 * invalidates everything that shows money. A total still showing the previous
 * figure is not a stale cache, it is a wrong number about somebody's money.
 */

export const keys = {
  me: ['me'] as const,
  accounts: ['accounts'] as const,
  account: (id: string) => ['account', id] as const,
  checkpoints: (id: string) => ['checkpoints', id] as const,
  entries: (query: EntryQuery) => ['entries', query] as const,
  importRuns: (id: string) => ['import-runs', id] as const,
  chat: ['chat'] as const,
  investmentLink: (id: string) => ['investment-link', id] as const,
  portfolio: (id: string) => ['portfolio', id] as const,
  investmentTrades: (id: string) => ['investment-trades', id] as const,
  security: ['security'] as const,
  categories: ['categories'] as const,
}

/** Invalidates everything that displays money. */
function useMoneyInvalidation() {
  const queryClient = useQueryClient()
  return () =>
    Promise.all(
      [
        'me',
        'accounts',
        'account',
        'checkpoints',
        'entries',
        'import-runs',
        'portfolio',
      ].map((key) => queryClient.invalidateQueries({ queryKey: [key] })),
    )
}

// --- accounts ---------------------------------------------------------------

export function useMe() {
  return useQuery({ queryKey: keys.me, queryFn: api.fetchMe })
}

export function useAccounts() {
  return useQuery({ queryKey: keys.accounts, queryFn: api.fetchAccounts })
}

export function useAccount(id: string) {
  return useQuery({ queryKey: keys.account(id), queryFn: () => api.fetchAccount(id) })
}

export function useUpdateAccount(id: string) {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: (patch: { alias?: string; type?: 'checking' | 'savings' }) =>
      api.updateAccount(id, patch),
    onSuccess: invalidate,
  })
}

export function useDeleteAccount() {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: (id: string) => api.deleteAccount(id),
    onSuccess: invalidate,
  })
}

export function useCheckpoints(accountId: string) {
  return useQuery({
    queryKey: keys.checkpoints(accountId),
    queryFn: () => api.fetchCheckpoints(accountId),
  })
}

export function useAddCheckpoint(accountId: string) {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: (input: CheckpointInput) => api.addCheckpoint(accountId, input),
    onSuccess: invalidate,
  })
}

export function useDeleteCheckpoint(accountId: string) {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: (checkpointId: string) => api.deleteCheckpoint(accountId, checkpointId),
    onSuccess: invalidate,
  })
}

export function usePinCheckpoint(accountId: string) {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: (checkpointId: string | null) => api.pinCheckpoint(accountId, checkpointId),
    onSuccess: invalidate,
  })
}

// --- movements --------------------------------------------------------------

/** One page of movements; keeps the previous page visible while the next loads. */
export function useEntries(query: EntryQuery) {
  return useQuery({
    queryKey: keys.entries(query),
    queryFn: () => api.fetchEntries(query),
    placeholderData: (previous) => previous,
  })
}

/** Every page of movements matching a query, loaded on demand. */
export function useEntryPages(query: Omit<EntryQuery, 'cursor'>) {
  return useInfiniteQuery({
    queryKey: ['entries', 'pages', query],
    queryFn: ({ pageParam }) => api.fetchEntries({ ...query, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.has_more ? last.next_cursor : undefined),
  })
}

export function useUpdateEntry() {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: EntryPatch }) =>
      api.updateEntry(id, patch),
    onSuccess: invalidate,
  })
}

// --- imports -----------------------------------------------------------------

export function useImportStatement() {
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: ({ file, accountId }: { file: File; accountId?: string }) =>
      api.importStatement(file, accountId),
    onSuccess: invalidate,
  })
}

/** Parses a statement without importing it, so nothing is invalidated. */
export function useDryRunStatement() {
  return useMutation({ mutationFn: (file: File) => api.dryRunStatement(file) })
}

export function useImportRuns(accountId: string) {
  return useQuery({
    queryKey: keys.importRuns(accountId),
    queryFn: () => api.fetchImportRuns(accountId),
  })
}

// --- investments -------------------------------------------------------------

export function useInvestmentLink(accountId: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.investmentLink(accountId),
    queryFn: () => api.fetchInvestmentLink(accountId),
    enabled: options.enabled ?? true,
    // Absent is an ordinary answer here, not a failure worth retrying.
    retry: false,
  })
}

export function useLinkIBKR() {
  const invalidate = useMoneyInvalidation()
  return useMutation({ mutationFn: api.linkIBKR, onSuccess: invalidate })
}

export function useRelinkIBKR(accountId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: { flex_query_id: string; flex_token: string }) =>
      api.relinkIBKR(accountId, input),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: keys.investmentLink(accountId) }),
  })
}

export function useSyncInvestmentAccount(accountId: string) {
  const queryClient = useQueryClient()
  const invalidate = useMoneyInvalidation()
  return useMutation({
    mutationFn: () => api.syncInvestmentAccount(accountId),
    // Settled rather than success: a failed sync is recorded on the link too,
    // and the page should show it without a reload.
    onSettled: () => {
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: keys.investmentLink(accountId) })
      void queryClient.invalidateQueries({ queryKey: keys.investmentTrades(accountId) })
    },
  })
}

export function usePortfolio(accountId: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.portfolio(accountId),
    queryFn: () => api.fetchPortfolio(accountId),
    enabled: options.enabled ?? true,
  })
}

export function useInvestmentTrades(
  accountId: string,
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: keys.investmentTrades(accountId),
    queryFn: () => api.fetchInvestmentTrades(accountId, 20),
    enabled: options.enabled ?? true,
  })
}

// --- chat ---------------------------------------------------------------------

export function useChatHistory() {
  return useQuery({ queryKey: keys.chat, queryFn: api.fetchChatHistory })
}

export function useClearChat() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: api.clearChat,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.chat }),
  })
}

// --- security (PIN) ----------------------------------------------------

export function useSecurityStatus() {
  return useQuery({ queryKey: keys.security, queryFn: api.fetchSecurityStatus })
}

function useSecurityInvalidation() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: keys.security })
}

export function useSetPin() {
  const invalidate = useSecurityInvalidation()
  return useMutation({
    mutationFn: ({ currentPassword, pin }: { currentPassword: string; pin: string }) =>
      api.setPin(currentPassword, pin),
    onSuccess: invalidate,
  })
}

export function useRemovePin() {
  const invalidate = useSecurityInvalidation()
  return useMutation({
    mutationFn: (currentPassword: string) => api.removePin(currentPassword),
    onSuccess: invalidate,
  })
}

export function useEnableDeviceForPin() {
  const invalidate = useSecurityInvalidation()
  return useMutation({
    mutationFn: api.enableDeviceForPin,
    onSuccess: invalidate,
  })
}

export function useDisableDeviceForPin() {
  const invalidate = useSecurityInvalidation()
  return useMutation({
    mutationFn: api.disableDeviceForPin,
    onSuccess: invalidate,
  })
}

// --- categories ---------------------------------------------------------------

export function useCategories() {
  return useQuery({ queryKey: keys.categories, queryFn: api.fetchCategories })
}
