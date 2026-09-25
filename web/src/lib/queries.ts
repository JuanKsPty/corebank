import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'

import * as api from '@/api/endpoints'
import type { HistoryQuery } from '@/api/endpoints'
import type { Account, AccountType } from '@/api/types'

/**
 * Server state.
 *
 * One rule shapes all of it: anything that moved money invalidates everything that
 * shows money. A dashboard still displaying the previous balance after a transfer
 * is not a stale cache, it is a wrong number about somebody's money — so the
 * mutations below invalidate broadly and deliberately rather than surgically.
 */

export const keys = {
  me: ['me'] as const,
  dashboard: ['dashboard'] as const,
  history: (query: HistoryQuery) => ['history', query] as const,
  chat: ['chat'] as const,
  investmentLink: (accountNumber: string) => ['investment-link', accountNumber] as const,
  portfolio: (accountNumber: string) => ['portfolio', accountNumber] as const,
  investmentTrades: (accountNumber: string) =>
    ['investment-trades', accountNumber] as const,
  security: ['security'] as const,
  externalAccounts: ['external-accounts'] as const,
  externalTransactions: (externalAccountId: string) =>
    ['external-transactions', externalAccountId] as const,
  importHistory: (externalAccountId: string) =>
    ['import-history', externalAccountId] as const,
  spendByCategory: (externalAccountId: string) =>
    ['spend-by-category', externalAccountId] as const,
  categories: ['categories'] as const,
}

export function useMe() {
  return useQuery({ queryKey: keys.me, queryFn: api.fetchMe })
}

export function useDashboard() {
  return useQuery({
    queryKey: keys.dashboard,
    queryFn: api.fetchDashboard,
    // A reservation expires on its own after a couple of minutes, and when it does
    // the balance changes with nobody having touched the page. Refetching on an
    // interval is what keeps the held figure honest without a socket.
    refetchInterval: 30_000,
  })
}

export function useHistory(query: HistoryQuery) {
  return useQuery({
    queryKey: keys.history(query),
    queryFn: () => api.fetchHistory(query),
    // Keeping the previous page visible while the next loads stops the table from
    // collapsing to a skeleton on every filter change.
    placeholderData: (previous) => previous,
  })
}

/** Invalidates everything that displays money. */
function useMoneyInvalidation() {
  const queryClient = useQueryClient()
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.me }),
      queryClient.invalidateQueries({ queryKey: keys.dashboard }),
      queryClient.invalidateQueries({ queryKey: ['history'] }),
    ])
}

/**
 * Corrects a real account's balance ("sincerar saldo"). It posts a correcting
 * movement, so everything that displays money is invalidated.
 */
export function useReconcileAccount() {
  const invalidate = useMoneyInvalidation()

  return useMutation({
    mutationFn: ({
      accountNumber,
      targetBalance,
      idempotencyKey,
    }: {
      accountNumber: string
      targetBalance: string
      idempotencyKey: string
    }) => api.reconcileAccount(accountNumber, targetBalance, idempotencyKey),
    onSuccess: invalidate,
  })
}

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

/**
 * Opens an account.
 *
 * Invalidates everything that lists accounts or totals them. A new account is empty, so
 * no balance changes — but every account picker in the application is now missing an
 * option, which is the same kind of wrong.
 */
export function useOpenAccount() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ type, alias }: { type: AccountType; alias?: string }) =>
      api.openAccount(type, alias),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.me })
      void queryClient.invalidateQueries({ queryKey: keys.dashboard })
    },
  })
}

/**
 * Renames one of the customer's accounts.
 *
 * Invalidates the same two queries as opening one, because an account's label appears
 * on every screen that lists accounts and both of these feed those lists. A rename that
 * showed on the detail page but not in the transfer picker would leave somebody choosing
 * between an old name and a new one.
 */
export function useRenameAccount() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ number, alias }: { number: string; alias: string }) =>
      api.renameAccount(number, alias),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.me })
      void queryClient.invalidateQueries({ queryKey: keys.dashboard })
    },
  })
}

