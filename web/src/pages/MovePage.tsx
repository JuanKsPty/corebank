import { CircleAlertIcon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { newIdempotencyKey } from '@/api/endpoints'
import { cn } from '@/lib/utils'
import type { Amount, Transaction } from '@/api/types'
import { ConfirmationCard } from '@/components/ConfirmationCard'
import { BalanceComposition, Figure } from '@/components/primitives'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Spinner } from '@/components/ui/spinner'
import { Skeleton } from '@/components/ui/skeleton'
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
    // A form has a measure — a 1400px-wide field is not easier to fill in — but it used
    // to get that measure from `mx-auto max-w-2xl`, which meant 672px of form centred in
    // 1440px of viewport with 384px of nothing down each side. The cap stays; what
    // changes is that the space left over is put to work instead of being padding. What
    // goes in it is the one thing you actually need while filling this in: what you have
    // and how much of it is already spoken for.
    //
    // The side column waits until `2xl` because it is competing for width with two other
    // fixtures: the 16rem rail and, from `xl`, the 24rem assistant. At 1440px those two
    // leave 736px, and splitting that three ways gave the accounts 136px — narrow enough
    // that the balance was clipped inside its own card. Below `2xl` the accounts sit
    // under the form instead, which is where a phone has them anyway.
    <div className="grid gap-8 2xl:grid-cols-[minmax(0,32rem)_minmax(0,1fr)] 2xl:gap-12">
      <div className="min-w-0">
        <header>
          <p className="type-eyebrow">Mover dinero</p>
          <h1 className="type-display mt-1.5 text-[1.75rem]">¿Qué quieres hacer?</h1>
        </header>

        <div
          className="mt-5 grid gap-2 sm:grid-cols-3"
          role="tablist"
          aria-label="Tipo de operación"
        >
          {KINDS.map((option) => (
            <button
              key={option.value}
              type="button"
              role="tab"
              aria-selected={kind === option.value}
              onClick={() => setKind(option.value)}
              className={cn(
                'rounded-[5px] border px-3.5 py-3 text-left transition-colors',
                kind === option.value
                  ? 'border-copper bg-copper/8'
                  : 'border-rule bg-paper-raised hover:border-ink-faint',
              )}
            >
              <span className="block text-[0.9375rem] font-medium">{option.label}</span>
              <span className="mt-0.5 block text-[0.75rem] text-ink-faint">
                {option.blurb}
              </span>
            </button>
          ))}
        </div>

        {/* The form is keyed on the operation, so switching resets it. Carrying a
            destination account over from a transfer into a deposit would be a
            confusing kind of helpful. */}
        <MovementForm key={kind} kind={kind} accounts={accounts} loading={me.isLoading} />
      </div>

      <AvailableAside accounts={accounts} loading={me.isLoading} />
    </div>
  )
}

/**
 * What you have, beside the form that spends it.
 *
 * Below `2xl` this drops under the form rather than disappearing: on a phone it is still
 * the answer to "can I actually send this", and it is cheap — the accounts are already
 * loaded to populate the form's own account picker.
 */
function AvailableAside({
  accounts,
  loading,
}: {
  accounts: Array<{
    account_number: string
    account_type: string
    available: Amount
    posted: Amount
    held: Amount
  }>
  loading?: boolean
}) {
  if (loading) {
    return (
      <aside className="space-y-3" aria-hidden="true">
        <Skeleton className="h-3 w-28" />
        <Skeleton className="h-3 h-24 w-full" />
      </aside>
    )
  }
  if (accounts.length === 0) return null

  const held = accounts.reduce((total, account) => total + account.held.cents, 0)

  return (
    <aside aria-labelledby="disponible" className="2xl:pt-1">
      <h2 id="disponible" className="type-eyebrow">
        Lo que puedes mover
      </h2>

      {/* Two-up below 2xl, where this sits under the form and has the full width;
          stacked in the side column, where it does not. */}
      <ul className="mt-3 grid gap-2 sm:grid-cols-2 2xl:grid-cols-1">
        {accounts.map((account) => (
          <li key={account.account_number} className="card p-4">
            <div className="flex items-baseline justify-between gap-3">
              <p className="text-[0.8125rem] font-medium">
                {accountTypeLabel(account.account_type)}
              </p>
              <p className="type-figure text-[0.6875rem] text-ink-faint">
                ···{account.account_number.slice(-4)}
              </p>
            </div>
            <p className="mt-2">
              <Figure amount={account.available} size="lg" />
            </p>
            {account.held.cents > 0 && (
              <div className="mt-3">
                <BalanceComposition
                  posted={account.posted}
                  held={account.held}
                  available={account.available}
                />
              </div>
            )}
          </li>
        ))}
      </ul>

      {held > 0 && (
        <p className="mt-3 text-[0.8125rem] text-ink-soft">
          Hay{' '}
          <Figure
            amount={{ cents: held, formatted: (held / 100).toFixed(2), currency: 'USD' }}
            size="sm"
            tone="hold"
          />{' '}
          retenidos por operaciones que aún no has confirmado. Ese dinero no se puede mover
          hasta que las confirmes o expiren.
        </p>
      )}
    </aside>
  )
}

