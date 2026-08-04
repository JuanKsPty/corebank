import { Link } from 'react-router-dom'

import type { Transaction } from '@/api/types'
import {
  counterpartyLabel,
  formatDate,
  movementLabel,
  sideOf,
  statusLabel,
} from '@/lib/format'
import { Badge, EmptyState, Figure, SkeletonLine, cx } from './primitives'

/**
 * MovementList is a statement, ruled the way a ledger is.
 *
 * The debit and credit columns are separate and divided by a real rule, because
 * that is what a movement is: two sides, and only one of them yours. Netting them
 * into one signed column would be shorter and would throw away the structure the
 * whole system is built on.
 *
 * The columns collapse on a narrow screen — a table with five columns on a phone is
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
      <EmptyState title={emptyTitle} action={emptyAction}>
        {emptyBody}
      </EmptyState>
    )
  }

  return (
    <>
      {/* The ruled table, from small screens up.
          The scroll container is not optional: five columns of a statement have a
          real minimum width, and without it the credit column silently falls off
          the right edge of the card at narrower viewports. */}
      <div className="hidden overflow-x-auto sm:block">
      <table className="ledger w-full min-w-[34rem] table-fixed">
        <thead>
          <tr>
            <th className="w-[5.75rem]">Fecha</th>
            <th>Concepto</th>
            <th className="w-[8rem]">Contraparte</th>
            <th className="col-debit w-[6.5rem] text-right">Cargo</th>
            <th className="w-[6.5rem] text-right">Abono</th>
          </tr>
        </thead>
        <tbody>
          {movements.map((movement) => {
            const side = sideOf(movement, ownedAccounts)
            const settled = movement.status === 'completed'

            return (
              <tr key={movement.id}>
                <td className="type-figure whitespace-nowrap text-[0.75rem] text-ink-faint">
                  {formatDate(movement.occurred_at)}
                </td>
                <td>
                  <div className="flex items-baseline gap-2">
                    {/* Fixed columns mean a long description ellipsizes rather than
                        widening the table past its card. */}
                    <span className="min-w-0 truncate">{movement.description || movementLabel(movement.kind)}</span>
                    <StatusMark movement={movement} />
                  </div>
                  <span className="mt-0.5 block text-[0.6875rem] text-ink-faint">
                    {movementLabel(movement.kind)}
                    {movement.origin === 'chat' && ' · vía asistente'}
                  </span>
                </td>
                <td className="type-figure whitespace-nowrap text-[0.75rem] text-ink-soft">
                  <Counterparty movement={movement} side={side} ownedAccounts={ownedAccounts} />
                </td>
                <td className="col-debit text-right">
                  {side === 'debit' && (
                    <Figure
                      amount={movement.amount}
                      size="sm"
                      tone={settled ? 'ink' : 'faint'}
                      className={cx(!settled && 'line-through decoration-1')}
                    />
                  )}
                </td>
                <td className="text-right">
                  {side === 'credit' && (
                    <Figure
                      amount={movement.amount}
                      size="sm"
                      tone={settled ? 'credit' : 'faint'}
                      className={cx(!settled && 'line-through decoration-1')}
                    />
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      </div>

      {/* The same statement on a phone. */}
      <ul className="divide-y divide-rule sm:hidden">
        {movements.map((movement) => {
          const side = sideOf(movement, ownedAccounts)
          const settled = movement.status === 'completed'

          return (
            <li key={movement.id} className="flex items-baseline gap-3 py-3">
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <p className="min-w-0 truncate text-[0.875rem]">
                    {movement.description || movementLabel(movement.kind)}
                  </p>
                  <StatusMark movement={movement} />
                </div>
                <p className="type-figure mt-0.5 text-[0.6875rem] text-ink-faint">
                  {formatDate(movement.occurred_at)} · {movementLabel(movement.kind)}
                  {movement.origin === 'chat' && ' · asistente'}
                </p>
              </div>
              <Figure
                amount={movement.amount}
                size="sm"
                tone={!settled ? 'faint' : side === 'credit' ? 'credit' : 'ink'}
                className={cx('shrink-0', !settled && 'line-through decoration-1')}
              />
            </li>
          )
        })}
      </ul>
    </>
  )
}

/** Only a movement that is not simply done gets a mark. */
function StatusMark({ movement }: { movement: Transaction }) {
  if (movement.status === 'completed') return null

  const tones = {
    pending: 'hold',
    failed: 'danger',
    voided: 'neutral',
    expired: 'neutral',
  } as const

  return <Badge tone={tones[movement.status] ?? 'neutral'}>{statusLabel(movement.status)}</Badge>
}

/**
 * Counterparty names the other side of the movement — the account that is not the
 * customer's, which is the only one worth showing.
 */
function Counterparty({
  movement,
  side,
  ownedAccounts,
}: {
  movement: Transaction
  side: 'debit' | 'credit'
  ownedAccounts: ReadonlySet<string>
}) {
  const other = side === 'debit' ? movement.to_account : movement.from_account
  const label = counterpartyLabel(other)

  // A move between two of the customer's own accounts links through, since they
  // can actually go and look at it.
  if (other && ownedAccounts.has(other)) {
    return (
      <Link to={`/cuentas/${other}`} className="underline decoration-rule underline-offset-2 hover:decoration-copper">
        {label}
      </Link>
    )
  }
  return <>{label}</>
}

function MovementSkeleton() {
  return (
    <div className="space-y-3 py-2" aria-hidden="true">
      {Array.from({ length: 5 }, (_, index) => (
        <div key={index} className="flex items-center gap-4">
          <SkeletonLine className="w-16" />
          <SkeletonLine className="flex-1" />
          <SkeletonLine className="w-20" />
        </div>
      ))}
    </div>
  )
}
