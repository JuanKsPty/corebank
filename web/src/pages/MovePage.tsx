import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { newIdempotencyKey } from '@/api/endpoints'
import type { Transaction } from '@/api/types'
import { ConfirmationCard } from '@/components/ConfirmationCard'
import { Field, Figure, Notice, Spinner, cx } from '@/components/primitives'
import { accountTypeLabel, money, movementLabel } from '@/lib/format'
import { useMe, useMovement } from '@/lib/queries'
import {
  accountNumberProblem,
  amountProblem,
  isClean,
  normaliseAccountNumber,
  type Errors,
} from '@/lib/validate'

type Kind = 'deposit' | 'withdraw' | 'transfer'
type FieldName = 'amount' | 'account_number' | 'to_account_number' | 'description'

const KINDS: Array<{ value: Kind; label: string; blurb: string }> = [
  { value: 'deposit', label: 'Ingresar', blurb: 'Entra dinero a tu cuenta.' },
  { value: 'withdraw', label: 'Retirar', blurb: 'Sale dinero de tu cuenta.' },
  { value: 'transfer', label: 'Transferir', blurb: 'Envía dinero a otra cuenta.' },
]

/**
 * Moving money by hand.
 *
 * The three operations share one form, because they are the same act with different
 * counterparties, and three near-identical forms would drift apart in how they
 * validate. What changes between them is which fields appear and whether the
 * customer is offered the confirmation step.
 *
 * That offer is deliberate. The confirmation flow is not something the assistant
 * has and the interface lacks: anybody can ask for their money to be reserved and
 * confirmed in two steps, which is what makes it a property of the bank rather than
 * a feature of the chat.
 */
export function MovePage() {
  const me = useMe()
  const [kind, setKind] = useState<Kind>('deposit')

  const accounts = me.data?.accounts ?? []

  return (
    <div className="mx-auto max-w-2xl">
      <header>
        <p className="type-eyebrow">Mover dinero</p>
        <h1 className="type-display mt-1.5 text-[1.75rem]">¿Qué quieres hacer?</h1>
      </header>

      <div className="mt-5 grid gap-2 sm:grid-cols-3" role="tablist" aria-label="Tipo de operación">
        {KINDS.map((option) => (
          <button
            key={option.value}
            type="button"
            role="tab"
            aria-selected={kind === option.value}
            onClick={() => setKind(option.value)}
            className={cx(
              'rounded-[5px] border px-3.5 py-3 text-left transition-colors',
              kind === option.value
                ? 'border-copper bg-copper/8'
                : 'border-rule bg-paper-raised hover:border-ink-faint',
            )}
          >
            <span className="block text-[0.9375rem] font-medium">{option.label}</span>
            <span className="mt-0.5 block text-[0.75rem] text-ink-faint">{option.blurb}</span>
          </button>
        ))}
      </div>

      {/* The form is keyed on the operation, so switching resets it. Carrying a
          destination account over from a transfer into a deposit would be a
          confusing kind of helpful. */}
      <MovementForm key={kind} kind={kind} accounts={accounts} loading={me.isLoading} />
    </div>
  )
}

