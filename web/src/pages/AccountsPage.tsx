import { useState } from 'react'
import { ChevronRightIcon, PlusIcon, UploadIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { ApiError } from '@/api/client'
import type { Account, AccountType } from '@/api/types'
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
import { ImportStatementDialog } from '@/components/ImportStatementDialog'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { accountLabel, accountTypeAside, formatDate } from '@/lib/format'
import {
  useAccountTotal,
  useExternalAccounts,
  useHoldingsValue,
  useMe,
  useOpenAccount,
} from '@/lib/queries'
import { cn } from '@/lib/utils'
import { MAX_ALIAS } from '@/lib/validate'

const INSTITUTION_LABEL: Record<string, string> = {
  banco_general: 'Banco General',
  bac: 'BAC',
}

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
  const investmentAccountNumbers = accounts
    .filter((account) => account.account_type === 'investment')
    .map((account) => account.account_number)
  const { holdings } = useHoldingsValue(investmentAccountNumbers)

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        {/* Hidden on a phone, where the app bar above already carries this exact title
            and repeating it costs a row of the screen to say nothing new. */}
        <div className="hidden md:block">
          <p className="type-eyebrow">Cuentas</p>
          <h1 className="type-display mt-1.5 text-[1.75rem]">Tus cuentas</h1>
        </div>
        <div className="flex gap-2">
          <ImportStatementDialog
            trigger={
              <Button variant="outline">
                <UploadIcon aria-hidden="true" />
                Importar estado de cuenta
              </Button>
            }
          />
          <OpenAccountDialog held={accounts.length} />
        </div>
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
                <AccountCard account={account} />
              </li>
            ))}
          </ul>

          {me.data && (
            <div className="space-y-1 border-t border-rule pt-4 text-[0.875rem] text-ink-soft">
              <p className="flex items-baseline gap-2">
                <span>
                  {accounts.length === 1 ? '1 cuenta' : `${accounts.length} cuentas`} ·
                  total disponible
                </span>
                <Figure amount={me.data.total_available} size="sm" />
              </p>
              {holdings.cents > 0 && (
                <p className="flex items-baseline gap-2">
                  <span>Patrimonio total (incluye inversiones)</span>
                  <Figure
                    amount={{
                      cents: me.data.total_available.cents + holdings.cents,
                      formatted: (
                        (me.data.total_available.cents + holdings.cents) /
                        100
                      ).toFixed(2),
                      currency: 'USD',
                    }}
                    size="sm"
                    tone="copper"
                  />
                </p>
              )}
            </div>
          )}

          <ExternalAccountsSection />
        </>
      )}
    </div>
  )
}

/**
 * Credit cards imported from a statement — corebank never opened these and
 * never verifies them. A bank *account* statement doesn't appear here: it is
 * linked to one of the real accounts above instead (see
 * ImportStatementDialog), so this list is cards only.
 *
 * They still get the same card, the same grid, the same click-through to a
 * full detail page as a real account: for a customer who runs their
 * spending from a card, this is not a secondary list. What sets a row apart
 * is the eyebrow and the tone of its balance, not its size or its reach.
 */
