import { LogOutIcon, SettingsIcon, ShieldIcon, SparklesIcon } from 'lucide-react'
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
 * The desktop navigation: one ink column down the left, not a band across the top.
 *
 * A rail was tried and removed once, when the argument against it held: three
 * destinations did not earn 16rem. That argument is gone — Asistente, Ajustes and a
 * user menu all live here now too, and this is deliberately the shape corebank's own
 * owner reaches for elsewhere (Dokploy, GitHub) rather than a tab strip. It is still
 * the only dark surface in the interface, still the product's mark rather than a
 * theme — that part carries over unchanged, just turned ninety degrees.
 */
export function Sidebar({ className }: { className?: string }) {
  const { user, signOut } = useSession()
  const { setOpen: setAssistantOpen, docked, setDocked, pendingCount } = useAssistant()
  const location = useLocation()

  const firstName = user?.full_name.split(' ')[0] ?? ''

  return (
    <aside
      className={cn(
        'sticky top-0 z-30 flex h-dvh w-64 shrink-0 flex-col bg-sidebar text-sidebar-foreground',
        className,
      )}
    >
      {/* A plain Link, not a NavLink — see DesktopNav's old rationale, unchanged: this
          is the way home, not a section, so it never claims aria-current. */}
      <Link
        to="/panel"
        className="flex shrink-0 items-center px-5 py-5"
        aria-label="corebank, ir al resumen"
      >
        <Wordmark className="h-[18px] text-sidebar-foreground" />
      </Link>

      <nav
        aria-label="Secciones principales"
        className="flex flex-1 flex-col gap-1 overflow-y-auto px-3"
      >
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
                'relative flex items-center gap-3 rounded px-3 py-2 text-[0.875rem] transition-colors',
                active
                  ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                  : 'text-sidebar-foreground/65 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground',
              )}
            >
              {/* The active section is marked by a copper rule, the same device the
                  statement uses to close a column and the phone's tab bar uses under
                  its icons — turned to the leading edge now that the list runs
                  vertically instead of horizontally. */}
              {active && (
                <span
                  aria-hidden="true"
                  className="absolute inset-y-1.5 left-0 w-[2px] rounded-full bg-sidebar-primary"
                />
              )}
              <item.icon className="size-4 shrink-0" aria-hidden="true" />
              {item.label}
            </NavLink>
          )
        })}
      </nav>

      <div className="shrink-0 space-y-1 border-t border-sidebar-border px-3 py-3">
        {/* Two triggers for two surfaces, revealed by breakpoint like the rest of the
            chrome. Below xl the assistant is a sheet and this opens it; at xl it is a
            column and this docks or undocks it. */}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setAssistantOpen(true)}
          className="relative w-full justify-start gap-3 text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground xl:hidden"
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
            'relative hidden w-full justify-start gap-3 xl:flex',
            docked
              ? 'bg-sidebar-accent text-sidebar-accent-foreground hover:bg-sidebar-accent/80'
              : 'text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground',
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
              className="w-full justify-start gap-3 text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground"
            >
              <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-sidebar-accent">
                <span className="type-figure text-[0.6875rem]">
                  {(firstName.charAt(0) || '?').toUpperCase()}
                </span>
              </span>
              <span className="truncate">{firstName}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" side="top" className="w-56">
            <DropdownMenuLabel className="truncate font-normal text-ink-soft">
              {user?.full_name}
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link to="/ajustes">
                <SettingsIcon aria-hidden="true" />
                Ajustes
              </Link>
            </DropdownMenuItem>
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
    </aside>
  )
}

/** The count of reservations still waiting, on the assistant's trigger. */
function PendingMark({ count }: { count: number }) {
  return (
    <span
      className="flex size-4 items-center justify-center rounded-full bg-hold text-[0.625rem] font-semibold text-ink"
      aria-label={`${count} ${count === 1 ? 'operación pendiente' : 'operaciones pendientes'} de confirmar`}
    >
      {count}
    </span>
  )
}
