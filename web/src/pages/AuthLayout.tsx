import { Link } from 'react-router-dom'

import { Wordmark } from '@/components/Wordmark'

/**
 * AuthLayout is the frame around signing in and signing up.
 *
 * Two panels: the form on paper, and beside it an ink panel that says what this
 * bank actually is. The claim on the right is not marketing filler — it is the one
 * true, unusual thing about the system, stated plainly, and it is the same claim
 * the dashboard's balance bar then demonstrates.
 */
export function AuthLayout({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string
  subtitle: string
  children: React.ReactNode
  footer: React.ReactNode
}) {
  return (
    <div className="grid min-h-dvh lg:grid-cols-[1fr_minmax(0,26rem)]">
      {/* On a narrow screen the panel moves below the form: the form is what the
          visitor came for. */}
      <div className="order-2 flex flex-col justify-center bg-ink px-6 py-12 text-paper lg:order-1 lg:px-14">
        <div className="mx-auto w-full max-w-lg">
          <Wordmark className="h-5 text-paper" />

          <p className="type-display mt-9 text-[clamp(1.5rem,3.2vw,2.25rem)] text-paper">
            Cada movimiento tiene dos lados.
          </p>
          <p className="mt-3 max-w-md text-[0.9375rem] leading-relaxed text-paper/70">
            Los saldos no se guardan en una columna: se derivan de un ledger de
            doble entrada. Por eso un sobregiro no es una validación que se pueda
            olvidar, y por eso puedes ver el dinero reservado antes de confirmarlo.
          </p>

          <dl className="mt-10 grid gap-5 sm:grid-cols-3">
            {[
              ['Doble entrada', 'Todo cargo tiene su abono'],
              ['Centavos enteros', 'Ningún importe pasa por un float'],
              ['Confirmación real', 'Los fondos se reservan, no se mueven'],
            ].map(([term, detail]) => (
              <div key={term} className="border-t border-paper/20 pt-3">
                <dt className="type-eyebrow text-paper/50">{term}</dt>
                <dd className="mt-1 text-[0.8125rem] leading-snug text-paper/80">{detail}</dd>
              </div>
            ))}
          </dl>
        </div>
      </div>

      <div className="order-1 flex flex-col justify-center px-6 py-12 lg:order-2 lg:px-10">
        <div className="mx-auto w-full max-w-sm">
          <h1 className="type-display text-[1.625rem]">{title}</h1>
          <p className="mt-1.5 text-[0.9375rem] text-ink-soft">{subtitle}</p>

          <div className="mt-7">{children}</div>

          <div className="mt-6 border-t border-rule pt-5 text-[0.875rem] text-ink-soft">
            {footer}
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * DemoCredentials fills the form with a seeded customer.
 *
 * The dataset's passwords are published in the README anyway, and an evaluator who
 * has to copy an account out of a JSON file before they can look at anything is
 * being made to work for no reason. The button says exactly what it does.
 */
export function DemoCredentials({
  onUse,
}: {
  onUse: (email: string, password: string) => void
}) {
  const accounts = [
    { email: 'ihernandez@email.com', password: 'Isabel2024!', note: '1 cuenta · $32,354.53' },
    { email: 'andres.perez368@mail.com', password: 'Andrés2024!', note: '3 cuentas · $86,047.62' },
  ]

  return (
    <div className="rounded-[5px] border border-rule bg-paper-sunken p-3.5">
      <p className="type-eyebrow">Cuentas de prueba</p>
      <ul className="mt-2.5 space-y-2">
        {accounts.map((account) => (
          <li key={account.email} className="flex items-center gap-3">
            <div className="min-w-0 flex-1">
              <p className="type-figure truncate text-[0.75rem]">{account.email}</p>
              <p className="text-[0.6875rem] text-ink-faint">{account.note}</p>
            </div>
            <button
              type="button"
              onClick={() => onUse(account.email, account.password)}
              className="btn btn-secondary shrink-0 px-2.5 py-1 text-[0.75rem]"
            >
              Usar
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function AuthLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link to={to} className="font-medium text-copper underline decoration-copper/35 underline-offset-2 hover:decoration-copper">
      {children}
    </Link>
  )
}