function ExternalAccountsSection() {
  const externalAccounts = useExternalAccounts()
  const accounts = externalAccounts.data?.accounts ?? []

  if (externalAccounts.isLoading || accounts.length === 0) return null

  return (
    <div className="space-y-3 border-t border-rule pt-6">
      <h2 className="type-eyebrow">Tarjetas</h2>
      <ul className="grid gap-3 sm:grid-cols-2">
        {accounts.map((account) => (
          <li key={account.id}>
            <Link
              to={`/tarjetas/${account.id}`}
              className="card group flex h-full flex-col p-5 transition-colors hover:border-ink-faint"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-[0.9375rem] font-medium">
                    {account.display_name}
                  </p>
                  <p className="type-figure mt-0.5 text-[0.75rem] text-ink-faint">
                    {INSTITUTION_LABEL[account.institution] ?? account.institution}
                  </p>
                </div>
                <ChevronRightIcon
                  className="mt-0.5 size-4 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5"
                  aria-hidden="true"
                />
              </div>

              <p className="mt-4">
                <Figure amount={account.declared_balance} size="lg" tone="faint" />
              </p>
              <p className="type-eyebrow mt-1">Saldo declarado</p>

              <p className="mt-auto pt-4 text-[0.75rem] text-ink-faint">
                No verificado por corebank
              </p>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * One account's card in the list.
 *
 * Its own component, not inline in the `.map()` above, because a linked investment
 * account needs its own hook calls (`useInvestmentLink`/`usePortfolio`, via
 * `useAccountTotal`) to know whether to show its IBKR total instead of its cash
 * balance — and hooks can't be called a variable number of times per render.
 */
function AccountCard({ account }: { account: Account }) {
  const { total, cash, showTotal } = useAccountTotal(account)

  return (
    <Link
      to={`/cuentas/${account.account_number}`}
      className={cn(
        'card group flex h-full flex-col p-5 transition-colors hover:border-ink-faint',
        account.held.cents > 0 && 'border-hold/45',
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-[0.9375rem] font-medium">{accountLabel(account)}</p>
          <p className="type-figure mt-0.5 text-[0.75rem] text-ink-faint">
            {account.account_number}
            {accountTypeAside(account) && (
              <span className="font-sans"> · {accountTypeAside(account)}</span>
            )}
          </p>
        </div>
        <ChevronRightIcon
          className="mt-0.5 size-4 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5"
          aria-hidden="true"
        />
      </div>

      <p className="mt-4">
        <Figure
          amount={showTotal && total ? total : account.available}
          size="lg"
          tone={showTotal ? 'copper' : 'ink'}
        />
      </p>
      <p className="type-eyebrow mt-1">{showTotal ? 'Total' : 'Disponible'}</p>
      {showTotal && (
        <p className="type-figure mt-1 text-[0.75rem] text-ink-faint">
          Efectivo: <Figure amount={cash ?? account.available} size="sm" />
        </p>
      )}

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
  const [alias, setAlias] = useState('')
  const openAccount = useOpenAccount()

  const submit = () => {
    openAccount.mutate(
      { type, alias },
      {
        onSuccess: () => {
          setOpen(false)
          setType('savings')
          setAlias('')
          openAccount.reset()
        },
      },
    )
  }

  const failure = openAccount.error
  const message =
    failure instanceof ApiError
      ? // The per-field message when the alias is what the server objected to.
        // Without it the dialog answers a rejected alias with the generic envelope
        // message and never says which field to fix — and this form has two.
        (failure.fields?.alias ?? failure.message)
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
        <Button className="bg-copper text-primary-foreground hover:bg-copper/90">
          <PlusIcon aria-hidden="true" />
          Abrir cuenta
        </Button>
      </DialogTrigger>

      <DialogContent className="sm:max-w-[26rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.25rem]">
            Abre otra cuenta
          </DialogTitle>
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

        {/* Optional, and the help text says what happens if it is left alone. Offering
            it here rather than only afterwards is the point: a second savings account
            is indistinguishable from the first the moment it exists, so the name is
            most useful at the moment of opening. */}
        <div className="grid gap-1.5" aria-disabled={openAccount.isPending}>
          <label
            htmlFor="account-alias"
            className="text-[0.8125rem] font-medium text-ink-soft"
          >
            Alias de la cuenta{' '}
            <span className="font-normal text-ink-faint">(opcional)</span>
          </label>
          <Input
            id="account-alias"
            value={alias}
            onChange={(event) => setAlias(event.target.value)}
            placeholder="Gastos del mes"
            maxLength={MAX_ALIAS}
            disabled={openAccount.isPending}
            autoComplete="off"
          />
          <p className="text-[0.75rem] text-ink-faint">
            Para distinguirla de otras del mismo tipo. Sin alias se muestra como «
            {TYPES.find((option) => option.value === type)?.label}».
          </p>
        </div>

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
            className="bg-copper text-primary-foreground hover:bg-copper/90"
          >
            {openAccount.isPending && <Spinner />}
            Abrir cuenta
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
