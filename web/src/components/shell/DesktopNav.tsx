import { LogOutIcon, ShieldIcon, SparklesIcon, UploadIcon } from 'lucide-react'
import { Link, NavLink, useLocation } from 'react-router-dom'

import { useAssistant } from '@/components/assistant/AssistantProvider'
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
import { ROUTES } from '@/lib/nav'
import { useSession } from '@/lib/session'
import { cn } from '@/lib/utils'

/**
 * The desktop navigation: one ink band across the top.
 *
 * A rail was tried here and removed. A vertical rail earns its 16rem when an
 * application has enough sections to need grouping, or deep enough hierarchy to need a
 * tree; this one has three destinations. What the rail actually did was take a quarter
 * of the width away from the page and spend it on three words, which then left the
 * content stretched thin across whatever remained.
 *
 * So the chrome goes back to a single band, and it is the only dark surface in the
 * interface — which is the point of keeping it to one band. The ink reads as the
 * product's mark rather than as a theme, and everything below it stays on paper where
 * the numbers are easiest to read.
 */
export function DesktopNav({ className }: { className?: string }) {
  const { user, signOut } = useSession()
  const { setOpen: setAssistantOpen, docked, setDocked, pendingCount } = useAssistant()
  const location = useLocation()

  const firstName = user?.full_name.split(' ')[0] ?? ''

  return (
    <header className={cn('sticky top-0 z-30 bg-ink text-paper', className)}>
      <div className="mx-auto flex w-full max-w-[88rem] items-center gap-8 px-6 py-3 xl:px-8">
        {/* A plain Link, not a NavLink. NavLink marks itself aria-current="page" when
            its route is active, which on /panel made a screen reader announce the
            wordmark as the current page alongside the nav item that actually is one.
            This is the way home, not a section. */}
        <Link to="/panel" className="shrink-0" aria-label="corebank, ir al resumen">
          <Wordmark className="h-[18px] text-paper" />
        </Link>

        <nav aria-label="Secciones principales" className="flex items-center gap-1">
          {ROUTES.map((item) => {
            const active =
              item.to === '/panel'
                ? location.pathname === '/panel'
                : location.pathname.startsWith(item.to)

            return (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === '/panel'}
                className={cn(
                  'relative flex items-center gap-2 rounded px-3 py-1.5 text-[0.875rem] transition-colors',
                  active ? 'text-paper' : 'text-paper/65 hover:text-paper',
                )}
              >
                <item.icon className="size-4" aria-hidden="true" />
                {item.label}
                {/* The active section is marked by a copper rule under it, the same
                    device the statement uses to close a column and the phone's tab bar
                    uses under its icons. */}
                {active && (
                  <span
                    aria-hidden="true"
                    className="absolute inset-x-3 -bottom-[11px] h-[2px] rounded-full bg-copper"
                  />
                )}
              </NavLink>
            )
          })}
        </nav>

        <div className="ml-auto flex items-center gap-2">
          {/* Two triggers for two surfaces, revealed by breakpoint like the rest of the
              chrome. Below xl the assistant is a sheet and this opens it; at xl it is a
              column and this docks or undocks it. One button switching behaviour on a
              media query would have to read the viewport in JavaScript to know which
              thing it currently is. */}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setAssistantOpen(true)}
            className="relative gap-2 text-paper/80 hover:bg-paper/10 hover:text-paper xl:hidden"
          >
            <SparklesIcon className="size-4" aria-hidden="true" />
            Asistente
            {pendingCount > 0 && <PendingMark count={pendingCount} />}
          </Button>

          <Button
            variant="ghost"
            size="sm"
            onClick={() => setDocked(!docked)}
            aria-pressed={docked}
            className={cn(
              'relative hidden gap-2 xl:inline-flex',
              docked
                ? 'bg-paper/12 text-paper hover:bg-paper/20 hover:text-paper'
                : 'text-paper/80 hover:bg-paper/10 hover:text-paper',
            )}
          >
            <SparklesIcon className="size-4" aria-hidden="true" />
            Asistente
            {pendingCount > 0 && <PendingMark count={pendingCount} />}
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                className="gap-2 text-paper/80 hover:bg-paper/10 hover:text-paper"
              >
                <span className="flex size-6 items-center justify-center rounded-full bg-paper/12">
                  <span className="type-figure text-[0.6875rem]">
                    {(firstName.charAt(0) || '?').toUpperCase()}
                  </span>
                </span>
                <span className="hidden lg:inline">{firstName}</span>
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
              <DropdownMenuItem asChild>
                <Link to="/importar">
                  <UploadIcon aria-hidden="true" />
                  Importar
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
      </div>
    </header>
  )
}

/** The count of reservations still waiting, on the assistant's trigger. */
function PendingMark({ count }: { count: number }) {
  return (
    <span
      className="ml-0.5 flex size-4 items-center justify-center rounded-full bg-hold text-[0.625rem] font-semibold text-ink"
      aria-label={`${count} ${count === 1 ? 'operación pendiente' : 'operaciones pendientes'} de confirmar`}
    >
      {count}
    </span>
  )
}
