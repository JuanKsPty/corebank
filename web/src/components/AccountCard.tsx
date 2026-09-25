import { ChevronRightIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import type { Account, Amount } from '@/api/types'
import { Figure } from '@/components/primitives'
import {
  accountLabel,
  accountTypeLabel,
  formatCivilDate,
  institutionLabel,
  money,
  numberShort,
} from '@/lib/format'
import { cn } from '@/lib/utils'

/** Builds an Amount from cents, for a sum computed in the browser. */
export function amountOf(cents: number): Amount {
  return { cents, formatted: (cents / 100).toFixed(2), currency: 'USD' }
}

/** The one figure an account is summed up by: owed for a card, cash plus holdings for a broker. */
export function headline(account: Account): { amount: Amount; label: string } {
  if (account.class === 'liability') {
    return { amount: account.owed ?? account.balance, label: 'Adeudado' }
  }
  if (account.type === 'brokerage') {
    return {
      amount: amountOf(account.balance.cents + (account.holdings?.cents ?? 0)),
      label: 'Total',
    }
  }
  return { amount: account.balance, label: 'Saldo' }
}

/** Whether the account agrees with its bank, in one line. */
export function reconciliationNote(account: Account): {
  text: string
  tone: 'ok' | 'warn' | 'none'
} {
  if (!account.anchored) {
    return account.movements > 0
      ? {
          text:
            account.class === 'liability'
              ? 'Sin saldo inicial: ingresa el monto adeudado de tu estado'
              : 'Sin saldo inicial',
          tone: 'warn',
        }
      : { text: 'Sin movimientos todavía', tone: 'none' }
  }
  const d = account.drift
  if (!d) return { text: 'Saldo de partida del banco', tone: 'none' }
  if (d.difference.cents === 0) {
    return { text: `Cuadra con el banco al ${formatCivilDate(d.as_of)}`, tone: 'ok' }
  }
  return {
    text: `No cuadra: el banco dice ${money(d.stated)} al ${formatCivilDate(d.as_of)} (diferencia ${money(d.difference)})`,
    tone: 'warn',
  }
}

export function AccountCard({ account }: { account: Account }) {
  const { amount, label } = headline(account)
  const note = reconciliationNote(account)

  return (
    <Link
      to={`/cuentas/${account.id}`}
      className={cn(
        'card group flex h-full flex-col p-5 transition-colors hover:border-ink-faint',
        note.tone === 'warn' && 'border-hold/45',
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-[0.9375rem] font-medium">{accountLabel(account)}</p>
          <p className="type-figure mt-0.5 text-[0.75rem] text-ink-faint">
            {institutionLabel(account.institution)} · {numberShort(account.external_number)}
            <span className="font-sans"> · {accountTypeLabel(account.type)}</span>
          </p>
        </div>
        <ChevronRightIcon
          className="mt-0.5 size-4 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5"
          aria-hidden="true"
        />
      </div>

      <p className="mt-4">
        <Figure amount={amount} size="lg" tone={account.anchored ? 'ink' : 'faint'} />
      </p>
      <p className="type-eyebrow mt-1">{label}</p>
      {account.type === 'brokerage' && (
        <p className="type-figure mt-1 text-[0.75rem] text-ink-faint">
          Efectivo {money(account.balance)} · Posiciones{' '}
          {money(account.holdings ?? amountOf(0))}
        </p>
      )}

      <p
        className={cn(
          'mt-auto pt-4 text-[0.75rem]',
          note.tone === 'warn'
            ? 'text-hold-text'
            : note.tone === 'ok'
              ? 'text-credit'
              : 'text-ink-faint',
        )}
      >
        {note.text}
      </p>
    </Link>
  )
}
