import { useEffect, useRef, useState } from 'react'

import type { Amount } from '@/api/types'
import { formatCountdown, money, moneyParts, secondsUntil } from '@/lib/format'
import { cn } from '@/lib/utils'

// ---------------------------------------------------------------------------
// Money
// ---------------------------------------------------------------------------

/**
 * Figure renders an amount with the cents set smaller than the dollars.
 *
 * The cents are what you check, not what you read, so they are subordinate — which
 * lets the dollars carry the display size without the whole number shouting.
 */
export function Figure({
  amount,
  size = 'md',
  tone = 'ink',
  className,
}: {
  amount: Amount | number
  size?: 'sm' | 'md' | 'lg' | 'display'
  tone?: 'ink' | 'credit' | 'hold' | 'faint' | 'copper'
  className?: string
}) {
  const { whole, cents } = moneyParts(amount)

  const sizes = {
    sm: 'text-[0.8125rem]',
    md: 'text-base',
    lg: 'text-[1.375rem]',
    display: 'text-[clamp(2.25rem,7vw,3.5rem)]',
  }
  const tones = {
    ink: 'text-ink',
    credit: 'text-credit',
    hold: 'text-hold',
    faint: 'text-ink-faint',
    copper: 'text-copper',
  }

  return (
    <span
      className={cn('type-figure whitespace-nowrap', sizes[size], tones[tone], className)}
      // The full value stays available to a screen reader as one number, rather
      // than being announced as two fragments.
      aria-label={money(amount)}
    >
      <span aria-hidden="true">{whole}</span>
      <span aria-hidden="true" className={size === 'display' ? 'text-[0.55em]' : 'text-[0.8em]'}>
        {cents}
      </span>
    </span>
  )
}

// ---------------------------------------------------------------------------
// The signature element
// ---------------------------------------------------------------------------

/**
 * BalanceComposition shows a balance as the ledger position it is.
 *
 * This is the one thing in the interface that exists because of what is underneath
 * it. A balance here is not a number but three: what has settled, what is reserved
 * by a movement nobody has confirmed yet, and what is therefore actually spendable.
 * Most banking interfaces cannot show the middle one, because most do not have a
 * ledger that can hold funds without moving them.
 *
 * So the bar is drawn as a single rule divided into what you can spend and what is
 * on hold — and the held segment grows in when it appears, because that is the
 * moment the assistant reserved money and the customer needs to see it happen.
 *
 * The held segment has a floor, and it needs one. A hold is usually a small fraction
 * of a balance: reserving $250 against this dataset's largest account, $85,047, is
 * 0.294%, which on a 576px bar is 1.7px — the element that exists to make a
 * reservation visible would render it as a speck indistinguishable from a rendering
 * artefact, and widening the bar makes that worse rather than better because the
 * fraction does not change. So below the width at which a segment can be read as a
 * proportion at all, the segment stops claiming to be one: it is floored at 10px and
 * a rule is drawn at the boundary, which reads as a mark rather than as a
 * measurement. The exact figures are stated underneath either way, and they are what
 * the customer acts on.
 */

/**
 * The narrowest held segment that reads as a deliberate part of the bar.
 *
 * Below roughly this width the eye takes a 6px-tall sliver for an artefact, and a
 * proportion that cannot be perceived is not communicating a proportion.
 */
const HELD_FLOOR_PX = 10

export function BalanceComposition({
  posted,
  held,
  available,
}: {
  posted: Amount
  held: Amount
  available: Amount
}) {
  const hasHold = held.cents > 0
  const heldShare = posted.cents > 0 ? Math.min(100, (held.cents / posted.cents) * 100) : 0

  return (
    <div>
      <div
        className="flex h-[6px] w-full overflow-hidden rounded-full bg-paper-sunken"
        role="img"
        aria-label={
          hasHold
            ? `De ${money(posted)} liquidados, ${money(held)} están retenidos y ${money(available)} disponibles.`
            : `${money(available)} disponibles.`
        }
      >
        <div className="flex-1 bg-ink/85" />
        {hasHold && (
          <div
            className="bg-hold"
            style={{
              // max() rather than minWidth so the floor participates in the flex
              // sizing instead of fighting it.
              width: `max(${heldShare}%, ${HELD_FLOOR_PX}px)`,
              // The boundary rule. At the floor this is most of what is seen, and it
              // is what makes a small reservation register at all.
              boxShadow: 'inset 1px 0 0 var(--color-paper-raised)',
              transformOrigin: 'right',
              animation: 'hold-grow 420ms var(--ease-out-soft)',
            }}
          />
        )}
      </div>

      {hasHold ? (
        <dl className="mt-2.5 flex flex-wrap items-baseline gap-x-5 gap-y-1 text-[0.8125rem]">
          <div className="flex items-baseline gap-1.5">
            <dt className="text-ink-faint">Liquidado</dt>
            <dd>
              <Figure amount={posted} size="sm" />
            </dd>
          </div>
          <div className="flex items-baseline gap-1.5">
            <dt className="flex items-center gap-1.5 text-hold">
              <span aria-hidden="true" className="size-2 rounded-full bg-hold" />
              Retenido
            </dt>
            <dd>
              <Figure amount={held} size="sm" tone="hold" />
            </dd>
          </div>
        </dl>
      ) : (
        // With nothing reserved, repeating the headline under it as "Liquidado"
        // would be an echo. Saying that nothing is held is the fact worth stating.
        <p className="mt-2.5 text-[0.8125rem] text-ink-faint">
          Nada retenido: todo tu saldo está disponible.
        </p>
      )}
    </div>
  )
}

/**
 * What is left here after the move to shadcn, and why.
 *
 * Everything that had an equivalent in the component library is gone: Badge, Notice,
 * EmptyState, Spinner, SkeletonLine and Field are now badge, alert, empty, spinner,
 * skeleton and field, which handle the states and the aria wiring better than the
 * hand-written versions did.
 *
 * These three stay because shadcn has nothing that does their job. Figure sets an
 * amount's cents smaller than its dollars and hands a screen reader the whole number
 * as one label. BalanceComposition draws a balance as the ledger position it is —
 * settled, reserved, spendable — which is the one thing this product can express that
 * a bank storing a balance in a column cannot. Countdown counts a reservation down to
 * its release and fires once when it gets there. A component library has no opinion
 * about any of that, because none of it is a generic interface pattern; it is this
 * bank.
 */

// ---------------------------------------------------------------------------
// Time
// ---------------------------------------------------------------------------

/** A live countdown to an instant, for a reservation about to be released. */
export function Countdown({ until, onElapsed }: { until: string; onElapsed?: () => void }) {
  const [seconds, setSeconds] = useState(() => secondsUntil(until))
  const elapsedRef = useRef(false)

  useEffect(() => {
    setSeconds(secondsUntil(until))
    elapsedRef.current = false

    const timer = setInterval(() => {
      const remaining = secondsUntil(until)
      setSeconds(remaining)

      // Fired once, so a parent refetching on expiry does not do it every second.
      if (remaining === 0 && !elapsedRef.current) {
        elapsedRef.current = true
        onElapsed?.()
      }
    }, 1000)

    return () => clearInterval(timer)
  }, [until, onElapsed])

  if (seconds === 0) return <span className="type-figure text-[0.6875rem]">expirado</span>

  return (
    <span className="type-figure text-[0.6875rem]" aria-live="off">
      {formatCountdown(seconds)}
    </span>
  )
}
