import { Link } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Wordmark } from '@/components/Wordmark'
import { gutter } from '@/lib/layout'
import { cn } from '@/lib/utils'

/**
 * The public front door: what the app is for, and the two ways in.
 *
 * The copy is about what somebody can do here, not about how it is built — nobody
 * picks a finance app for its storage model. The stack gets one quiet line in the
 * footer, as evidence rather than as the pitch.
 */
export function LandingPage() {
  return (
    <div className="flex min-h-dvh flex-col bg-paper">
      {/* One link, not two. The header used to repeat both of the hero's buttons a few
          hundred pixels above them, which is four calls to action on a page with one
          thing to do. What is left is the other intent: somebody who already has an
          account and wants past all of this. */}
      <header className={cn('flex items-center justify-between py-4', gutter)}>
        <Wordmark className="h-[18px] text-ink" />
        <Button asChild variant="ghost" size="sm">
          <Link to="/entrar">Entrar</Link>
        </Button>
      </header>

      <main className={cn('flex flex-1 flex-col justify-center py-10 md:py-14', gutter)}>
        <div>
          <div>
            <h1 className="type-display max-w-[19ch] text-[clamp(2rem,5.2vw,3.5rem)]">
              Tus cuentas, tarjetas e inversiones{' '}
              <span className="text-ink-faint">en un solo lugar.</span>
            </h1>
            <p className="mt-5 max-w-[46ch] text-[1.0625rem] leading-relaxed text-ink-soft">
              Importa los estados de cuenta de tu banco, sincroniza tu cuenta de IBKR y
              pregúntale al asistente, en español, cuánto gastaste y en qué.
            </p>

            <div className="mt-8 flex flex-wrap items-center gap-3">
              <Button
                asChild
                size="lg"
                className="bg-copper text-primary-foreground hover:bg-copper/90"
              >
                <Link to="/entrar">Entrar</Link>
              </Button>
              <Button asChild variant="outline" size="lg">
                <Link to="/registro">Crear una cuenta</Link>
              </Button>
            </div>
          </div>
        </div>
      </main>

      <footer className={cn('border-t border-rule py-5', gutter)}>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[0.75rem]">
          <p className="text-ink-faint">Go · TigerBeetle · React · asistente por MCP</p>
        </div>
      </footer>
    </div>
  )
}
