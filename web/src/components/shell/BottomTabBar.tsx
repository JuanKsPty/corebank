import { NavLink } from 'react-router-dom'

import { useAssistant } from '@/components/assistant/AssistantProvider'
import { NAV } from '@/lib/nav'
import { cn } from '@/lib/utils'

/**
 * The phone's navigation: a bar fixed to the bottom of the screen.
 *
 * Built by hand because shadcn has no bottom-navigation component — the request for
 * one is open and unanswered — and because the raised centre slot is the part that
 * matters and no generic tab component would give it.
 *
 * The assistant occupies that raised slot. On every other bank the prominent middle
 * button is "transact"; here the assistant *is* how you transact, and it is the one
 * thing this product does that its neighbours cannot, so it gets the position a thumb
 * reaches first. It is also the only slot that is not a route: it opens over whatever
 * you were reading, which is deliberate — the balance stays visible behind the sheet
 * while a reservation is confirmed, and watching the held segment appear is the whole
 * point of the feature.
 */
export function BottomTabBar() {
  const { setOpen, pendingCount } = useAssistant()

  return (
    <nav
      // Named distinctly from the navbar's: both are in the DOM at every width and
      // only one is visible, but a screen reader reads the tree rather than the CSS,
      // and two navigations called "Secciones" is a landmark list nobody can use.
      aria-label="Secciones, barra inferior"
      className={cn(
        'fixed inset-x-0 bottom-0 z-40 md:hidden',
        'border-t border-rule bg-paper-raised/95 backdrop-blur-sm',
        // The inset keeps the row clear of the home indicator on a phone that has
        // one, and resolves to zero on every device that does not.
        'pb-[env(safe-area-inset-bottom)]',
      )}
    >
      <ul className="grid grid-cols-6">
        {NAV.map((item) =>
          item.assistant ? (
            <li key={item.to} className="relative flex justify-center">
              <button
                type="button"
                onClick={() => setOpen(true)}
                aria-label="Abrir el asistente"
                className={cn(
                  'absolute -top-5 flex size-14 flex-col items-center justify-center rounded-full',
                  // A plain black shadow rather than a tinted one: elevation reads
                  // the same regardless of which edition of the palette is active.
                  'bg-copper text-primary-foreground shadow-[0_2px_10px_rgba(0,0,0,0.22)]',
                  // A ring in the page colour, so the button reads as sitting above
                  // the bar rather than punched through it.
                  'ring-4 ring-paper',
                  'transition-transform active:scale-95',
                )}
              >
                <item.icon className="size-6" aria-hidden="true" />
                {pendingCount > 0 && (
                  <span
                    className="absolute -right-0.5 -top-0.5 flex size-5 items-center justify-center rounded-full bg-hold text-[0.625rem] font-semibold text-ink ring-2 ring-paper"
                    aria-label={`${pendingCount} operación pendiente de confirmar`}
                  >
                    {pendingCount}
                  </span>
                )}
              </button>
              {/* The label sits in the bar itself, aligned with its neighbours, while
                  the button floats above it. */}
              <span className="pointer-events-none mt-auto pb-1.5 pt-11 text-[0.625rem] text-ink-faint">
                {item.short}
              </span>
            </li>
          ) : (
            <li key={item.to}>
              <NavLink
                to={item.to}
                end={item.to === '/panel'}
                className={({ isActive }) =>
                  cn(
                    'flex flex-col items-center gap-1 px-1 pb-1.5 pt-2.5 text-[0.625rem]',
                    isActive ? 'text-copper' : 'text-ink-faint',
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    <item.icon
                      className={cn('size-[1.375rem]', isActive && 'text-copper')}
                      aria-hidden="true"
                    />
                    <span>{item.short}</span>
                    {/* The active section is marked by a copper rule, the same device
                        the ledger uses to close a column and the rail uses above. */}
                    <span
                      aria-hidden="true"
                      className={cn(
                        'h-[2px] w-5 rounded-full',
                        isActive ? 'bg-copper' : 'bg-transparent',
                      )}
                    />
                  </>
                )}
              </NavLink>
            </li>
          ),
        )}
      </ul>
    </nav>
  )
}
