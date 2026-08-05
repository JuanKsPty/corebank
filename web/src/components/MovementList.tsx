import { ChevronRightIcon, FileTextIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import type { Transaction } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  counterpartyLabel,
  counterpartyShort,
  formatDayOfMonth,
  formatMonth,
  monthKey,
  movementLabel,
  sideOf,
  statusLabel,
  type Side,
} from '@/lib/format'
import { cn } from '@/lib/utils'
import { Figure } from './primitives'
import { COLUMNS } from './statement/columns'

/**
 * MovementList is a statement, ruled the way a ledger is.
 *
 * The debit and credit columns are separate and divided by a real rule, because that
 * is what a movement is: two sides, and only one of them yours. Netting them into one
 * signed column would be shorter and would throw away the structure the whole system
 * is built on.
 *
 * Movements are grouped by month, with the month stated once as a heading. That is how
 * a paper statement reads, and it has a second effect worth naming: a row under
 * "Agosto 2026" only needs to say "02", so the date column shrinks from twelve
 * characters to two and stops being the second column unable to hold its own contents.
 *
 * The columns collapse on a narrow screen — five columns of a statement on a phone is
 * unreadable — so each movement becomes a row with the amount aligned right and its
 * direction carried by colour and sign instead of by position.
 */
export function MovementList({
  movements,
  ownedAccounts,
  loading,
  emptyTitle = 'Aún no hay movimientos',
  emptyBody,
  emptyAction,
}: {
  movements: Transaction[]
  /** The customer's own account numbers, needed to know which side each falls on. */
  ownedAccounts: ReadonlySet<string>
  loading?: boolean
  emptyTitle?: string
  emptyBody?: React.ReactNode
  emptyAction?: React.ReactNode
}) {
  if (loading) return <MovementSkeleton />

  if (movements.length === 0) {
    return (
      <Empty className="border-0 bg-transparent">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <FileTextIcon />
          </EmptyMedia>
          <EmptyTitle className="type-display text-[1.0625rem]">{emptyTitle}</EmptyTitle>
          {emptyBody && <EmptyDescription>{emptyBody}</EmptyDescription>}
        </EmptyHeader>
        {emptyAction && <EmptyContent>{emptyAction}</EmptyContent>}
      </Empty>
    )
  }

  const groups = groupByMonth(movements)

  return (
    <>
      {/* The ruled statement, from small screens up. */}
      <div className="hidden sm:block">
        <Table className="ledger table-fixed">
          {/* Widths live here and nowhere else. A cell cannot disagree with its own
              column because a cell no longer declares a width at all. */}
          <colgroup>
            {COLUMNS.map((column) => (
              <col key={column.key} className={column.width} />
            ))}
          </colgroup>

          <TableHeader>
            <TableRow className="hover:bg-transparent">
              {COLUMNS.map((column) => (
                <TableHead
                  key={column.key}
                  className={cn(
                    'type-eyebrow h-auto px-3 pb-2 pt-0 align-bottom',
                    column.align === 'right' && 'text-right',
                    column.dividesSides && 'col-debit',
                  )}
                >
                  {column.label}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>

          <TableBody>
            {groups.map((group) => (
              <MonthGroup key={group.key} group={group} ownedAccounts={ownedAccounts} />
            ))}
          </TableBody>
        </Table>
      </div>

      {/* The same statement on a phone: no columns, and the month headings stay put
          while the movements under them scroll. */}
      <ul className="sm:hidden">
        {groups.map((group) => (
          <li key={group.key}>
            <h3 className="type-eyebrow sticky top-0 z-10 bg-paper-raised/95 py-2 backdrop-blur-sm">
              {group.label}
            </h3>
            <ul className="divide-y divide-rule">
              {group.items.map((movement) => (
                <PhoneRow
                  key={movement.id}
                  movement={movement}
                  side={sideOf(movement, ownedAccounts)}
                  ownedAccounts={ownedAccounts}
                />
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </>
  )
}

// ---------------------------------------------------------------------------
// Grouping
// ---------------------------------------------------------------------------

interface MonthGroupData {
  key: string
  label: string
  items: Transaction[]
}

/**
 * Groups movements into the months they occurred in, preserving order.
 *
 * The API returns movements already ordered by `occurred_at` descending — that
 * ordering is what its keyset pagination is built on — so grouping sequentially is
 * enough and no sort is needed. Sorting here would risk disagreeing with the cursor
 * and making a page boundary skip a row.
 */
function groupByMonth(movements: Transaction[]): MonthGroupData[] {
  const groups: MonthGroupData[] = []
  for (const movement of movements) {
    const key = monthKey(movement.occurred_at)
    const last = groups[groups.length - 1]
    if (last && last.key === key) {
      last.items.push(movement)
    } else {
      groups.push({ key, label: formatMonth(movement.occurred_at), items: [movement] })
    }
  }
  return groups
}

// ---------------------------------------------------------------------------
// The table
// ---------------------------------------------------------------------------

function MonthGroup({
  group,
  ownedAccounts,
}: {
  group: MonthGroupData
  ownedAccounts: ReadonlySet<string>
}) {
  return (
    <>
      <TableRow className="hover:bg-transparent">
        <TableCell
          colSpan={COLUMNS.length}
          className="type-eyebrow border-b border-rule bg-paper-sunken/60 px-3 py-1.5"
        >
          {group.label}
        </TableCell>
      </TableRow>

      {group.items.map((movement) => {
        const side = sideOf(movement, ownedAccounts)
        const settled = movement.status === 'completed'

        return (
          <TableRow key={movement.id} className="border-b border-rule">
            <TableCell className="type-figure px-3 py-2.5 align-baseline text-[0.8125rem] text-ink-faint">
              {formatDayOfMonth(movement.occurred_at)}
            </TableCell>

            {/* overflow-hidden is the guarantee, not the layout: min-w-0 below makes
                the description truncate, and this makes anything that still does not
                fit — a status badge in a narrow column — clip at the column edge
                instead of painting over the neighbouring one. */}
            <TableCell className="overflow-hidden px-3 py-2.5 align-baseline whitespace-normal">
              <div className="flex min-w-0 items-baseline gap-2">
                {/* min-w-0 is what lets the truncate engage: without it a flex child
                    refuses to shrink below its own content. */}
                <span className="min-w-0 truncate">
                  {movement.description || movementLabel(movement.kind)}
                </span>
                <StatusMark movement={movement} />
              </div>
              <span className="mt-0.5 block truncate text-[0.6875rem] text-ink-faint">
                {movementLabel(movement.kind)}
                {movement.origin === 'chat' && ' · vía asistente'}
              </span>
            </TableCell>

            <TableCell className="type-figure overflow-hidden px-3 py-2.5 align-baseline text-[0.75rem] text-ink-soft">
              <Counterparty movement={movement} side={side} ownedAccounts={ownedAccounts} />
            </TableCell>

            <TableCell className="col-debit px-3 py-2.5 text-right align-baseline">
              {side === 'debit' && (
                <Figure
                  amount={movement.amount}
                  size="sm"
                  tone={settled ? 'ink' : 'faint'}
                  className={cn(!settled && 'line-through decoration-1')}
                />
              )}
            </TableCell>

            <TableCell className="px-3 py-2.5 text-right align-baseline">
              {side === 'credit' && (
                <Figure
                  amount={movement.amount}
                  size="sm"
                  tone={settled ? 'credit' : 'faint'}
                  className={cn(!settled && 'line-through decoration-1')}
                />
              )}
            </TableCell>
          </TableRow>
        )
      })}
    </>
  )
}

// ---------------------------------------------------------------------------
// The phone row
// ---------------------------------------------------------------------------

function PhoneRow({
  movement,
  side,
  ownedAccounts,
}: {
  movement: Transaction
  side: Side
  ownedAccounts: ReadonlySet<string>
}) {
  const settled = movement.status === 'completed'
  const other = side === 'debit' ? movement.to_account : movement.from_account
  // A chevron is a promise that tapping goes somewhere. It is only drawn when the
  // counterparty is an account the customer holds and can actually open.
  const destination = other && ownedAccounts.has(other) ? `/cuentas/${other}` : undefined

  const body = (
    <>
      <span className="type-figure w-6 shrink-0 pt-0.5 text-[0.8125rem] text-ink-faint">
        {formatDayOfMonth(movement.occurred_at)}
      </span>

      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 truncate text-[0.875rem]">
            {movement.description || movementLabel(movement.kind)}
          </span>
          <StatusMark movement={movement} />
        </span>
        <span className="mt-0.5 block text-[0.6875rem] text-ink-faint">
          {movementLabel(movement.kind)}
          {' · '}
          {counterpartyShort(other)}
          {movement.origin === 'chat' && ' · asistente'}
        </span>
      </span>

      <Figure
        amount={movement.amount}
        size="sm"
        tone={!settled ? 'faint' : side === 'credit' ? 'credit' : 'ink'}
        className={cn('shrink-0', !settled && 'line-through decoration-1')}
      />

      {destination ? (
        <ChevronRightIcon
          className="mt-0.5 size-4 shrink-0 text-ink-faint"
          aria-hidden="true"
        />
      ) : (
        <span className="size-4 shrink-0" aria-hidden="true" />
      )}
    </>
  )

  return (
    <li>
      {destination ? (
        <Link
          to={destination}
          className="flex items-baseline gap-3 py-3 transition-colors active:bg-paper-sunken"
        >
          {body}
        </Link>
      ) : (
        <div className="flex items-baseline gap-3 py-3">{body}</div>
      )}
    </li>
  )
}

// ---------------------------------------------------------------------------
// Cells
// ---------------------------------------------------------------------------

/** Only a movement that is not simply done gets a mark. */
function StatusMark({ movement }: { movement: Transaction }) {
  if (movement.status === 'completed') return null

  const label = statusLabel(movement.status)

  if (movement.status === 'pending') {
    return (
      <Badge
        variant="outline"
        className="type-figure shrink-0 border-hold/35 bg-hold/12 text-hold"
      >
        {label}
      </Badge>
    )
  }
  if (movement.status === 'failed') {
    return (
      <Badge
        variant="outline"
        className="type-figure shrink-0 border-danger/30 bg-danger/10 text-danger-text"
      >
        {label}
      </Badge>
    )
  }
  return (
    <Badge variant="secondary" className="type-figure shrink-0 text-ink-soft">
      {label}
    </Badge>
  )
}

/**
 * Counterparty names the other side of the movement — the account that is not the
 * customer's, which is the only one worth showing.
 *
 * Shown as its last four digits, which is what identifies an account to a person and
 * what the account cards already display. The whole number is in the `title` at every
 * width, and from 3xl up — where the column has room for all nineteen characters — it
 * is shown outright. Both forms are in the DOM, so the full number is what a screen
 * reader announces regardless of viewport.
 */
function Counterparty({
  movement,
  side,
  ownedAccounts,
}: {
  movement: Transaction
  side: Side
  ownedAccounts: ReadonlySet<string>
}) {
  const other = side === 'debit' ? movement.to_account : movement.from_account
  const full = counterpartyLabel(other)
  const short = counterpartyShort(other)

  const label = (
    <>
      <span className="3xl:hidden" aria-hidden="true">
        {short}
      </span>
      <span className="hidden 3xl:inline">{full}</span>
      <span className="sr-only 3xl:hidden">{full}</span>
    </>
  )

  // A move between two of the customer's own accounts links through, since they can
  // actually go and look at it.
  if (other && ownedAccounts.has(other)) {
    return (
      <Link
        to={`/cuentas/${other}`}
        title={full}
        className="block truncate underline decoration-rule underline-offset-2 hover:decoration-copper"
      >
        {label}
      </Link>
    )
  }
  return (
    <span title={full} className="block truncate">
      {label}
    </span>
  )
}

function MovementSkeleton() {
  return (
    <div className="space-y-3 py-2" aria-hidden="true">
      {Array.from({ length: 5 }, (_, index) => (
        <div key={index} className="flex items-center gap-4">
          <Skeleton className="h-3 w-8" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-20" />
        </div>
      ))}
    </div>
  )
}
