import { useState } from 'react'
import { Link } from 'react-router-dom'

import type { Amount } from '@/api/types'
import { BalanceComposition, Figure } from '@/components/primitives'
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
        <div className="grid items-center gap-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,30rem)] lg:gap-16">
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

          <HoldDemonstration />
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

/** Builds the wire shape the balance components expect, from cents. */
function amount(cents: number): Amount {
  return { cents, formatted: (cents / 100).toFixed(2), currency: 'USD' }
}

const POSTED = amount(3_287_508)
const RESERVED = amount(25_000)

/**
 * The signature element, driven by a button.
 *
 * The same `BalanceComposition` the dashboard uses, with the same held-segment
 * animation, so what a visitor sees here is not a mock-up of the product but the
 * product's one distinctive component running on sample figures.
 */
function HoldDemonstration() {
  const [reserved, setReserved] = useState(false)

  const held = reserved ? RESERVED : amount(0)
  const available = amount(POSTED.cents - held.cents)

  return (
    <section
      aria-labelledby="demo"
      className="card overflow-hidden bg-paper-raised p-5 sm:p-7 lg:sticky lg:top-8"
    >
      <p id="demo" className="type-eyebrow">
        Disponible
      </p>
      <p className="mt-1.5">
        <Figure amount={available} size="display" className="type-display" />
      </p>

      <div className="mt-6">
        <BalanceComposition posted={POSTED} held={held} available={available} />
      </div>

      <div className="mt-7 border-t border-rule pt-5">
        <p className="text-[0.875rem] leading-relaxed text-ink-soft">
          {/* What the number does, not what the architecture is. The version before
              this one ended by comparing the product to a bank that stores its balance
              in a column — arguing with an imaginary competitor instead of saying what
              the visitor is looking at. */}
          {reserved ? (
            <>
              Hay <strong className="font-semibold text-hold">$250.00</strong> retenidos.
              Salieron de lo que puedes gastar y todavía no han llegado a ninguna parte:
              esperan a que confirmes, y hasta entonces nadie puede gastarlos.
            </>
          ) : (
            <>
              Pídele al asistente que mueva dinero y verás pasar esto: los fondos se
              reservan primero y solo se mueven cuando confirmas. Pruébalo.
            </>
          )}
        </p>

        <Button
          onClick={() => setReserved((value) => !value)}
          variant={reserved ? 'outline' : 'default'}
          className={cn('mt-4', !reserved && 'bg-ink text-paper hover:bg-ink/90')}
        >
          {reserved ? 'Liberar la reserva' : 'Reservar $250'}
        </Button>
      </div>
    </section>
  )
}
