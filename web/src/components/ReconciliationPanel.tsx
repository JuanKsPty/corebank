import { useEffect, useRef, useState } from 'react'
import { ChevronDownIcon } from 'lucide-react'

import type {
  Account,
  ReconciliationCheck,
  ReconciliationLine,
  StatementCheck,
} from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { formatCivilDate, money } from '@/lib/format'
import { useReconciliation } from '@/lib/queries'
import { cn } from '@/lib/utils'

/**
 * "¿Cuadra con el banco?": every statement's own balances against what the
 * movements add up to, and — for a bank that prints a running balance — every line
 * with both balances side by side, opened at the line where they part.
 *
 * Nothing here corrects anything. A difference is shown so its cause can be found:
 * a statement never imported, a line missing from an export, a starting balance
 * typed wrong.
 */
export function ReconciliationPanel({ account }: { account: Account }) {
  const [open, setOpen] = useState(false)
  const rec = useReconciliation(account.id, { enabled: open })

  return (
    <section className="card">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between p-4 text-left"
        aria-expanded={open}
      >
        <span>
          <span className="type-eyebrow block">¿Cuadra con el banco?</span>
          <span className="mt-1 block text-[0.8125rem] text-ink-faint">
            Compara cada saldo que imprimió tu banco con lo que suman los movimientos.
          </span>
        </span>
        <ChevronDownIcon
          className={cn('size-4 text-ink-faint transition-transform', open && 'rotate-180')}
        />
      </button>

      {open && (
        <div className="space-y-4 border-t border-rule p-4">
          {rec.isLoading && <Skeleton className="h-24 w-full" />}
          {rec.data && (
            <>
              {!rec.data.anchor && (
                <p className="text-[0.8125rem] text-hold-text">
                  Sin saldo de partida: los saldos se calculan desde cero y no se pueden
                  comparar con el banco.
                </p>
              )}
              {rec.data.statements.length > 0 && (
                <ul className="space-y-2">
                  {rec.data.statements.map((s) => (
                    <StatementRow key={s.id} statement={s} />
                  ))}
                </ul>
              )}
              {rec.data.checks.filter(
                (c) => c.checkpoint.source === 'manual' || c.checkpoint.source === 'broker',
              ).length > 0 && (
                <ul className="space-y-1 text-[0.8125rem]">
                  {rec.data.checks
                    .filter(
                      (c) =>
                        c.checkpoint.source === 'manual' ||
                        c.checkpoint.source === 'broker',
                    )
                    .map((c) => (
                      <li key={c.checkpoint.id} className="flex flex-wrap gap-x-2">
                        <span className="text-ink-faint">
                          {c.checkpoint.source === 'broker' ? 'IBKR' : 'Ingresado por ti'}{' '}
                          al {formatCivilDate(c.checkpoint.as_of)}:
                        </span>
                        <CheckFigures check={c} />
                      </li>
                    ))}
                </ul>
              )}
              <Timeline lines={rec.data.lines} firstBreak={rec.data.first_break} />
            </>
          )}
        </div>
      )}
    </section>
  )
}

function StatementRow({ statement }: { statement: StatementCheck }) {
  return (
    <li className="rounded-[5px] border border-rule p-3 text-[0.8125rem]">
      <p className="flex flex-wrap items-baseline gap-2">
        <span className="font-medium">
          {formatCivilDate(statement.period_start)} al{' '}
          {formatCivilDate(statement.period_end)}
        </span>
        <span className="text-ink-faint">{statement.line_count} movimientos</span>
        {statement.gap && (
          <Badge variant="outline" className="border-hold/40 text-hold-text">
            Falta un período antes de este
          </Badge>
        )}
      </p>
      {statement.opening && (
        <p className="mt-1 flex flex-wrap gap-x-2">
          <span className="text-ink-faint">Saldo inicial:</span>
          <CheckFigures check={statement.opening} />
        </p>
      )}
      {statement.closing && (
        <p className="flex flex-wrap gap-x-2">
          <span className="text-ink-faint">Saldo final:</span>
          <CheckFigures check={statement.closing} />
        </p>
      )}
      {statement.warnings.map((w) => (
        <p key={w} className="mt-1 text-hold-text">
          {w}
        </p>
      ))}
    </li>
  )
}

function CheckFigures({ check }: { check: ReconciliationCheck }) {
  const ok = check.difference.cents === 0
  return (
    <span className={ok ? 'text-credit' : 'text-hold-text'}>
      banco {money(check.checkpoint.balance)} · calculado {money(check.computed)}
      {!ok && ` · diferencia ${money(check.difference)}`}
    </span>
  )
}

function Timeline({
  lines,
  firstBreak,
}: {
  lines: ReconciliationLine[]
  firstBreak?: string
}) {
  const breakRef = useRef<HTMLTableRowElement>(null)
  const withBank = lines.filter((l) => l.bank)

  useEffect(() => {
    breakRef.current?.scrollIntoView({ block: 'center' })
  }, [firstBreak])

  if (withBank.length === 0) {
    return (
      <p className="text-[0.75rem] text-ink-faint">
        Este tipo de archivo no trae el saldo línea por línea, así que la comparación es por
        estado de cuenta.
      </p>
    )
  }

  return (
    <div>
      <div className="flex items-baseline justify-between">
        <h3 className="type-eyebrow">Línea por línea</h3>
        {firstBreak ? (
          <span className="text-[0.75rem] text-hold-text">
            La diferencia empieza en la línea marcada.
          </span>
        ) : (
          <span className="text-[0.75rem] text-credit">Cada saldo del banco coincide.</span>
        )}
      </div>
      <div className="mt-2 max-h-80 overflow-y-auto rounded-[5px] border border-rule">
        <table className="w-full text-[0.75rem]">
          <thead className="sticky top-0 bg-paper-raised">
            <tr className="text-left text-ink-faint">
              <th className="px-2 py-1.5 font-normal">Fecha</th>
              <th className="px-2 py-1.5 font-normal">Movimiento</th>
              <th className="px-2 py-1.5 text-right font-normal">Monto</th>
              <th className="px-2 py-1.5 text-right font-normal">Calculado</th>
              <th className="px-2 py-1.5 text-right font-normal">Banco</th>
            </tr>
          </thead>
          <tbody className="type-figure">
            {lines.map((l) => {
              const isBreak = l.id === firstBreak
              const off = l.difference && l.difference.cents !== 0
              return (
                <tr
                  key={l.id}
                  ref={isBreak ? breakRef : undefined}
                  className={cn(
                    'border-t border-rule',
                    isBreak && 'bg-hold/15',
                    off && !isBreak && 'text-hold-text',
                  )}
                >
                  <td className="px-2 py-1">{l.booked_on}</td>
                  <td className="max-w-40 truncate px-2 py-1 font-sans">{l.description}</td>
                  <td className="px-2 py-1 text-right">{money(l.amount)}</td>
                  <td className="px-2 py-1 text-right">{money(l.computed)}</td>
                  <td className="px-2 py-1 text-right">{l.bank ? money(l.bank) : '—'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
