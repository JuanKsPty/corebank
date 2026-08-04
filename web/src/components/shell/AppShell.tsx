import { useLocation } from 'react-router-dom'

import { AssistantPanel } from '@/components/assistant/AssistantPanel'
import { AssistantProvider, useAssistant } from '@/components/assistant/AssistantProvider'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { shell } from '@/lib/layout'
import { cn } from '@/lib/utils'
import { BottomTabBar } from './BottomTabBar'
import { DesktopNav } from './DesktopNav'
import { MobileAppBar } from './MobileAppBar'

/**
 * The signed-in chrome.
 *
 * Two experiences, and the split is done in CSS rather than in JavaScript. That is a
 * decision worth stating because the obvious alternative — a viewport hook choosing
 * between a `<MobileShell>` and a `<DesktopShell>` — is wrong here in two specific
 * ways. It would unmount and remount the page on every crossing of the breakpoint,
 * losing whatever a customer had half-typed into the transfer form; and `useIsMobile`
 * reports `false` on its first render, so a phone would paint the desktop layout for a
 * frame before correcting itself.
 *
 * So the chrome — the navbar, the app bar, the tab bar — is rendered once and revealed
 * by breakpoint, while `<main>` and its children exist exactly once at every width. The
 * pieces that are hidden are stateless, so keeping them mounted costs nothing.
 *
 * The assistant is the one thing that genuinely differs: a permanent column from `xl`
 * up, and a sheet rising from the tab bar below that. Its conversation lives in the
 * provider above both, so neither surface owns it and no resize interrupts an answer in
 * flight.
 */
export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <AssistantProvider>
      <div className="flex min-h-dvh flex-col bg-paper">
        {/* Skip link: the first thing a keyboard reaches, and the way past the nav. */}
        <a
          href="#contenido"
          className={cn(
            'sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50',
            'focus:rounded focus:bg-ink focus:px-3 focus:py-2 focus:text-paper',
          )}
        >
          Saltar al contenido
        </a>

        <DesktopNav className="hidden md:block" />
        <MobileAppBar />

        <ShellBody>{children}</ShellBody>

        <Footer />
        <BottomTabBar />
        <AssistantSheet />
      </div>
    </AssistantProvider>
  )
}

function ShellBody({ children }: { children: React.ReactNode }) {
  const location = useLocation()

  return (
    <div className={cn('flex w-full flex-1 gap-8 xl:gap-10', shell)}>
      <main
        id="contenido"
        className={cn(
          'min-w-0 flex-1 py-6 xl:py-8',
          // Room for the tab bar, which is fixed and would otherwise cover the last
          // movement in a list. The inset resolves to zero where there is no home
          // indicator.
          'pb-[calc(5.5rem+env(safe-area-inset-bottom))] md:pb-8',
        )}
        key={location.pathname}
      >
        {children}
      </main>

      <AssistantColumn />
    </div>
  )
}

/**
 * The assistant as a column beside the page, from `xl` up and only while it is docked.
 *
 * Docking is the customer's choice and it is remembered. It starts docked, because a
 * feature nobody finds is worth nothing, but an assistant that cannot be put away is
 * furniture rather than a tool — and on the statement, where the page wants every pixel,
 * putting it away is the reasonable thing to want. Closed, it costs nothing: the
 * conversation lives in the provider, so shutting the column does not end a reply in
 * flight or lose a word of what was said.
 *
 * `sticky` against the navbar's height rather than the top of the viewport, so the
 * transcript does not slide under the chrome.
 */
function AssistantColumn() {
  const { docked, setDocked } = useAssistant()

  if (!docked) return null

  return (
    <aside
      aria-label="Asistente"
      className="sticky top-14 mb-8 mt-6 hidden h-[calc(100dvh-7rem)] w-88 shrink-0 xl:flex 2xl:w-96"
    >
      <AssistantPanel className="card w-full overflow-hidden" onClose={() => setDocked(false)} />
    </aside>
  )
}

/** The assistant as a sheet, below `xl`. */
function AssistantSheet() {
  const { open, setOpen } = useAssistant()

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetContent
        side="bottom"
        // Stops short of the top so the balance stays visible behind it: watching the
        // held segment appear is the point of confirming a movement here.
        className="h-[85dvh] gap-0 rounded-t-[10px] p-0 xl:hidden"
      >
        <SheetHeader className="sr-only">
          <SheetTitle>Asistente</SheetTitle>
        </SheetHeader>
        <AssistantPanel className="h-full rounded-t-[10px]" />
      </SheetContent>
    </Sheet>
  )
}

function Footer() {
  return (
    // Stated on the desktop only: on a phone the tab bar occupies this edge, and a note
    // about double-entry bookkeeping is not worth a row of thumb space.
    <footer className="hidden border-t border-rule md:block">
      <div
        className={cn(
          'flex w-full flex-wrap items-center gap-x-6 gap-y-1 py-4 text-[0.75rem] text-ink-faint',
          shell,
        )}
      >
        <p>
          Saldos y movimientos sobre un ledger de doble entrada. Los importes se manejan
          en centavos enteros.
        </p>
        <p className="type-figure ml-auto">USD</p>
      </div>
    </footer>
  )
}