function MovementForm({
  kind,
  accounts,
  loading,
}: {
  kind: Kind
  accounts: Array<{ account_number: string; account_type: string; available: { cents: number; formatted: string; currency: string } }>
  loading: boolean
}) {
  const movement = useMovement(kind)

  const [amount, setAmount] = useState('')
  const [source, setSource] = useState('')
  const [destination, setDestination] = useState('')
  const [description, setDescription] = useState('')
  const [confirmFirst, setConfirmFirst] = useState(false)
  const [errors, setErrors] = useState<Errors<FieldName>>({})
  const [failure, setFailure] = useState<ApiError | null>(null)
  const [result, setResult] = useState<Transaction | null>(null)

  /**
   * One key per attempt, kept across retries.
   *
   * This is the whole point of the header: if the response to the first submission
   * is lost and the customer presses the button again, the same key tells the server
   * it is the same movement. A fresh key per press would move the money twice.
   */
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey)

  const needsSource = kind !== 'deposit'
  const needsDestination = kind === 'transfer'
  const canConfirmFirst = kind !== 'deposit'

  const selectedAccount = useMemo(
    () => accounts.find((account) => account.account_number === (source || accounts[0]?.account_number)),
    [accounts, source],
  )

  // The available balance is only a limit when money is leaving. Checking it here
  // is a courtesy, not the guarantee: the ledger refuses an overdraft regardless.
  const limitCents = needsSource ? selectedAccount?.available.cents : undefined

  function validate(): Errors<FieldName> {
    return {
      amount: amountProblem(amount, limitCents),
      account_number:
        needsSource && accounts.length > 1 && !source
          ? 'Elige desde qué cuenta.'
          : undefined,
      to_account_number: needsDestination
        ? accountNumberProblem(destination) ??
          (normaliseAccountNumber(destination) === (source || accounts[0]?.account_number)
            ? 'El origen y el destino no pueden ser la misma cuenta.'
            : undefined)
        : undefined,
      description: description.length > 200 ? 'Máximo 200 caracteres.' : undefined,
    }
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()

    const found = validate()
    setErrors(found)
    if (!isClean(found)) return

    setFailure(null)
    try {
      const transaction = await movement.mutateAsync({
        input: {
          amount: amount.trim(),
          account_number: source || undefined,
          to_account_number: needsDestination ? normaliseAccountNumber(destination) : undefined,
          description: description.trim() || undefined,
          require_confirmation: confirmFirst && canConfirmFirst,
        },
        idempotencyKey,
      })
      setResult(transaction)
    } catch (error) {
      if (error instanceof ApiError) {
        setFailure(error)
        if (error.fields) setErrors(error.fields as Errors<FieldName>)
        // A rejected movement never happened, so the next attempt is a new one and
        // reusing the key would make the server report a mismatch.
        if (error.status !== 0 && error.status < 500) setIdempotencyKey(newIdempotencyKey())
      } else {
        setFailure(
          new ApiError(0, {
            code: 'network',
            message:
              'No se pudo contactar con el servidor. Puedes volver a enviarlo: si el primer intento llegó, no se duplicará.',
          }),
        )
      }
    }
  }

  function reset() {
    setAmount('')
    setDestination('')
    setDescription('')
    setResult(null)
    setFailure(null)
    setErrors({})
    setIdempotencyKey(newIdempotencyKey())
  }

  if (result) {
    return <Outcome transaction={result} onAgain={reset} />
  }

  return (
    <form onSubmit={handleSubmit} noValidate className="card mt-4 space-y-4 p-5">
      {failure && (
        <Notice
          tone="error"
          title={failureTitle(failure)}
          requestId={failure.status >= 500 ? failure.requestId : undefined}
          onDismiss={() => setFailure(null)}
        >
          {failure.message}
        </Notice>
      )}

      <Field
        label="Monto"
        error={errors.amount}
        hint={
          limitCents !== undefined && selectedAccount
            ? `Disponible en esta cuenta: ${money(selectedAccount.available)}`
            : 'Hasta dos decimales, por ejemplo 150.50.'
        }
      >
        {(props) => (
          <div className="relative">
            <span
              aria-hidden="true"
              className="type-figure pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ink-faint"
            >
              $
            </span>
            <input
              {...props}
              className="field-input type-figure pl-7 text-[1.125rem]"
              // `decimal` rather than `numeric`: it puts a decimal point on a phone
              // keypad, which an amount needs.
              inputMode="decimal"
              autoComplete="off"
              autoFocus
              placeholder="0.00"
              value={amount}
              onChange={(event) => {
                setAmount(event.target.value)
                if (errors.amount) setErrors((p) => ({ ...p, amount: undefined }))
              }}
            />
          </div>
        )}
      </Field>

      {needsSource && (
        <Field label="Desde" error={errors.account_number}>
          {(props) =>
            loading ? (
              <div className="skeleton h-10 w-full" aria-hidden="true" />
            ) : (
              <select
                {...props}
                className="field-input"
                value={source}
                onChange={(event) => {
                  setSource(event.target.value)
                  if (errors.account_number) setErrors((p) => ({ ...p, account_number: undefined }))
                }}
              >
                {accounts.length > 1 && <option value="">Elige una cuenta</option>}
                {accounts.map((account) => (
                  <option key={account.account_number} value={account.account_number}>
                    {accountTypeLabel(account.account_type)} · {account.account_number} ·{' '}
                    {money(account.available)}
                  </option>
                ))}
              </select>
            )
          }
        </Field>
      )}

      {kind === 'deposit' && accounts.length > 1 && (
        <Field label="Hacia" error={errors.account_number}>
          {(props) => (
            <select {...props} className="field-input" value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="">Elige una cuenta</option>
              {accounts.map((account) => (
                <option key={account.account_number} value={account.account_number}>
                  {accountTypeLabel(account.account_type)} · {account.account_number}
                </option>
              ))}
            </select>
          )}
        </Field>
      )}

      {needsDestination && (
        <Field
          label="Cuenta de destino"
          error={errors.to_account_number}
          hint="16 dígitos, con o sin guiones."
        >
          {(props) => (
            <input
              {...props}
              className="field-input type-figure"
              inputMode="numeric"
              autoComplete="off"
              placeholder="4001-0000-0000-0000"
              value={destination}
              onChange={(event) => {
                setDestination(event.target.value)
                if (errors.to_account_number) setErrors((p) => ({ ...p, to_account_number: undefined }))
              }}
              onBlur={() => setDestination((value) => (value ? normaliseAccountNumber(value) : value))}
            />
          )}
        </Field>
      )}

      <Field label="Concepto" error={errors.description} hint="Opcional. Lo verás en tu historial.">
        {(props) => (
          <input
            {...props}
            className="field-input"
            maxLength={200}
            autoComplete="off"
            placeholder={kind === 'deposit' ? 'Nómina' : 'Pago del alquiler'}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        )}
      </Field>

      {canConfirmFirst && (
        <label className="flex cursor-pointer items-start gap-2.5 rounded-[5px] border border-rule bg-paper-sunken px-3.5 py-3">
          <input
            type="checkbox"
            checked={confirmFirst}
            onChange={(event) => setConfirmFirst(event.target.checked)}
            className="mt-0.5 size-4 accent-copper"
          />
          <span>
            <span className="block text-[0.875rem] font-medium">Reservar y confirmar después</span>
            <span className="mt-0.5 block text-[0.75rem] text-ink-soft">
              Los fondos quedan retenidos y el dinero solo se mueve cuando lo
              confirmes. Es el mismo paso que usa el asistente.
            </span>
          </span>
        </label>
      )}

      <div className="flex items-center gap-3 pt-1">
        <button type="submit" className="btn btn-primary" disabled={movement.isPending}>
          {movement.isPending && <Spinner />}
          {movement.isPending
            ? 'Enviando'
            : confirmFirst && canConfirmFirst
              ? 'Reservar fondos'
              : KINDS.find((option) => option.value === kind)?.label}
        </button>
        <Link to="/" className="text-[0.875rem] text-ink-soft hover:text-ink">
          Cancelar
        </Link>
      </div>
    </form>
  )
}

