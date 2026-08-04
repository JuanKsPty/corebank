import { useState } from 'react'
import { Link } from 'react-router-dom'

import type { Amount } from '@/api/types'
import { BalanceComposition, Figure } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Wordmark } from '@/components/Wordmark'
import { gutter } from '@/lib/layout'
import { cn } from '@/lib/utils'

/**
 * The public front door.
 *
 * The root used to redirect: to the login if you were signed out, to your accounts if
 * you were not. That made `/` a URL nobody could link to, and left the product with no
 * page that says what it is.
 *
 * What it says is one thing, and it demonstrates it rather than claiming it. Every bank
 * shows a balance. This one is built on a double-entry ledger that can hold funds
 * without moving them, so a balance here is three numbers instead of one — and the
 * demonstration below is the real component from the real application, driven by a
 * button. Reserving is what happens when you ask the assistant to move money: the funds
 * leave the spendable total, arrive nowhere, and wait for you. Watching that happen is
 * more convincing than a paragraph about ledgers, and it is the same element that will
 * be on screen a minute later once somebody signs in.
 */
export function LandingPage() {
  return (
    <div className="flex min-h-dvh flex-col bg-paper">
      <header className={cn('flex items-center justify-between py-4', gutter)}>
        <Wordmark className="h-[18px] text-ink" />
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="sm">
            <Link to="/entrar">Entrar</Link>
          </Button>
          <Button asChild size="sm" className="bg-copper text-white hover:bg-copper/90">
            <Link to="/registro">Crear cuenta</Link>
          </Button>
        </div>
      </header>

      <main className={cn('flex flex-1 flex-col justify-center py-10 md:py-14', gutter)}>
        <div className="grid items-center gap-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,30rem)] lg:gap-16">
          <div>
            <h1 className="type-display max-w-[22ch] text-[clamp(2rem,5.2vw,3.5rem)]">
              Cada movimiento tiene dos lados.
            </h1>
            <p className="mt-5 max-w-[46ch] text-[1.0625rem] leading-relaxed text-ink-soft">
              Los saldos no se guardan en una columna: se derivan de un ledger de doble
              entrada. Por eso un sobregiro no es una validación que se pueda olvidar, y por
              eso puedes ver el dinero reservado antes de confirmarlo.
            </p>

            <div className="mt-8 flex flex-wrap items-center gap-3">
              <Button asChild size="lg" className="bg-copper text-white hover:bg-copper/90">
                <Link to="/registro">Abre una cuenta</Link>
              </Button>
              <Button asChild variant="outline" size="lg">
                <Link to="/entrar">Ya tengo cuenta</Link>
              </Button>
            </div>
          </div>

          <HoldDemonstration />
        </div>

        {/* The three claims run the full width under both columns rather than under
            the prose, so they read as the base the page stands on instead of as a
            third paragraph. */}
        <dl className="mt-14 grid gap-6 border-t border-rule pt-6 sm:grid-cols-3 lg:mt-20">
          {[
            [
              'Doble entrada',
              'Todo cargo tiene su abono, y el ledger lo impone: un sobregiro es imposible por construcción, no por una validación.',
            ],
            [
              'Centavos enteros',
              'Ningún importe pasa por un float, en ningún punto del sistema.',
            ],
            [
              'Confirmación real',
              'La IA reserva los fondos y te pregunta. Moverlos lo decides tú, siempre.',
            ],
          ].map(([term, detail]) => (
            <div key={term}>
              <dt className="type-eyebrow">{term}</dt>
              <dd className="mt-1.5 max-w-[42ch] text-[0.875rem] leading-snug text-ink-soft">
                {detail}
              </dd>
            </div>
          ))}
        </dl>
      </main>

      <footer className={cn('border-t border-rule py-5', gutter)}>
        <p className="text-[0.75rem] text-ink-faint">
          Saldos y movimientos sobre un ledger de doble entrada. Los importes se manejan en
          centavos enteros.
        </p>
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
          {reserved ? (
            <>
              Hay <strong className="font-semibold text-hold">$250.00</strong> retenidos.
              Salieron de lo que puedes gastar y todavía no han llegado a ninguna parte: el
              asiento existe, pendiente, y nadie más puede gastar ese dinero. Un banco que
              guarda el saldo en una columna no tiene forma de decir esto.
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
