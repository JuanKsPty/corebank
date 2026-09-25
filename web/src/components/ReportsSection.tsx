import { useState } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { ApiError } from '@/api/client'
import type { Account, CategoryNode, TransferSuggestion } from '@/api/types'
import { categoryNames } from '@/components/EntryList'
import { Figure } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  accountLabel,
  formatCivilDayMonth,
  formatCivilMonth,
  money,
  todayCivil,
} from '@/lib/format'
import {
  useCategoryTotals,
  useDecideTransfer,
  useFlow,
  useTransferSuggestions,
} from '@/lib/queries'

/** The first day of the month `back` months before today's. */
function monthStart(back: number): string {
  const [year = 0, month = 1] = todayCivil().split('-').map(Number)
  const d = new Date(year, month - 1 - back, 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

/**
 * What the owner spent this month, by category. Transfers between their own
 * accounts and trades are not spending and never appear here.
 */
export function SpendingByCategory({ categories }: { categories?: CategoryNode[] }) {
  const [month, setMonth] = useState(0)
  const from = monthStart(month)
  const to = month === 0 ? todayCivil() : lastDay(from)
  const totals = useCategoryTotals({ from, to, kind: 'spend' })
  const names = categoryNames(categories)

  const rows = (totals.data?.totals ?? []).filter((t) => t.total.cents > 0)
  const sum = rows.reduce((acc, t) => acc + t.total.cents, 0)
  const top = rows[0]?.total.cents ?? 1

  return (
    <section className="card p-4">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="type-eyebrow">Gasto por categoría</h2>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="sm" onClick={() => setMonth((m) => m + 1)}>
            ‹
          </Button>
          <span className="min-w-28 text-center text-[0.8125rem] text-ink-soft">
            {formatCivilMonth(from)}
          </span>
          <Button
            variant="ghost"
            size="sm"
            disabled={month === 0}
            onClick={() => setMonth((m) => Math.max(0, m - 1))}
          >
            ›
          </Button>
        </div>
      </div>

      {totals.isLoading ? (
        <div className="mt-3 space-y-2">
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      ) : rows.length === 0 ? (
        <p className="mt-3 text-[0.875rem] text-ink-soft">No hay gastos en este mes.</p>
      ) : (
        <>
          <p className="mt-2">
            <Figure amount={sum} size="lg" />
          </p>
          <ul className="mt-3 space-y-2.5">
            {rows.map((row) => (
              <li key={row.category_id ?? 'none'} className="space-y-1">
                <div className="flex items-baseline justify-between gap-3 text-[0.875rem]">
                  <span className="truncate">
                    {row.category_id
                      ? (names.get(row.category_id) ?? 'Categoría')
                      : 'Sin categoría'}
                    <span className="ml-1.5 text-[0.75rem] text-ink-faint">
                      {row.count}
                    </span>
                  </span>
                  <Figure amount={row.total} size="sm" />
                </div>
                <div className="h-1.5 rounded-full bg-rule">
                  <div
                    className="h-1.5 rounded-full bg-copper"
                    style={{ width: `${Math.max(2, (row.total.cents / top) * 100)}%` }}
                  />
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  )
}

function lastDay(first: string): string {
  const [year = 0, month = 1] = first.split('-').map(Number)
  const d = new Date(year, month, 0)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** Money in and out of the owner's accounts per month, transfers left out. */
export function MonthlyFlow() {
  const flow = useFlow({ from: monthStart(5), to: todayCivil(), granularity: 'month' })
  const data = (flow.data?.points ?? []).map((p) => ({
    label: formatCivilMonth(p.day).split(' ')[0]?.slice(0, 3) ?? p.day,
    in: p.in.cents / 100,
    out: p.out.cents / 100,
  }))

  return (
    <section className="card p-4">
      <h2 className="type-eyebrow">Entradas y salidas</h2>
      {flow.isLoading ? (
        <Skeleton className="mt-3 h-40 w-full" />
      ) : data.length === 0 ? (
        <p className="mt-3 text-[0.875rem] text-ink-soft">Aún no hay movimientos.</p>
      ) : (
        <div className="mt-3 h-48">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
              <CartesianGrid vertical={false} stroke="var(--color-rule)" />
              <XAxis dataKey="label" tickLine={false} axisLine={false} fontSize={12} />
              <YAxis tickLine={false} axisLine={false} fontSize={11} width={48} />
              <Tooltip
                formatter={(value: number, name: string) => [
                  money(Math.round(value * 100)),
                  name === 'in' ? 'Entró' : 'Salió',
                ]}
              />
              <Bar dataKey="in" fill="var(--color-credit)" radius={[3, 3, 0, 0]} />
              <Bar dataKey="out" fill="var(--color-copper)" radius={[3, 3, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
    </section>
  )
}

/**
 * Pairs of movements that look like one transfer between the owner's accounts
 * but were not unambiguous enough to confirm alone. Confirming one takes both
 * out of spending and income.
 */
export function TransferSuggestions({ accounts }: { accounts: Account[] }) {
  const suggestions = useTransferSuggestions()
  const list = suggestions.data?.suggestions ?? []
  if (list.length === 0) return null

  const byId = new Map(accounts.map((a) => [a.id, a]))

  return (
    <section className="card p-4">
      <h2 className="type-eyebrow">¿Son transferencias entre tus cuentas?</h2>
      <ul className="mt-3 divide-y divide-rule">
        {list.map((s) => (
          <SuggestionRow key={`${s.out.id}:${s.in.id}`} suggestion={s} accounts={byId} />
        ))}
      </ul>
    </section>
  )
}

function SuggestionRow({
  suggestion,
  accounts,
}: {
  suggestion: TransferSuggestion
  accounts: Map<string, Account>
}) {
  const decide = useDecideTransfer()
  const side = (label: string, entry: TransferSuggestion['out']) => {
    const account = accounts.get(entry.account_id)
    return (
      <p className="flex items-baseline justify-between gap-3 text-[0.875rem]">
        <span className="min-w-0 truncate">
          <span className="text-ink-faint">{label}</span>{' '}
          {account ? accountLabel(account) : 'Cuenta'} ·{' '}
          {formatCivilDayMonth(entry.booked_on)} · {entry.description}
        </span>
        <Figure amount={entry.amount} size="sm" />
      </p>
    )
  }
  const send = (confirm: boolean) =>
    decide.mutate({
      out_entry_id: suggestion.out.id,
      in_entry_id: suggestion.in.id,
      confirm,
    })

  return (
    <li className="space-y-1.5 py-3">
      {side('Sale de', suggestion.out)}
      {side('Entra a', suggestion.in)}
      <div className="flex gap-2 pt-1">
        <Button size="sm" disabled={decide.isPending} onClick={() => send(true)}>
          Confirmar
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={decide.isPending}
          onClick={() => send(false)}
        >
          No es
        </Button>
      </div>
      {decide.error && (
        <p className="text-[0.8125rem] text-destructive">
          {decide.error instanceof ApiError ? decide.error.message : 'No se pudo guardar.'}
        </p>
      )}
    </li>
  )
}