/** What replaces the form once the movement has an outcome. */
function Outcome({ transaction, onAgain }: { transaction: Transaction; onAgain: () => void }) {
  const awaiting = transaction.confirmation && transaction.status === 'pending'

  return (
    <div className="mt-4 space-y-4">
      {awaiting ? (
        <>
          <Notice tone="hold" title="Fondos reservados">
            El dinero todavía no se ha movido. Confirma o cancela abajo.
          </Notice>
          <ConfirmationCard
            card={{
              hold_id: transaction.confirmation!.hold_id,
              kind: transaction.kind,
              amount: transaction.amount.formatted,
              from_account: transaction.from_account ?? '',
              to_account: transaction.to_account ?? '',
              expires_at: transaction.confirmation!.expires_at,
            }}
          />
        </>
      ) : (
        <div className="card p-5">
          <div className="flex items-center gap-2 text-credit">
            <svg viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M3 8.5l3.5 3.5L13 4.5" />
            </svg>
            <p className="font-medium">{movementLabel(transaction.kind)} completada</p>
          </div>
          <p className="mt-3">
            <Figure amount={transaction.amount} size="lg" />
          </p>
          {transaction.description && (
            <p className="mt-1 text-[0.875rem] text-ink-soft">{transaction.description}</p>
          )}
        </div>
      )}

      <div className="flex items-center gap-3">
        <button type="button" onClick={onAgain} className="btn btn-secondary">
          Hacer otra operación
        </button>
        <Link to="/" className="text-[0.875rem] text-ink-soft hover:text-ink">
          Volver al resumen
        </Link>
      </div>
    </div>
  )
}

function failureTitle(error: ApiError): string {
  switch (error.code) {
    case 'insufficient_funds':
      return 'No hay fondos suficientes'
    case 'destination_account_not_found':
      return 'La cuenta de destino no existe'
    case 'same_account':
      return 'Origen y destino coinciden'
    case 'idempotency_key_reused':
      return 'Esa clave ya se usó'
    case 'rate_limited':
      return 'Demasiadas peticiones'
    default:
      return 'No se pudo completar la operación'
  }
}
