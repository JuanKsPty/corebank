import { useState } from 'react'
import { CircleAlertIcon } from 'lucide-react'

import { ApiError } from '@/api/client'
import type { Account, CategoryNode, Entry } from '@/api/types'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'
import { accountLabel, entryKindLabel, formatCivilDate, money } from '@/lib/format'
import { useUpdateEntry } from '@/lib/queries'
import { Figure } from './primitives'

/**
 * One movement, opened. What the bank printed — amount, date, description — is
 * shown and cannot change; what it means can: its category, a note, and whether it
 * is a transfer between the owner's own accounts.
 */
export function EntryDialog({
  entry,
  account,
  categories,
  onClose,
}: {
  entry: Entry
  account?: Account
  categories: CategoryNode[]
  onClose: () => void
}) {
  const [category, setCategory] = useState(entry.category_id ?? '')
  const [note, setNote] = useState(entry.note ?? '')
  const [transfer, setTransfer] = useState(entry.kind === 'transfer')
  const update = useUpdateEntry()

  const wantsIncome = entry.amount.cents > 0 && entry.kind !== 'refund'
  const options = categories.filter((c) =>
    wantsIncome ? c.kind === 'income' : c.kind === 'expense',
  )

  const save = () => {
    const patch: { category_id?: string | null; note?: string; transfer?: boolean } = {}
    if ((entry.category_id ?? '') !== category) patch.category_id = category || null
    if ((entry.note ?? '') !== note) patch.note = note
    if ((entry.kind === 'transfer') !== transfer) patch.transfer = transfer
    if (Object.keys(patch).length === 0) return onClose()
    update.mutate({ id: entry.id, patch }, { onSuccess: onClose })
  }

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-[28rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.125rem]">
            {entry.description || entryKindLabel(entry.kind)}
          </DialogTitle>
          <DialogDescription>
            {formatCivilDate(entry.booked_on)}
            {account && ` · ${accountLabel(account)}`}
            {entry.bank_category && ` · ${entry.bank_category}`}
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-baseline justify-between">
          <Figure
            amount={entry.amount}
            size="lg"
            tone={entry.amount.cents > 0 ? 'credit' : 'ink'}
          />
          {entry.bank_balance && (
            <span className="text-[0.75rem] text-ink-faint">
              Saldo según el banco: {money(entry.bank_balance)}
            </span>
          )}
        </div>

        <label className="flex items-start gap-2 text-[0.875rem]">
          <input
            type="checkbox"
            className="mt-1"
            checked={transfer}
            onChange={(e) => setTransfer(e.target.checked)}
          />
          <span>
            Es una transferencia entre mis cuentas
            <span className="block text-[0.75rem] text-ink-faint">
              No cuenta como gasto ni como ingreso, por ejemplo el pago de la tarjeta.
            </span>
          </span>
        </label>

        {!transfer && (
          <Field>
            <FieldLabel htmlFor="entry-category">Categoría</FieldLabel>
            <NativeSelect
              id="entry-category"
              className="w-full"
              value={category}
              onChange={(e) => setCategory(e.target.value)}
            >
              <NativeSelectOption value="">Sin categoría</NativeSelectOption>
              {options.map((parent) => (
                <optgroup key={parent.id} label={parent.name}>
                  <NativeSelectOption value={parent.id}>{parent.name}</NativeSelectOption>
                  {parent.children.map((child) => (
                    <NativeSelectOption key={child.id} value={child.id}>
                      {child.name}
                    </NativeSelectOption>
                  ))}
                </optgroup>
              ))}
            </NativeSelect>
          </Field>
        )}

        <Field>
          <FieldLabel htmlFor="entry-note">Nota</FieldLabel>
          <Textarea
            id="entry-note"
            value={note}
            maxLength={500}
            rows={2}
            onChange={(e) => setNote(e.target.value)}
          />
        </Field>

        {update.isError && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertDescription>
              {update.error instanceof ApiError
                ? update.error.message
                : 'No se pudo guardar.'}
            </AlertDescription>
          </Alert>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancelar
          </Button>
          <Button
            onClick={save}
            disabled={update.isPending}
            className="bg-copper text-primary-foreground hover:bg-copper/90"
          >
            {update.isPending && <Spinner />}
            Guardar
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