function MovementForm({
  kind,
  accounts,
  loading,
}: {
  kind: Kind
  accounts: Array<{
    account_number: string
    account_type: string
    available: { cents: number; formatted: string; currency: string }
  }>
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
    () =>
      accounts.find(
        (account) => account.account_number === (source || accounts[0]?.account_number),
      ),
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
        ? (accountNumberProblem(destination) ??
          (normaliseAccountNumber(destination) === (source || accounts[0]?.account_number)
            ? 'El origen y el destino no pueden ser la misma cuenta.'
            : undefined))
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
          to_account_number: needsDestination
            ? normaliseAccountNumber(destination)
            : undefined,
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
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{failureTitle(failure)}</AlertTitle>
          <AlertDescription>
            {failure.message}
            {failure.status >= 500 && failure.requestId && (
              <span className="type-figure mt-1 block text-[0.6875rem] opacity-70">
                ref {failure.requestId}
              </span>
            )}
          </AlertDescription>
        </Alert>
      )}

      <Field data-invalid={errors.amount ? true : undefined}>
        <FieldLabel htmlFor="monto">Monto</FieldLabel>
        <InputGroup>
          <InputGroupAddon>
            <span className="type-figure text-ink-faint" aria-hidden="true">
              $
            </span>
          </InputGroupAddon>
          <InputGroupInput
            id="monto"
            className="type-figure text-[1.125rem]"
            // `decimal` rather than `numeric`: it puts a decimal point on a phone
            // keypad, which an amount needs.
            inputMode="decimal"
            autoComplete="off"
            autoFocus
            placeholder="0.00"
            value={amount}
            aria-invalid={errors.amount ? true : undefined}
            onChange={(event) => {
              setAmount(event.target.value)
              if (errors.amount) setErrors((p) => ({ ...p, amount: undefined }))
            }}
          />
        </InputGroup>
        {errors.amount ? (
          <FieldError>{errors.amount}</FieldError>
        ) : (
          <FieldDescription>
            {limitCents !== undefined && selectedAccount
              ? `Disponible en esta cuenta: ${money(selectedAccount.available)}`
              : 'Hasta dos decimales, por ejemplo 150.50.'}
          </FieldDescription>
        )}
      </Field>

      {needsSource && (
        <Field data-invalid={errors.account_number ? true : undefined}>
          <FieldLabel htmlFor="origen">Desde</FieldLabel>
          {loading ? (
            <Skeleton className="h-9 w-full" />
          ) : (
            <NativeSelect
              id="origen"
              value={source}
              aria-invalid={errors.account_number ? true : undefined}
              onChange={(event) => {
                setSource(event.target.value)
                if (errors.account_number)
                  setErrors((p) => ({ ...p, account_number: undefined }))
              }}
              className="w-full"
            >
              {accounts.length > 1 && (
                <NativeSelectOption value="">Elige una cuenta</NativeSelectOption>
              )}
              {accounts.map((account) => (
                <NativeSelectOption
                  key={account.account_number}
                  value={account.account_number}
                >
                  {accountTypeLabel(account.account_type)} · {account.account_number} ·{' '}
                  {money(account.available)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
          {errors.account_number && <FieldError>{errors.account_number}</FieldError>}
        </Field>
      )}

      {kind === 'deposit' && accounts.length > 1 && (
        <Field data-invalid={errors.account_number ? true : undefined}>
          <FieldLabel htmlFor="destino-propio">Hacia</FieldLabel>
          <NativeSelect
            id="destino-propio"
            value={source}
            aria-invalid={errors.account_number ? true : undefined}
            onChange={(event) => setSource(event.target.value)}
            className="w-full"
          >
            <NativeSelectOption value="">Elige una cuenta</NativeSelectOption>
            {accounts.map((account) => (
              <NativeSelectOption
                key={account.account_number}
                value={account.account_number}
              >
                {accountTypeLabel(account.account_type)} · {account.account_number}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {errors.account_number && <FieldError>{errors.account_number}</FieldError>}
        </Field>
      )}

      {needsDestination && (
        <Field data-invalid={errors.to_account_number ? true : undefined}>
          <FieldLabel htmlFor="destino">Cuenta de destino</FieldLabel>
          <Input
            id="destino"
            className="type-figure"
            inputMode="numeric"
            autoComplete="off"
            placeholder="4001-0000-0000-0000"
            value={destination}
            aria-invalid={errors.to_account_number ? true : undefined}
            onChange={(event) => {
              setDestination(event.target.value)
              if (errors.to_account_number)
                setErrors((p) => ({ ...p, to_account_number: undefined }))
            }}
            onBlur={() =>
              setDestination((value) => (value ? normaliseAccountNumber(value) : value))
            }
          />
          {errors.to_account_number ? (
            <FieldError>{errors.to_account_number}</FieldError>
          ) : (
            <FieldDescription>16 dígitos, con o sin guiones.</FieldDescription>
          )}
        </Field>
      )}

      <Field data-invalid={errors.description ? true : undefined}>
        <FieldLabel htmlFor="concepto">Concepto</FieldLabel>
        <Input
          id="concepto"
          maxLength={200}
          autoComplete="off"
          placeholder={kind === 'deposit' ? 'Nómina' : 'Pago del alquiler'}
          value={description}
          aria-invalid={errors.description ? true : undefined}
          onChange={(event) => setDescription(event.target.value)}
        />
        {errors.description ? (
          <FieldError>{errors.description}</FieldError>
        ) : (
          <FieldDescription>Opcional. Lo verás en tu historial.</FieldDescription>
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
            <span className="block text-[0.875rem] font-medium">
              Reservar y confirmar después
            </span>
            <span className="mt-0.5 block text-[0.75rem] text-ink-soft">
              Los fondos quedan retenidos y el dinero solo se mueve cuando lo confirmes. Es
              el mismo paso que usa el asistente.
            </span>
          </span>
        </label>
      )}

      <div className="flex items-center gap-3 pt-1">
        <Button
          type="submit"
          disabled={movement.isPending}
          className="bg-copper text-white hover:bg-copper/90"
        >
          {movement.isPending && <Spinner />}
          {movement.isPending
            ? 'Enviando'
            : confirmFirst && canConfirmFirst
              ? 'Reservar fondos'
              : KINDS.find((option) => option.value === kind)?.label}
        </Button>
        <Link to="/panel" className="text-[0.875rem] text-ink-soft hover:text-ink">
          Cancelar
        </Link>
      </div>
    </form>
  )
}

/** What replaces the form once the movement has an outcome. */
function Outcome({
  transaction,
  onAgain,
}: {
  transaction: Transaction
  onAgain: () => void
}) {
  const awaiting = transaction.confirmation && transaction.status === 'pending'

  return (
    <div className="mt-4 space-y-4">
      {awaiting ? (
        <>
          <Alert className="border-hold/40 bg-hold/8 text-hold-text">
            <CircleAlertIcon className="text-hold" />
            <AlertTitle>Fondos reservados</AlertTitle>
            <AlertDescription className="text-hold-text/85">
              El dinero todavía no se ha movido y queda a la espera de que lo confirmes.
            </AlertDescription>
          </Alert>
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
            <svg
              viewBox="0 0 16 16"
              className="size-4"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
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
        <Button variant="outline" onClick={onAgain}>
          Hacer otra operación
        </Button>
        <Link to="/panel" className="text-[0.875rem] text-ink-soft hover:text-ink">
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
