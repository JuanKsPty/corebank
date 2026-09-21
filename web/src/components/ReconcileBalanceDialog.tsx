import { CircleAlertIcon, ScaleIcon } from 'lucide-react'
import { useState } from 'react'

import { ApiError } from '@/api/client'
import type { Amount, ReconcileResult } from '@/api/types'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { Spinner } from '@/components/ui/spinner'
import { money } from '@/lib/format'

/**
 * Correcting a balance a bank import got wrong ("sincerar saldo").
 *
 * One dialog, two very different backends: a real account posts one
 * corrective deposit or withdrawal, because TigerBeetle can never edit or
 * delete a past movement (see transactions.Service.Reconcile); a card
 * inserts one adjustment row into a sum that was never a stored figure to
 * begin with (see bankimport.Service.Reconcile). The interaction is
 * identical either way — state the true balance, corebank posts the delta —
 * so it is the caller's `onReconcile` that differs, not this component.
 *
 * Unlike a movement amount, the target balance may be zero or negative: an
 * empty or overdrawn account is a real, valid balance to state.
 */
export function ReconcileBalanceDialog({
  currentBalance,
  trigger,
  onReconcile,
  isPending,
  error,
  reset,
}: {
  currentBalance: Amount
  trigger: React.ReactNode
  onReconcile: (targetBalance: string) => Promise<ReconcileResult>
  isPending: boolean
  error: unknown
  reset: () => void
}) {
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState('')
  const [fieldError, setFieldError] = useState<string | undefined>()
  const [result, setResult] = useState<ReconcileResult | null>(null)

  const failure =
    error instanceof ApiError
      ? (error.fields?.target_balance ?? error.message)
      : error
        ? 'No se pudo sincerar el saldo. Inténtalo de nuevo.'
        : null

  async function submit(event: React.FormEvent) {
    event.preventDefault()

    const problem = targetBalanceProblem(value)
    setFieldError(problem)
    if (problem) return

    setResult(null)
    const outcome = await onReconcile(value.trim())
    setResult(outcome)
    if (outcome.adjusted) setOpen(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) {
          reset()
          setValue('')
          setFieldError(undefined)
          setResult(null)
        }
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>

      <DialogContent className="sm:max-w-[26rem]">
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle className="type-display text-[1.25rem]">
              Sincerar saldo
            </DialogTitle>
            <DialogDescription>
              corebank calcula {money(currentBalance)} a partir de lo importado. Si tu banco
              muestra otro monto, dilo aquí y se corrige con un solo ajuste.
            </DialogDescription>
          </DialogHeader>

          <div className="py-2">
            <Field data-invalid={fieldError ? true : undefined}>
              <FieldLabel htmlFor="saldo-real">Saldo real, según tu banco</FieldLabel>
              <InputGroup>
                <InputGroupAddon>
                  <span className="type-figure text-ink-faint" aria-hidden="true">
                    $
                  </span>
                </InputGroupAddon>
                <InputGroupInput
                  id="saldo-real"
                  className="type-figure text-[1.125rem]"
                  inputMode="decimal"
                  autoComplete="off"
                  autoFocus
                  placeholder="0.00"
                  value={value}
                  disabled={isPending}
                  aria-invalid={fieldError ? true : undefined}
                  onChange={(event) => {
                    setValue(event.target.value)
                    if (fieldError) setFieldError(undefined)
                  }}
                />
              </InputGroup>
              {fieldError ? (
                <FieldError>{fieldError}</FieldError>
              ) : (
                <FieldDescription>
                  Puede ser negativo si la cuenta está en descubierto.
                </FieldDescription>
              )}
            </Field>
          </div>

          {result && !result.adjusted && (
            <p className="text-[0.8125rem] text-ink-soft">
              Ya coincide con lo que ingresaste.
            </p>
          )}

          {failure && (
            <Alert variant="destructive">
              <CircleAlertIcon />
              <AlertTitle>No se pudo sincerar el saldo</AlertTitle>
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}

          <DialogFooter className="mt-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => setOpen(false)}
              disabled={isPending}
            >
              Cancelar
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending && <Spinner />}
              <ScaleIcon aria-hidden="true" />
              Sincerar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Validates a target balance: a signed decimal with up to two places.
 * Distinct from `amountProblem` (`@/lib/validate`), which every movement
 * amount uses — that one requires a strictly positive amount, and a balance
 * is neither strictly positive nor a "how much to move" figure.
 */
function targetBalanceProblem(value: string): string | undefined {
  const raw = value.trim()
  if (!raw) return 'Indica un monto.'
  if (!/^-?\d+(\.\d{1,2})?$/.test(raw)) {
    if (/,/.test(raw))
      return 'Escribe el monto sin separador de miles, por ejemplo 1500.50.'
    if (/^-?\d+\.\d{3,}$/.test(raw)) return 'El monto admite como máximo dos decimales.'
    return 'Escribe solo dígitos, un signo opcional y un punto decimal, por ejemplo -12.50.'
  }
  return undefined
}
