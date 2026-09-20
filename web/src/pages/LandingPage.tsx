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
 * The copy is about what somebody can do here, not about how it is built. The previous
 * version was the opposite: it opened on "cada movimiento tiene dos lados" — an
 * accounting truism that sounds like insight and tells a visitor nothing — then said
 * balances are "derived from a double-entry ledger rather than stored in a column", and
 * offered "no amount passes through a float" as a selling point. Four mentions of the
 * ledger and two of integer cents, every claim about the implementation and none about
 * the product. Nobody chose a bank for its storage model. That is the padding this page
 * was asked to lose, and the technical detail belongs where it now sits: one quiet line
 * in the footer, as evidence rather than as the pitch.
 *
 * What stays is the demonstration, because it shows instead of telling. Reserving is
 * what happens when you ask the assistant to move money — the funds leave the spendable
 * total, arrive nowhere, and wait for you — and this is the real component from the real
 * application, the same one on screen a minute later once somebody signs in.
 *
 * The headline states the two true things and neither of them is a phrase: this is an
 * online bank, and the data in it is invented. An earlier attempt opened on "tus
 * cuentas, y alguien que las opera contigo", which reads well and commits to nothing —
 * bank-marketing voice with the padding removed but the register intact. A demo whose
 * front page performs is still performing.
 *
 * Saying it is a demo in the footer was also still hiding it, so the caveat is in the
 * headline in softer ink. And the primary action is now getting in with the seeded
 * account rather than filling a registration form: somebody evaluating this should not
 * have to invent an identity before they can see a balance, and the link carries state
 * that fills the sign-in form, so the button costs the one click it advertises.
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
            {/* The caveat is in the headline, in softer ink, rather than in a badge
                somewhere below it. It is the second most important true thing about
                this page and hiding it in the footer was still hiding it. */}
            <h1 className="type-display max-w-[19ch] text-[clamp(2rem,5.2vw,3.5rem)]">
              Esto es un banco en línea.{' '}
              <span className="text-ink-faint">Los datos son ficticios.</span>
            </h1>
            <p className="mt-5 max-w-[46ch] text-[1.0625rem] leading-relaxed text-ink-soft">
              Todo lo demás funciona. Consulta saldos, transfiere entre cuentas y revisa tu
              historial, o pídeselo al asistente en español: él prepara el movimiento y
              espera a que tú lo confirmes.
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

      {/* The size of the dataset, because "ficticios" invites the question of how
          much of it there is, and the answer is more interesting than the adjective.
          The stack sits alongside as evidence for whoever wants it. */}
      <footer className={cn('border-t border-rule py-5', gutter)}>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[0.75rem]">
          <p className="text-ink-soft">1000 clientes, 1605 cuentas y 6429 movimientos.</p>
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