/**
 * Deletes one of the customer's accounts. Same invalidation as opening or
 * renaming one — every list that shows accounts is now missing one.
 */
export function useDeleteAccount() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (number: string) => api.deleteAccount(number),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.me })
      void queryClient.invalidateQueries({ queryKey: keys.dashboard })
    },
  })
}

// --- investments --------------------------------------------------------------

/**
 * An investment account's IBKR link, or its absence.
 *
 * A 404 with code `ibkr_not_linked` is a normal state — "not linked yet" — not a
 * transient failure, so this never retries. The caller tells the two apart by
 * checking `error instanceof ApiError && error.code === 'ibkr_not_linked'`.
 */
export function useInvestmentLink(
  accountNumber: string,
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: keys.investmentLink(accountNumber),
    queryFn: () => api.fetchInvestmentLink(accountNumber),
    retry: false,
    enabled: options.enabled ?? true,
  })
}

export function useLinkInvestmentAccount() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      accountNumber,
      input,
    }: {
      accountNumber: string
      input: { ibkr_account_id: string; flex_query_id: string; flex_token: string }
    }) => api.linkInvestmentAccount(accountNumber, input),
    onSuccess: (_, { accountNumber }) => {
      void queryClient.invalidateQueries({ queryKey: keys.investmentLink(accountNumber) })
    },
  })
}

export function usePortfolio(accountNumber: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.portfolio(accountNumber),
    queryFn: () => api.fetchPortfolio(accountNumber),
    enabled: options.enabled ?? true,
  })
}

export function useInvestmentTrades(
  accountNumber: string,
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: keys.investmentTrades(accountNumber),
    queryFn: () => api.fetchInvestmentTrades(accountNumber),
    enabled: options.enabled ?? true,
  })
}

/**
 * Syncs an investment account against its linked IBKR Flex Query.
 *
 * A sync posts real deposits/withdrawals for whatever cash movements IBKR
 * reports, so this invalidates everything `useMoneyInvalidation` does — the
 * same rule every other money-moving mutation in this file follows — in
 * addition to the investment-specific queries a sync also changes.
 */
export function useSyncInvestmentAccount() {
  const queryClient = useQueryClient()
  const invalidateMoney = useMoneyInvalidation()

  return useMutation({
    mutationFn: (accountNumber: string) => api.syncInvestmentAccount(accountNumber),
    onSuccess: (_, accountNumber) => {
      void invalidateMoney()
      void queryClient.invalidateQueries({ queryKey: keys.portfolio(accountNumber) })
      void queryClient.invalidateQueries({ queryKey: keys.investmentTrades(accountNumber) })
      void queryClient.invalidateQueries({ queryKey: keys.investmentLink(accountNumber) })
    },
  })
}

/**
 * Whether an account's headline figure should be its IBKR total (cash + holdings)
 * instead of the ledger cash balance, and both numbers to render either way.
 *
 * `total` only appears once the account is a linked investment account and its
 * portfolio has loaded — a non-investment account, or one not yet linked, simply
 * gets `showTotal: false` and the caller falls back to `account.available`.
 */
export function useAccountTotal(account: Account | undefined) {
  const isInvestment = account?.account_type === 'investment'
  const link = useInvestmentLink(account?.account_number ?? '', { enabled: isInvestment })
  const linked = link.isSuccess && !!link.data
  const portfolio = usePortfolio(account?.account_number ?? '', {
    enabled: isInvestment && linked,
  })

  return {
    total: portfolio.data?.total_value,
    cash: account?.available,
    showTotal: isInvestment && !!portfolio.data,
  }
}

/**
 * The combined holdings value across a set of investment accounts, for the
 * "patrimonio total" figures on the accounts list and the dashboard.
 *
 * An account that isn't linked yet just fails its portfolio fetch (404
 * `ibkr_not_linked`) and contributes nothing — `retry: false` keeps that from
 * turning into a burst of pointless retries.
 */
