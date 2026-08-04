import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import * as api from '@/api/endpoints'
import type { HistoryQuery, MovementInput } from '@/api/endpoints'

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

export function useMovement(kind: 'deposit' | 'withdraw' | 'transfer') {
  const invalidate = useMoneyInvalidation()

  return useMutation({
    mutationFn: ({
      input,
      idempotencyKey,
    }: {
      input: MovementInput
      /**
       * Minted once per attempt by the form and reused across retries, so a
       * resubmission after a lost response is recognised instead of moving the
       * money twice.
       */
      idempotencyKey: string
    }) => api.submitMovement(kind, input, idempotencyKey),
    onSuccess: invalidate,
  })
}

export function useResolveConfirmation() {
  const invalidate = useMoneyInvalidation()

  return useMutation({
    mutationFn: ({ holdId, action }: { holdId: string; action: 'confirm' | 'cancel' }) =>
      api.resolveConfirmation(holdId, action),
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
    mutationFn: api.openAccount,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.me })
      void queryClient.invalidateQueries({ queryKey: keys.dashboard })
    },
  })
}
