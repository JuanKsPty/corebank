import { Link } from 'react-router-dom'

import { Wordmark } from '@/components/Wordmark'
import { cn } from '@/lib/utils'

/**
 * AuthLayout is the frame around signing in and signing up.
 *
 * One card, centred, and nothing else. This used to be two panels — the form beside an
 * ink panel restating what the bank is — which is a reasonable thing for a landing page
 * to do and the wrong thing here. Somebody who reached `/entrar` has already decided;
 * they came to type a password, and half the screen arguing the product's merits is an
 * obstacle between them and the only control that matters. The argument lives on `/`,
 * where it belongs and where it is demonstrated rather than asserted.
 *
 * The wordmark stays, because a form that asks for a password should say whose it is.
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
    <div className="flex min-h-dvh flex-col items-center justify-center bg-paper px-4 py-10">
      <div className="w-full max-w-[26rem]">
        <Link
          to="/"
          aria-label="corebank, ir al inicio"
          className="mx-auto mb-8 block w-fit"
        >
          <Wordmark className="h-[18px] text-ink" />
        </Link>

        <div className="card bg-paper-raised p-6 sm:p-7">
          <h1 className="type-display text-[1.5rem]">{title}</h1>
          <p className="mt-1.5 text-[0.9375rem] text-ink-soft">{subtitle}</p>

          <div className="mt-6">{children}</div>

          <div className="mt-6 border-t border-rule pt-5 text-[0.875rem] text-ink-soft">
            {footer}
          </div>
        </div>
      </div>
    </div>
  )
}

export function AuthLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link
      to={to}
      className={cn(
        'font-medium text-copper underline decoration-copper/35 underline-offset-2',
        'hover:decoration-copper',
      )}
    >
      {children}
    </Link>
  )
}
