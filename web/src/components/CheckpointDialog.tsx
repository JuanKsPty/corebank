import { useState } from 'react'
import { CircleAlertIcon } from 'lucide-react'

import { ApiError } from '@/api/client'
import type { Account } from '@/api/types'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { todayCivil } from '@/lib/format'
import { useAddCheckpoint } from '@/lib/queries'

/**
 * Stating a balance read off the bank: "at the end of this day the account held X",
 * or for a card, "I owed X at the cut-off".
 *
 * A card asks for the amount owed as a positive number, as the PDF statement prints
 * it, and stores it negative. With "usar como saldo de partida" it becomes the
 * anchor the balance is computed from; otherwise it is a check, and any difference
 * is shown rather than corrected away.
 */
export function CheckpointDialog({
  account,
  trigger,
  anchor = false,
}: {
  account: Account
  trigger: React.ReactNode
  /** Open with "use as the starting balance" already ticked. */
  anchor?: boolean
}) {
  const card = account.class === 'liability'
  const [open, setOpen] = useState(false)
  const [asOf, setAsOf] = useState(todayCivil())
  const [amount, setAmount] = useState('')
  const [inFavour, setInFavour] = useState(false)
  const [pin, setPin] = useState(anchor)
  const [dueOn, setDueOn] = useState('')
  const [minimum, setMinimum] = useState('')
  const add = useAddCheckpoint(account.id)

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const typed = amount.replace(/,/g, '').trim()
    // A card's "monto adeudado" is a debt: negative from the owner's view.
    const balance = card && !inFavour && !typed.startsWith('-') ? `-${typed}` : typed
    add.mutate(
      {
        as_of: asOf,
        balance,
        pin,
        due_on: card && dueOn ? dueOn : undefined,
        minimum_payment: card && minimum ? minimum.replace(/,/g, '') : undefined,
      },
      { onSuccess: () => setOpen(false) },
    )
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) add.reset()
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-[26rem]">
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle className="type-display text-[1.25rem]">
              {card ? 'Saldo adeudado según tu estado' : 'Saldo según tu banco'}
            </DialogTitle>
            <DialogDescription>
              {card
                ? 'Cópialo del estado de cuenta en PDF: la fecha de corte y el monto adeudado.'
                : 'El saldo que muestra tu banco al final de un día.'}
            </DialogDescription>
          </DialogHeader>

          <Field>
            <FieldLabel htmlFor="cp-date">{card ? 'Fecha de corte' : 'Fecha'}</FieldLabel>
            <Input
              id="cp-date"
              type="date"
              value={asOf}
              onChange={(e) => setAsOf(e.target.value)}
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="cp-amount">{card ? 'Monto adeudado' : 'Saldo'}</FieldLabel>
            <Input
              id="cp-amount"
              inputMode="decimal"
              placeholder="0.00"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              required
            />
          </Field>
          {card && (
            <>
              <label className="flex items-center gap-2 text-[0.8125rem]">
                <input
                  type="checkbox"
                  checked={inFavour}
                  onChange={(e) => setInFavour(e.target.checked)}
                />
                Es un saldo a mi favor
              </label>
              <div className="grid grid-cols-2 gap-3">
                <Field>
                  <FieldLabel htmlFor="cp-due">Fecha de pago</FieldLabel>
                  <Input
                    id="cp-due"
                    type="date"
                    value={dueOn}
                    onChange={(e) => setDueOn(e.target.value)}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="cp-min">Pago mínimo</FieldLabel>
                  <Input
                    id="cp-min"
                    inputMode="decimal"
                    placeholder="0.00"
                    value={minimum}
                    onChange={(e) => setMinimum(e.target.value)}
                  />
                </Field>
              </div>
            </>
          )}
          <label className="flex items-start gap-2 text-[0.8125rem]">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={pin}
              onChange={(e) => setPin(e.target.checked)}
            />
            <span>
              Usar como saldo de partida
              <span className="block text-ink-faint">
                El saldo se calcula a partir de este. Si no, solo se compara.
              </span>
            </span>
          </label>

          {add.isError && (
            <Alert variant="destructive">
              <CircleAlertIcon />
              <AlertDescription>
                {add.error instanceof ApiError ? add.error.message : 'No se pudo guardar.'}
              </AlertDescription>
            </Alert>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              Cancelar
            </Button>
            <Button
              type="submit"
              disabled={add.isPending || !amount}
              className="bg-copper text-primary-foreground hover:bg-copper/90"
            >
              {add.isPending && <Spinner />}
              Guardar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
