import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import * as api from '@/api/endpoints'
import { refreshSession, setAccessToken, setSessionEndedListener } from '@/api/client'
import type { User } from '@/api/types'

/**
 * The session.
 *
 * On load the app does not know whether it is signed in: the access token lives
 * only in memory, so a reload starts with nothing. What survives is the HttpOnly
 * refresh cookie, so the first thing that happens is one refresh attempt — which
 * either restores the session or establishes that there isn't one. Until that
 * settles, `status` is 'restoring' and the app shows neither the login form nor
 * the dashboard, because guessing wrong means a visible flash of the wrong screen.
 */

type Status = 'restoring' | 'signed-in' | 'signed-out'

interface SessionValue {
  status: Status
  user: User | null
  signIn: (email: string, password: string) => Promise<void>
  signInWithPin: (pin: string) => Promise<void>
  signUp: (input: {
    email: string
    password: string
    full_name: string
    account_type?: string
  }) => Promise<void>
  signOut: () => Promise<void>
}

const SessionContext = createContext<SessionValue | null>(null)

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<Status>('restoring')
  const [user, setUser] = useState<User | null>(null)
  const queryClient = useQueryClient()

  const endSession = useCallback(() => {
    setAccessToken(null)
    setUser(null)
    setStatus('signed-out')
    // Cached account data belongs to the person who just left. Clearing it is not
    // housekeeping: without it, the next customer to sign in on this browser
    // would see the previous one's balances until each query refetched.
    queryClient.clear()
  }, [queryClient])

  // The client calls this when a refresh definitively fails, which is the only
  // way a session ends without the customer asking.
  useEffect(() => {
    setSessionEndedListener(endSession)
  }, [endSession])

  useEffect(() => {
    let cancelled = false

    void (async () => {
      const restored = await refreshSession()
      if (cancelled) return

      if (!restored) {
        setStatus('signed-out')
        return
      }
      try {
        const me = await api.fetchMe()
        if (!cancelled) {
          setUser(me.user)
          setStatus('signed-in')
        }
      } catch {
        if (!cancelled) setStatus('signed-out')
      }
    })()

    return () => {
      cancelled = true
    }
  }, [])

  const value = useMemo<SessionValue>(
    () => ({
      status,
      user,
      async signIn(email, password) {
        const session = await api.login(email, password)
        setAccessToken(session.access_token)
        setUser(session.user)
        setStatus('signed-in')
      },
      async signInWithPin(pin) {
        const session = await api.loginPin(pin)
        setAccessToken(session.access_token)
        setUser(session.user)
        setStatus('signed-in')
      },
      async signUp(input) {
        const session = await api.register(input)
        setAccessToken(session.access_token)
        setUser(session.user)
        setStatus('signed-in')
      },
      async signOut() {
        try {
          await api.logout()
        } finally {
          // The local session ends even if the request failed: a customer who
          // pressed "sign out" must not be left signed in because the network
          // was down.
          endSession()
        }
      },
    }),
    [status, user, endSession],
  )

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession(): SessionValue {
  const value = useContext(SessionContext)
  if (!value) throw new Error('useSession must be used inside a SessionProvider')
  return value
}
