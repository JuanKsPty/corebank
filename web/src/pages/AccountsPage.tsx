import { useState } from 'react'
import { ChevronRightIcon, PlusIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { ApiError } from '@/api/client'
import type { AccountType } from '@/api/types'
import { BalanceComposition, Figure } from '@/components/primitives'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { accountTypeLabel, formatDate } from '@/lib/format'
import { useMe, useOpenAccount } from '@/lib/queries'
import { cn } from '@/lib/utils'

const TYPES: Array<{ value: AccountType; label: string; blurb: string }> = [
  { value: 'savings', label: 'Ahorros', blurb: 'Para guardar' },
  { value: 'checking', label: 'Corriente', blurb: 'Para el día a día' },
  { value: 'investment', label: 'Inversión', blurb: 'Para hacer crecer' },
]

/**
 * Every account the customer holds, and the way to open another.
 *
 * This screen was missing, and its absence was doing real damage: the summary showed
 * account cards but there was no page that was *about* the accounts, and nothing anywhere
 * that opened one. Registration created your first account and that was the end of it —
 * a bank you could only ever hold one account at, with no explanation of why.
 *
 * The composition bar appears on any account with funds held, rather than only on the
 * summary. An account is where a reservation actually lives, so it is the right place to
 * see one.
 */
export function AccountsPage() {
  const me = useMe()
  const accounts = me.data?.accounts ?? []

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        {/* Hidden on a phone, where the app bar above already carries this exact title
            and repeating it costs a row of the screen to say nothing new. */}
        <div className="hidden md:block">
          <p className="type-eyebrow">Cuentas</p>
          <h1 className="type-display mt-1.5 text-[1.75rem]">Tus cuentas</h1>
        </div>
        <OpenAccountDialog held={accounts.length} />
      </header>

      {me.isLoading ? (
        <ul className="grid gap-3 sm:grid-cols-2">
          {[0, 1].map((index) => (
            <li key={index} className="card space-y-3 p-5">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-7 w-40" />
              <Skeleton className="h-3 w-32" />
            </li>
          ))}
        </ul>
      ) : (
        <>
          <ul className="grid gap-3 sm:grid-cols-2">
            {accounts.map((account) => (
              <li key={account.id}>
                <Link
                  to={`/cuentas/${account.account_number}`}
                  className={cn(
                    'card group flex h-full flex-col p-5 transition-colors hover:border-ink-faint',
                    account.held.cents > 0 && 'border-hold/45',
                  )}
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="text-[0.9375rem] font-medium">
                        {accountTypeLabel(account.account_type)}
                      </p>
                      <p className="type-figure mt-0.5 text-[0.75rem] text-ink-faint">
                        {account.account_number}
                      </p>
                    </div>
                    <ChevronRightIcon
                      className="mt-0.5 size-4 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5"
                      aria-hidden="true"
                    />
                  </div>

                  <p className="mt-4">
                    <Figure amount={account.available} size="lg" />
                  </p>
                  <p className="type-eyebrow mt-1">Disponible</p>

                  {account.held.cents > 0 && (
                    <div className="mt-4">
                      <BalanceComposition
                        posted={account.posted}
                        held={account.held}
                        available={account.available}
                      />
                    </div>
                  )}

                  <p className="mt-auto pt-4 text-[0.75rem] text-ink-faint">
                    Abierta el {formatDate(account.created_at)}
                  </p>
                </Link>
              </li>
            ))}
          </ul>

          {me.data && (
            <p className="flex items-baseline gap-2 border-t border-rule pt-4 text-[0.875rem] text-ink-soft">
              <span>
                {accounts.length === 1 ? '1 cuenta' : `${accounts.length} cuentas`} · total
                disponible
              </span>
              <Figure amount={me.data.total_available} size="sm" />
            </p>
          )}
        </>
      )}
    </div>
  )
}

/**
 * Opening an account, in a dialog rather than on its own page.
 *
 * There is one decision to make — what kind — and a whole screen for one radio group
 * would be a navigation step charged for nothing. The dialog is shadcn's, so the focus
 * trap, the escape key and the return of focus to the button are handled rather than
 * approximated.
 */
function OpenAccountDialog({ held }: { held: number }) {
  const [open, setOpen] = useState(false)
  const [type, setType] = useState<AccountType>('savings')
  const openAccount = useOpenAccount()

  const submit = () => {
    openAccount.mutate(type, {
      onSuccess: () => {
        setOpen(false)
        setType('savings')
        openAccount.reset()
      },
    })
  }

  const failure = openAccount.error
  const message =
    failure instanceof ApiError
      ? failure.message
      : failure
        ? 'No pudimos abrir la cuenta. Inténtalo de nuevo.'
        : null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) openAccount.reset()
      }}
    >
      <DialogTrigger asChild>
        <Button className="bg-copper text-white hover:bg-copper/90">
          <PlusIcon aria-hidden="true" />
          Abrir cuenta
        </Button>
      </DialogTrigger>

      <DialogContent className="sm:max-w-[26rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.25rem]">Abre otra cuenta</DialogTitle>
          <DialogDescription>
            Se abre vacía y al instante. Ya tienes{' '}
            {held === 1 ? 'una cuenta' : `${held} cuentas`}.
          </DialogDescription>
        </DialogHeader>

        <fieldset className="grid gap-2" disabled={openAccount.isPending}>
          <legend className="mb-1 block text-[0.8125rem] font-medium text-ink-soft">
            Tipo de cuenta
          </legend>
          {TYPES.map((option) => (
            <label
              key={option.value}
              className={cn(
                'flex cursor-pointer items-baseline gap-3 rounded-[5px] border px-3.5 py-3 transition-colors',
                type === option.value
                  ? 'border-copper bg-copper/8'
                  : 'border-rule bg-paper-raised hover:border-ink-faint',
              )}
            >
              <input
                type="radio"
                name="account_type"
                value={option.value}
                checked={type === option.value}
                onChange={() => setType(option.value)}
                className="sr-only"
              />
              <span className="min-w-0 flex-1">
                <span className="block text-[0.9375rem] font-medium">{option.label}</span>
                <span className="mt-0.5 block text-[0.75rem] text-ink-faint">
                  {option.blurb}
                </span>
              </span>
              <span
                aria-hidden="true"
                className={cn(
                  'mt-1 size-3.5 shrink-0 rounded-full border',
                  type === option.value ? 'border-[5px] border-copper' : 'border-rule',
                )}
              />
            </label>
          ))}
        </fieldset>

        {message && (
          <p role="alert" className="text-[0.8125rem] text-danger-text">
            {message}
          </p>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => setOpen(false)}
            disabled={openAccount.isPending}
          >
            Cancelar
          </Button>
          <Button
            onClick={submit}
            disabled={openAccount.isPending}
            className="bg-copper text-white hover:bg-copper/90"
          >
            {openAccount.isPending && <Spinner />}
            Abrir cuenta
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
