import { Navigate, Route, Routes, useLocation } from 'react-router-dom'

import { Shell } from '@/components/Shell'
import { Wordmark } from '@/components/Wordmark'
import { useSession } from '@/lib/session'
import { AccountPage } from '@/pages/AccountPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { HistoryPage } from '@/pages/HistoryPage'
import { MovePage } from '@/pages/MovePage'
import { SignInPage } from '@/pages/SignInPage'
import { SignUpPage } from '@/pages/SignUpPage'

export function App() {
  const { status } = useSession()

  // While the refresh cookie is being exchanged the app does not yet know whether
  // it is signed in. Rendering either the login form or the dashboard now would
  // flash the wrong screen, so it renders neither.
  if (status === 'restoring') {
    return <RestoringSession />
  }

  if (status === 'signed-out') {
    return (
      <Routes>
        <Route path="/entrar" element={<SignInPage />} />
        <Route path="/registro" element={<SignUpPage />} />
        <Route path="*" element={<RedirectToSignIn />} />
      </Routes>
    )
  }

  return (
    <Shell>
      <Routes>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/mover" element={<MovePage />} />
        <Route path="/historial" element={<HistoryPage />} />
        <Route path="/cuentas/:number" element={<AccountPage />} />
        {/* Somebody who signed in from the login page lands back on the dashboard
            rather than on a form they no longer need. */}
        <Route path="/entrar" element={<Navigate to="/" replace />} />
        <Route path="/registro" element={<Navigate to="/" replace />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </Shell>
  )
}

/**
 * RedirectToSignIn sends an unauthenticated visitor to the login, remembering
 * where they were headed so a session that expires mid-navigation returns them
 * there instead of dumping them on the dashboard.
 */
function RedirectToSignIn() {
  const location = useLocation()
  return <Navigate to="/entrar" replace state={{ from: location.pathname + location.search }} />
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
    <div className="mx-auto max-w-lg px-5 py-20 text-center">
      <p className="type-eyebrow">Error 404</p>
      <h1 className="type-display mt-2 text-2xl">Esta página no existe</h1>
      <p className="mt-2 text-ink-soft">
        Revisa la dirección, o vuelve al resumen de tus cuentas.
      </p>
      <a href="/" className="btn btn-secondary mt-5">
        Ir al resumen
      </a>
    </div>
  )
}
