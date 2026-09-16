import { ChevronLeftIcon, LogOutIcon, ShieldIcon } from 'lucide-react'
import { Link, useLocation, useNavigate } from 'react-router-dom'

import { Wordmark } from '@/components/Wordmark'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { gutter } from '@/lib/layout'
import { useSession } from '@/lib/session'
import { cn } from '@/lib/utils'

/**
 * The phone's top bar, which changes shape depending on where you are.
 *
 * On the summary it is a greeting: the mark, the time of day and your name, because
 * that screen is the front door. On any screen you navigated *into* it becomes an
 * app bar — a back chevron and the title of the thing you are looking at, centred —
 * because from there the useful affordance is getting back out, not being greeted
 * again. Two shapes, one element, which is what a phone expects and what a single
 * unchanging header cannot give.
 */
export function MobileAppBar() {
  const { user, signOut } = useSession()
  const location = useLocation()
  const navigate = useNavigate()

  const detail = detailTitle(location.pathname)
  const firstName = user?.full_name.split(' ')[0] ?? ''

  return (
    <header className={cn('sticky top-0 z-30 bg-ink text-paper md:hidden')}>
      {detail ? (
        <div
          className={cn('grid grid-cols-[2.5rem_1fr_2.5rem] items-center py-2.5', gutter)}
        >
          <button
            type="button"
            onClick={() => void navigate(-1)}
            aria-label="Volver"
            className="-ml-2 flex size-10 items-center justify-center rounded-full text-paper/80 active:bg-paper/10"
          >
            <ChevronLeftIcon className="size-6" aria-hidden="true" />
          </button>
          <h1 className="truncate text-center text-[0.9375rem] font-medium">{detail}</h1>
          <span />
        </div>
      ) : (
        <div className={cn('flex items-center justify-between py-3', gutter)}>
          <Link to="/panel" aria-label="corebank, ir al resumen">
            <Wordmark className="h-[18px] text-paper" />
          </Link>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-9 rounded-full bg-paper/10 text-paper hover:bg-paper/20 hover:text-paper"
                aria-label="Tu cuenta"
              >
                <span className="type-figure text-[0.8125rem]">
                  {(firstName.charAt(0) || '?').toUpperCase()}
                </span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56">
              <DropdownMenuLabel className="truncate font-normal text-ink-soft">
                {user?.full_name}
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem asChild>
                <Link to="/seguridad">
                  <ShieldIcon aria-hidden="true" />
                  Seguridad
                </Link>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => void signOut()}>
                <LogOutIcon aria-hidden="true" />
                Salir
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      )}

      {!detail && (
        <p className={cn('pb-4 text-[1.375rem] text-paper', gutter)}>
          <span className="type-display">{greeting()}</span>
          {firstName && <span className="type-display font-bold">, {firstName}</span>}
        </p>
      )}
    </header>
  )
}

/**
 * The title for a screen you navigated into, or null on the front door.
 *
 * Kept here rather than threaded down from each page: a page should not have to know
 * it is being rendered inside a phone's chrome, and the routes are few enough that a
 * table is honest.
 */
function detailTitle(pathname: string): string | null {
  if (pathname === '/panel') return null
  if (pathname.startsWith('/mover')) return 'Mover dinero'
  if (pathname.startsWith('/historial')) return 'Historial'
  if (pathname.startsWith('/cuentas/')) return 'Detalle de cuenta'
  if (pathname === '/cuentas') return 'Tus cuentas'
  if (pathname === '/seguridad') return 'Seguridad'
  if (pathname.startsWith('/tarjetas/')) return 'Tarjeta'
  return null
}

/** The Spanish greeting for the hour it currently is. */
function greeting(): string {
  const hour = new Date().getHours()
  if (hour < 12) return 'Buenos días'
  if (hour < 19) return 'Buenas tardes'
  return 'Buenas noches'
}
