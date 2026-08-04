import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { Flow } from '@/api/types'
import { formatDayMonth, money } from '@/lib/format'

/**
 * FlowChart shows money in and money out per day.
 *
 * Two bars from a shared baseline rather than a single net line, because the two
 * directions are different facts: netting them to one number hides a month where a
 * lot came in and a lot went out, which is exactly the month worth looking at.
 *
 * The colours are the ones the rest of the interface already uses for the two sides
 * of an account, so the chart needs no legend to be read — inflow is the credit
 * green, outflow the ink used for debits.
 */
export function FlowChart({ flow }: { flow: Flow }) {
  const data = flow.points.map((point) => ({
    day: point.day,
    label: formatDayMonth(point.day),
    // Recharts needs numbers, and this is the one place a monetary value becomes
    // one. It is for pixel heights, never for arithmetic the customer sees: every
    // figure in the tooltip is formatted from the original cents.
    inCents: point.in.cents,
    outCents: point.out.cents,
  }))

  return (
    <div className="h-[180px] w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -18 }} barGap={1}>
          <CartesianGrid vertical={false} stroke="var(--color-rule)" strokeDasharray="2 3" />
          <XAxis
            dataKey="label"
            tick={{ fill: 'var(--color-ink-faint)', fontSize: 10, fontFamily: 'Spline Sans Mono' }}
            axisLine={{ stroke: 'var(--color-rule)' }}
            tickLine={false}
            // With thirty days on a narrow chart every label would overlap, so
            // recharts is left to thin them out.
            interval="preserveStartEnd"
            minTickGap={24}
          />
          <YAxis
            tick={{ fill: 'var(--color-ink-faint)', fontSize: 10, fontFamily: 'Spline Sans Mono' }}
            axisLine={false}
            tickLine={false}
            width={62}
            tickFormatter={(cents: number) => compactMoney(cents)}
          />
          <Tooltip
            content={<FlowTooltip />}
            cursor={{ fill: 'var(--color-paper-sunken)' }}
          />
          <Bar dataKey="inCents" name="Entradas" fill="var(--color-credit)" radius={[2, 2, 0, 0]} maxBarSize={14} />
          <Bar dataKey="outCents" name="Salidas" fill="var(--color-ink)" fillOpacity={0.55} radius={[2, 2, 0, 0]} maxBarSize={14} />
        </BarChart>
      </ResponsiveContainer>

      <div className="mt-1 flex items-center justify-end gap-4 text-[0.6875rem] text-ink-faint">
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-[1px] bg-credit" /> Entradas
        </span>
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-[1px] bg-ink/55" /> Salidas
        </span>
      </div>
    </div>
  )
}

interface TooltipPayload {
  active?: boolean
  payload?: Array<{ payload: { day: string; inCents: number; outCents: number } }>
}

function FlowTooltip({ active, payload }: TooltipPayload) {
  const point = payload?.[0]?.payload
  if (!active || !point) return null

  return (
    <div className="rounded-[4px] border border-rule bg-paper-raised px-2.5 py-2 text-[0.75rem] shadow-sm">
      <p className="type-figure text-ink-faint">{formatDayMonth(point.day)}</p>
      <p className="mt-1 flex items-center justify-between gap-3">
        <span className="text-credit">Entró</span>
        <span className="type-figure">{money(point.inCents)}</span>
      </p>
      <p className="flex items-center justify-between gap-3">
        <span className="text-ink-soft">Salió</span>
        <span className="type-figure">{money(point.outCents)}</span>
      </p>
    </div>
  )
}

/**
 * compactMoney keeps the axis narrow: $1.2k rather than $1,234.56.
 *
 * One decimal in the thousands range, not zero. Rounding to whole thousands made
 * ticks at 1,200 and 1,600 both render as different-looking labels for
 * near-neighbours — an axis whose numbers did not appear to increase evenly.
 */
function compactMoney(cents: number): string {
  const dollars = cents / 100
  if (dollars >= 1_000_000) return `$${trim(dollars / 1_000_000)}M`
  if (dollars >= 1_000) return `$${trim(dollars / 1_000)}k`
  return `$${Math.round(dollars)}`
}

function trim(value: number): string {
  return value.toFixed(1).replace(/\.0$/, '')
}
