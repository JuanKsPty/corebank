import { useState } from 'react'

import { ApiError } from '@/api/client'
import { cn } from '@/lib/utils'
import type { Account, ConfirmationCard as Card } from '@/api/types'
import { counterpartyLabel, moneyFromText, movementLabel } from '@/lib/format'
import { useMe, useResolveConfirmation } from '@/lib/queries'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { Countdown } from './primitives'

/**
 * ConfirmationCard is the two buttons that decide whether money moves.
 *
 * It is the most consequential thing in the interface, so it is built to be
 * unmistakable rather than convenient. It states in words that nothing has moved
 * yet; it shows the amount, both accounts, and the balance the customer would be
 * left with; and it carries a live countdown, because the reservation really does
 * expire on its own and a card that looked permanent would be lying.
 *
 * Confirming posts to the API with the hold id. The assistant cannot take this
 * step — it has no way to — which is what makes a prompt injection unable to
 * complete a payment no matter what it persuades the model to say.
 */
export function ConfirmationCard({
  card,
  onResolved,
  onExpired,
}: {
  card: Card
  onResolved?: (action: 'confirm' | 'cancel') => void
  onExpired?: () => void
}) {
  // The customer's own accounts, from the cache the rest of the interface already
  // fills. The alias is resolved here rather than carried on the SSE event so the
  // card shows what the account is called *now*, and so naming an account does not
  // become something the confirmation contract has to know about.
  const me = useMe()
  const accounts = me.data?.accounts ?? []

  const resolve = useResolveConfirmation()
  const [outcome, setOutcome] = useState<'confirm' | 'cancel' | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [expired, setExpired] = useState(false)

  async function act(action: 'confirm' | 'cancel') {
    setFailure(null)
    try {
      await resolve.mutateAsync({ holdId: card.hold_id, action })
      setOutcome(action)
      onResolved?.(action)
    } catch (error) {
      // The most likely failure here is that the reservation expired while the
      // customer was deciding, which is not an error in their behaviour and is
      // worth naming precisely.
      if (error instanceof ApiError) {
        if (error.code === 'confirmation_expired') setExpired(true)
        setFailure(error.message)
      } else {
        setFailure('No se pudo contactar con el servidor. Inténtalo de nuevo.')
      }
    }
  }

  if (outcome) {
    return <ResolvedCard card={card} action={outcome} />
  }

  const isWithdrawal = card.kind === 'withdrawal'
  const working = resolve.isPending

  return (
    <div
      className={cn(
        'overflow-hidden rounded-[6px] border-2 border-hold/55 bg-paper-raised',
        // A left band in the hold colour, so the card reads as "reserved" at a
        // glance and is not mistaken for a completed movement.
        'shadow-[inset_4px_0_0_var(--color-hold)]',
      )}
      role="group"
      aria-label={`Confirmar ${movementLabel(card.kind).toLowerCase()} de ${moneyFromText(card.amount)}`}
    >
      <div className="flex items-center justify-between gap-3 border-b border-rule bg-hold/8 px-4 py-2">
        <p
          className="type-eyebrow whitespace-nowrap text-[#8a6516]"
          style={{ letterSpacing: '0.08em' }}
        >
          {movementLabel(card.kind)} pendiente
        </p>
        <span className="flex items-center gap-1.5 text-[#8a6516]">
          <span
            aria-hidden="true"
            className="size-1.5 animate-pulse rounded-full bg-hold"
          />
          <Countdown
            until={card.expires_at}
            onElapsed={() => {
              setExpired(true)
              onExpired?.()
            }}
          />
        </span>
      </div>

      <div className="px-4 py-3.5">
        <p className="type-figure text-[1.75rem] leading-none">
          {moneyFromText(card.amount)}
        </p>

        <dl className="mt-3.5 space-y-1.5 text-[0.8125rem]">
          <div className="flex gap-2">
            <dt className="w-14 shrink-0 text-ink-faint">Desde</dt>
            <dd className="min-w-0">
              <AccountRef number={card.from_account} accounts={accounts} />
            </dd>
          </div>
          <div className="flex gap-2">
            <dt className="w-14 shrink-0 text-ink-faint">
              {isWithdrawal ? 'Salida' : 'Hacia'}
            </dt>
            <dd className="min-w-0">
              <AccountRef number={card.to_account} accounts={accounts} />
            </dd>
          </div>
          {card.balance_if_confirmed && (
            <div className="flex gap-2 border-t border-rule pt-1.5">
              <dt className="w-14 shrink-0 text-ink-faint">Te queda</dt>
              <dd className="type-figure">{moneyFromText(card.balance_if_confirmed)}</dd>
            </div>
          )}
        </dl>

        {/* The sentence that matters most on the card. */}
        <p className="mt-3 text-[0.8125rem] leading-snug text-[#8a6516]">
          Los fondos están reservados y tu saldo disponible ya lo refleja, pero el dinero{' '}
          <strong className="font-semibold">no se ha movido</strong>.
        </p>

        {failure && (
          <p className="mt-3 text-[0.8125rem] text-[#8f2d24]" role="alert">
            {failure}
          </p>
        )}

        {expired ? (
          <p className="mt-3.5 rounded border border-rule bg-paper-sunken px-3 py-2 text-[0.8125rem] text-ink-soft">
            La reserva expiró y los fondos volvieron a estar disponibles. Pídelo de nuevo si
            aún lo quieres.
          </p>
        ) : (
          <div className="mt-4 flex gap-2">
            <Button
              onClick={() => void act('confirm')}
              disabled={working}
              className="flex-1 bg-copper text-white hover:bg-copper/90"
            >
              {working && resolve.variables?.action === 'confirm' && <Spinner />}
              Confirmar
            </Button>
            <Button
              variant="outline"
              onClick={() => void act('cancel')}
              disabled={working}
              className="flex-1"
            >
              {working && resolve.variables?.action === 'cancel' && <Spinner />}
              Cancelar
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

/** What the card becomes once the customer has decided. */
function ResolvedCard({ card, action }: { card: Card; action: 'confirm' | 'cancel' }) {
  const confirmed = action === 'confirm'

  return (
    <div
      className={cn(
        'rounded-[6px] border px-4 py-3',
        confirmed ? 'border-credit/35 bg-credit/6' : 'border-rule bg-paper-sunken',
      )}
      role="status"
    >
      <div className="flex items-center gap-2">
        {confirmed ? (
          <svg
            viewBox="0 0 16 16"
            className="size-3.5 shrink-0 text-credit"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <path d="M3 8.5l3.5 3.5L13 4.5" />
          </svg>
        ) : (
          <svg
            viewBox="0 0 16 16"
            className="size-3.5 shrink-0 text-ink-faint"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.8"
          >
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
        )}
        <p
          className={cn(
            'text-[0.875rem] font-medium',
            confirmed ? 'text-credit' : 'text-ink-soft',
          )}
        >
          {confirmed
            ? `${movementLabel(card.kind)} de ${moneyFromText(card.amount)} completada`
            : `${movementLabel(card.kind)} cancelada`}
        </p>
      </div>
      <p className="mt-0.5 pl-[1.375rem] text-[0.75rem] text-ink-faint">
        {confirmed
          ? 'El dinero se movió y ya aparece en tu historial.'
          : 'No se movió nada y los fondos volvieron a estar disponibles.'}
      </p>
    </div>
  )
}

/**
 * An account as it appears on the card: by the name its owner gave it, with the last
 * four digits alongside.
 *
 * This is the one screen where mistaking two accounts costs money, so it is the one
 * that most needs to say "Gastos del mes" rather than a sixteen-digit number nobody
 * reads to the end. The number stays because aliases are not unique and the digits are
 * what actually identify the account.
 *
 * A counterparty that is not one of the customer's own accounts has no alias to find,
 * and falls back to what the card always showed.
 */
function AccountRef({
  number,
  accounts,
}: {
  number: string | undefined
  accounts: Account[]
}) {
  const own = number
    ? accounts.find((account) => account.account_number === number)
    : undefined
  const alias = own?.alias.trim()

  if (!alias || !number) {
    return <span className="type-figure">{counterpartyLabel(number)}</span>
  }

  return (
    <span className="flex min-w-0 flex-wrap items-baseline gap-x-1.5">
      <span className="truncate font-medium">{alias}</span>
      <span className="type-figure shrink-0 text-ink-faint">···{number.slice(-4)}</span>
    </span>
  )
}
