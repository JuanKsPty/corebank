import { NavLink, useLocation } from 'react-router-dom'

import { Wordmark } from '@/components/Wordmark'
import { useSession } from '@/lib/session'
import { cx } from './primitives'

const NAV = [
  { to: '/', label: 'Resumen' },
  { to: '/mover', label: 'Mover dinero' },
  { to: '/historial', label: 'Historial' },
]

/**
 * Shell is the signed-in chrome: the ink bar across the top and the page under it.
 *
 * The bar is the only dark surface in the interface. Keeping it to one band means
 * the ink reads as the product's mark rather than as a theme, and everything below
 * stays on paper where the numbers are easiest to read.
 */
export function Shell({ children }: { children: React.ReactNode }) {
  const { user, signOut } = useSession()
  const location = useLocation()

  return (
    <div className="flex min-h-dvh flex-col">
      {/* Skip link: the first thing a keyboard reaches, and the way past the nav. */}
      <a
        href="#contenido"
        className={cx(
          'sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50',
          'focus:rounded focus:bg-ink focus:px-3 focus:py-2 focus:text-paper',
        )}
      >
        Saltar al contenido
      </a>

      <header className="bg-ink text-paper">
        <div className="mx-auto flex max-w-[1180px] items-center gap-6 px-5 py-3.5">
          <NavLink to="/" className="shrink-0" aria-label="corebank, ir al resumen">
            <Wordmark className="h-[18px] text-paper" />
          </NavLink>

          <nav aria-label="Secciones" className="hidden items-center gap-1 sm:flex">
            {NAV.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) =>
                  cx(
                    'relative rounded px-3 py-1.5 text-[0.875rem] transition-colors',
                    isActive ? 'text-paper' : 'text-paper/65 hover:text-paper',
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    {item.label}
                    {/* The active section is marked by a copper rule under it, the
                        same device the ledger uses to close a column. */}
                    {isActive && (
                      <span
                        aria-hidden="true"
                        className="absolute inset-x-3 -bottom-[2px] h-[2px] rounded-full bg-copper"
                      />
                    )}
                  </>
                )}
              </NavLink>
            ))}
          </nav>

          <div className="ml-auto flex items-center gap-3">
            <span className="hidden text-[0.8125rem] text-paper/70 md:inline">
              {user?.full_name}
            </span>
            <button
              type="button"
              onClick={() => void signOut()}
              className="btn border-paper/25 text-[0.8125rem] text-paper/80 hover:bg-paper/10 hover:text-paper"
            >
              Salir
            </button>
          </div>
        </div>

        {/* On a narrow screen the sections move below the bar rather than into a
            hamburger: there are three of them, and hiding three links behind a
            menu costs a tap for nothing. */}
        <nav
          aria-label="Secciones"
          className="flex gap-1 overflow-x-auto border-t border-paper/10 px-5 pb-2 pt-1.5 sm:hidden"
        >
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) =>
                cx(
                  'whitespace-nowrap rounded px-3 py-1.5 text-[0.875rem]',
                  isActive ? 'bg-paper/12 text-paper' : 'text-paper/65',
                )
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </header>

      <main id="contenido" className="mx-auto w-full max-w-[1180px] flex-1 px-5 py-7" key={location.pathname}>
        {children}
      </main>

      <footer className="border-t border-rule">
        <div className="mx-auto flex max-w-[1180px] flex-wrap items-center gap-x-6 gap-y-1 px-5 py-4 text-[0.75rem] text-ink-faint">
          <p>
            Saldos y movimientos sobre un ledger de doble entrada. Los importes se
            manejan en centavos enteros.
          </p>
          <p className="type-figure ml-auto">USD</p>
        </div>
      </footer>
    </div>
  )
}
