import { Navigate, Route, Routes, useLocation } from 'react-router-dom'

import { AppShell } from '@/components/shell/AppShell'
import { Wordmark } from '@/components/Wordmark'
import { Button } from '@/components/ui/button'
import { useSession } from '@/lib/session'
import { AccountPage } from '@/pages/AccountPage'
import { AccountsPage } from '@/pages/AccountsPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { HistoryPage } from '@/pages/HistoryPage'
import { ImportPage } from '@/pages/ImportPage'
import { LandingPage } from '@/pages/LandingPage'
import { MovePage } from '@/pages/MovePage'
import { SecurityPage } from '@/pages/SecurityPage'
import { SignInPage } from '@/pages/SignInPage'
import { SignUpPage } from '@/pages/SignUpPage'

export function App() {
  const { status } = useSession()

  // While the refresh cookie is being exchanged the app does not yet know whether it
  // is signed in. Rendering either the login form or the dashboard now would flash the
  // wrong screen, so it renders neither.
  if (status === 'restoring') {
    return <RestoringSession />
  }

  if (status === 'signed-out') {
    return (
      <Routes>
        {/* Three distinct public routes. The landing is a page in its own right, not
            the login wearing a headline, and each of the two forms has its own URL so
            either can be linked to or bookmarked. */}
        <Route path="/" element={<LandingPage />} />
        <Route path="/entrar" element={<SignInPage />} />
        <Route path="/registro" element={<SignUpPage />} />
        {/* A protected address reached without a session goes to the login, which then
            returns you to it. Anything else was never a page. */}
        <Route path="/panel" element={<RedirectToSignIn />} />
        <Route path="/cuentas" element={<RedirectToSignIn />} />
        <Route path="/mover" element={<RedirectToSignIn />} />
        <Route path="/historial" element={<RedirectToSignIn />} />
        <Route path="/cuentas/:number" element={<RedirectToSignIn />} />
        <Route path="/seguridad" element={<RedirectToSignIn />} />
        <Route path="/importar" element={<RedirectToSignIn />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    )
  }

  return (
    <AppShell>
      <Routes>
        {/* The summary lives at /panel rather than at /, leaving the root free for a
            public page. A route that meant "the marketing site" or "your accounts"
            depending on a cookie could not be linked to. */}
        <Route path="/panel" element={<DashboardPage />} />
        <Route path="/cuentas" element={<AccountsPage />} />
        <Route path="/mover" element={<MovePage />} />
        <Route path="/historial" element={<HistoryPage />} />
        <Route path="/cuentas/:number" element={<AccountPage />} />
        <Route path="/seguridad" element={<SecurityPage />} />
        <Route path="/importar" element={<ImportPage />} />
        <Route path="/" element={<Navigate to="/panel" replace />} />
        {/* Somebody who signed in from the login page lands on their accounts rather
            than on a form they no longer need. */}
        <Route path="/entrar" element={<Navigate to="/panel" replace />} />
        <Route path="/registro" element={<Navigate to="/panel" replace />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </AppShell>
  )
}

/**
 * RedirectToSignIn sends an unauthenticated visitor to the login, remembering where
 * they were headed so a session that expires mid-navigation returns them there instead
 * of dumping them on the dashboard.
 */
function RedirectToSignIn() {
  const location = useLocation()
  return (
    <Navigate to="/entrar" replace state={{ from: location.pathname + location.search }} />
  )
}

function RestoringSession() {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-paper">
      <div className="flex flex-col items-center gap-3">
        <Wordmark className="h-4 text-ink" />
        {/* Announced rather than only drawn, so the wait is not silent to a screen
            reader. */}
        <p className="type-eyebrow" role="status">
          Restaurando sesión
        </p>
      </div>
    </div>
  )
}

function NotFound() {
  return (
    <div className="mx-auto max-w-lg py-20 text-center">
      <p className="type-eyebrow">Error 404</p>
      <h1 className="type-display mt-2 text-2xl">Esta página no existe</h1>
      <p className="mt-2 text-ink-soft">
        Revisa la dirección, o vuelve al resumen de tus cuentas.
      </p>
      <Button asChild variant="outline" className="mt-5">
        <a href="/panel">Ir al resumen</a>
      </Button>
    </div>
  )
}
