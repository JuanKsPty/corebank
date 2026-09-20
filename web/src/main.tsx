import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

import { App } from './App'
import { ApiError } from './api/client'
import { TooltipProvider } from './components/ui/tooltip'
import { SessionProvider } from './lib/session'
import { ThemeProvider } from './lib/theme'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Balances are the point of this application, so nothing is served from
      // cache for long: a figure that is a minute old is a wrong number about
      // somebody's money.
      staleTime: 10_000,
      refetchOnWindowFocus: true,
      retry(failureCount, error) {
        // A 4xx will not become a 2xx by asking again, and retrying a 401 would
        // race the token refresh the client is already doing.
        if (error instanceof ApiError && error.status < 500) return false
        return failureCount < 2
      },
    },
    mutations: {
      // A movement is never retried automatically. The customer decides whether to
      // try again, and the idempotency key is what makes that safe.
      retry: false,
    },
  },
})

const root = document.getElementById('root')
if (!root) throw new Error('#root is missing from the document')

createRoot(root).render(
  <StrictMode>
    {/* Above the router and the session: a signed-out visitor on the landing
        page or the sign-in form gets their preferred appearance too, not
        only somebody already inside the app. */}
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <SessionProvider>
            {/* Required by every shadcn component that can carry a tooltip. A short
                delay, so a pointer passing through on its way elsewhere does not
                trigger one that fires instantly and reads as noise. */}
            <TooltipProvider delayDuration={300}>
              <App />
            </TooltipProvider>
          </SessionProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
