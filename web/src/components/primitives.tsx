import { useEffect, useId, useRef, useState } from 'react'

import type { Amount } from '@/api/types'
import { formatCountdown, money, moneyParts, secondsUntil } from '@/lib/format'

/** cx joins class names, dropping anything falsy. */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}

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
      className={cx('type-figure whitespace-nowrap', sizes[size], tones[tone], className)}
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
 */
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
              width: `${heldShare}%`,
              minWidth: '3px',
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

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

export function Badge({
  children,
  tone = 'neutral',
}: {
  children: React.ReactNode
  tone?: 'neutral' | 'hold' | 'credit' | 'danger' | 'copper'
}) {
  const tones = {
    neutral: 'bg-paper-sunken text-ink-soft border-rule',
    hold: 'bg-hold/12 text-hold border-hold/35',
    credit: 'bg-credit/12 text-credit border-credit/30',
    danger: 'bg-[#a4342a]/10 text-[#8f2d24] border-[#a4342a]/30',
    copper: 'bg-copper/12 text-copper border-copper/30',
  }

  return (
    <span
      className={cx(
        'type-figure inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-[0.1rem]',
        'text-[0.6875rem] tracking-wide',
        tones[tone],
      )}
    >
      {children}
    </span>
  )
}

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

// ---------------------------------------------------------------------------
// Feedback
// ---------------------------------------------------------------------------

/**
 * Notice reports something that happened or went wrong.
 *
 * An error says what happened and what to do about it — never an apology, never a
 * vague "something went wrong". The request id is shown on a server fault because
 * it is the one thing that makes the failure findable in the logs.
 */
export function Notice({
  tone,
  title,
  children,
  requestId,
  onDismiss,
}: {
  tone: 'error' | 'success' | 'info' | 'hold'
  title: string
  children?: React.ReactNode
  requestId?: string
  onDismiss?: () => void
}) {
  const tones = {
    error: 'border-[#a4342a]/40 bg-[#a4342a]/6 text-[#7d2720]',
    success: 'border-credit/35 bg-credit/8 text-credit',
    info: 'border-rule bg-paper-sunken text-ink-soft',
    hold: 'border-hold/40 bg-hold/8 text-[#8a6516]',
  }

  return (
    <div
      className={cx('flex gap-3 rounded-[5px] border px-3.5 py-3 text-[0.875rem]', tones[tone])}
      role={tone === 'error' ? 'alert' : 'status'}
    >
      <div className="min-w-0 flex-1">
        <p className="font-medium">{title}</p>
        {children && <div className="mt-0.5 opacity-90">{children}</div>}
        {requestId && (
          <p className="type-figure mt-1.5 text-[0.6875rem] opacity-70">ref {requestId}</p>
        )}
      </div>
      {onDismiss && (
        <button
          type="button"
          onClick={onDismiss}
          className="-mr-1 -mt-1 self-start rounded p-1 opacity-60 hover:opacity-100"
          aria-label="Descartar"
        >
          <svg viewBox="0 0 16 16" className="size-3.5" fill="none" stroke="currentColor" strokeWidth="1.6">
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
        </button>
      )}
    </div>
  )
}

/**
 * EmptyState invites an action instead of stating a fact.
 *
 * "Aún no tienes movimientos" alone leaves somebody stuck; what makes the screen
 * useful is telling them what to do from here.
 */
export function EmptyState({
  title,
  children,
  action,
}: {
  title: string
  children?: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
      {/* A ruled page with nothing on it — the paper this ledger is drawn on. */}
      <svg viewBox="0 0 40 40" className="size-9 text-rule" fill="none" stroke="currentColor" strokeWidth="1.4">
        <rect x="7.5" y="4.5" width="25" height="31" rx="2" />
        <path d="M12 13h16M12 19h16M12 25h10" strokeOpacity="0.55" />
      </svg>
      <div>
        <p className="type-display text-[1.0625rem]">{title}</p>
        {children && <p className="mt-1 max-w-sm text-[0.875rem] text-ink-soft">{children}</p>}
      </div>
      {action}
    </div>
  )
}

/** Spinner is only ever shown inside a button that is working. */
export function Spinner({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      className={cx('size-3.5 animate-spin', className)}
      fill="none"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6.5" stroke="currentColor" strokeOpacity="0.25" strokeWidth="2" />
      <path d="M14.5 8A6.5 6.5 0 008 1.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  )
}

export function SkeletonLine({ className }: { className?: string }) {
  return <div className={cx('skeleton h-3', className)} aria-hidden="true" />
}

// ---------------------------------------------------------------------------
// Forms
// ---------------------------------------------------------------------------

/**
 * Field wires a label, its input, and its error message together.
 *
 * The message is bound with aria-describedby and the input marked aria-invalid, so
 * the failure is announced rather than only coloured — which is the difference
 * between a form that works with a screen reader and one that looks like it does.
 */
export function Field({
  label,
  error,
  hint,
  children,
  className,
}: {
  label: string
  error?: string
  hint?: string
  children: (props: {
    id: string
    'aria-invalid': boolean
    'aria-describedby': string | undefined
  }) => React.ReactNode
  className?: string
}) {
  const id = useId()
  const errorId = `${id}-error`
  const hintId = `${id}-hint`
  const describedBy = [error && errorId, hint && hintId].filter(Boolean).join(' ') || undefined

  return (
    <div className={className}>
      <label htmlFor={id} className="field-label">
        {label}
      </label>
      {children({ id, 'aria-invalid': Boolean(error), 'aria-describedby': describedBy })}
      {hint && !error && (
        <p id={hintId} className="mt-1 text-[0.75rem] text-ink-faint">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="field-error">
          <svg viewBox="0 0 16 16" className="mt-[0.2rem] size-3 shrink-0" fill="currentColor" aria-hidden="true">
            <path d="M8 1.5l6.5 12h-13L8 1.5zm0 4.2v4m0 1.8v.9" stroke="currentColor" strokeWidth="1.3" fill="none" />
          </svg>
          <span>{error}</span>
        </p>
      )}
    </div>
  )
}