export function useHoldingsValue(accountNumbers: string[]) {
  const results = useQueries({
    queries: accountNumbers.map((number) => ({
      queryKey: keys.portfolio(number),
      queryFn: () => api.fetchPortfolio(number),
      retry: false,
    })),
  })

  const cents = results.reduce(
    (total, result) => total + (result.data?.holdings_value.cents ?? 0),
    0,
  )

  return { holdings: { cents, formatted: (cents / 100).toFixed(2), currency: 'USD' } }
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

// --- bank import ----------------------------------------------------------

export function useExternalAccounts() {
  return useQuery({ queryKey: keys.externalAccounts, queryFn: api.fetchExternalAccounts })
}

/** Deletes a card. Invalidates the same lists opening or renaming a real
 * account does — every screen that lists cards is now missing one. */
export function useDeleteExternalAccount() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (externalAccountId: string) => api.deleteExternalAccount(externalAccountId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.externalAccounts })
      void queryClient.invalidateQueries({ queryKey: keys.dashboard })
    },
  })
}

/**
 * Corrects a card's declared balance ("sincerar saldo") by inserting one
 * adjustment row. Invalidates the card's own transactions too, since the new
 * row shows up right there in "Movimientos" — not just in declared_balance.
 */
export function useReconcileExternalAccount(externalAccountId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (targetBalance: string) =>
      api.reconcileExternalAccount(externalAccountId, targetBalance),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.externalAccounts })
      void queryClient.invalidateQueries({
        queryKey: keys.externalTransactions(externalAccountId),
      })
    },
  })
}

/**
 * Uploads a statement file.
 *
 * A card statement still lands in its own external account, so this
 * invalidates the card list and that account's own queries, the same way it
 * always has. A bank-account statement is different now: its movements
 * posted for real, so this invalidates everything that displays money —
 * exactly what a deposit, withdrawal or transfer already does — instead.
 */
export function useImportBankStatement() {
  const queryClient = useQueryClient()
  const invalidateMoney = useMoneyInvalidation()

  return useMutation({
    mutationFn: ({ file, accountNumber }: { file: File; accountNumber?: string }) =>
      api.importBankStatement(file, accountNumber),
    onSuccess: (result) => {
      if (result.is_card) {
        void queryClient.invalidateQueries({ queryKey: keys.externalAccounts })
        void queryClient.invalidateQueries({
          queryKey: keys.externalTransactions(result.external_account_id),
        })
        void queryClient.invalidateQueries({
          queryKey: keys.importHistory(result.external_account_id),
        })
        void queryClient.invalidateQueries({
          queryKey: keys.spendByCategory(result.external_account_id),
        })
      } else {
        void invalidateMoney()
      }
    },
  })
}

export function useExternalTransactions(
  externalAccountId: string,
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: keys.externalTransactions(externalAccountId),
    queryFn: () => api.fetchExternalTransactions(externalAccountId),
    enabled: options.enabled ?? true,
  })
}

export function useImportHistory(
  externalAccountId: string,
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: keys.importHistory(externalAccountId),
    queryFn: () => api.fetchImportHistory(externalAccountId),
    enabled: options.enabled ?? true,
  })
}

export function useSpendByCategory(
  externalAccountId: string,
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: keys.spendByCategory(externalAccountId),
    queryFn: () => api.fetchSpendByCategory(externalAccountId),
    enabled: options.enabled ?? true,
  })
}

export function useSetExternalTransactionCategory(externalAccountId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      transactionId,
      categoryId,
    }: {
      transactionId: string
      categoryId: string | null
    }) => api.setExternalTransactionCategory(transactionId, categoryId),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: keys.externalTransactions(externalAccountId),
      })
      void queryClient.invalidateQueries({
        queryKey: keys.spendByCategory(externalAccountId),
      })
    },
  })
}

// --- categories -------------------------------------------------------------

export function useCategories() {
  return useQuery({ queryKey: keys.categories, queryFn: api.fetchCategories })
}
