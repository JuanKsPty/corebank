import { useState } from 'react'
import { FileTextIcon } from 'lucide-react'

import type { Account, CategoryNode, Entry } from '@/api/types'
import { EntryDialog } from '@/components/EntryDialog'
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
import { accountLabel, entryKindLabel, formatCivilMonth, numberShort } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Figure } from './primitives'
import { COLUMNS } from './statement/columns'

/**
 * EntryList is a statement: every line the owner's banks printed, grouped by month.
 *
 * Money leaving sits in the Cargo column and money arriving in Abono, the way a
 * bank statement reads. A transfer between the owner's own accounts is marked,
 * because it is neither spending nor income. Tapping a line opens it, to file it
 * under a category, note it, or mark it as a transfer.
 */
export function EntryList({
  entries,
  accounts,
  categories,
  loading,
  showAccount = true,
  emptyTitle = 'Aún no hay movimientos',
  emptyBody,
  emptyAction,
}: {
  entries: Entry[]
  accounts: Account[]
  categories?: CategoryNode[]
  loading?: boolean
  /** Off on an account's own page, where every line is the same account. */
  showAccount?: boolean
  emptyTitle?: string
  emptyBody?: React.ReactNode
  emptyAction?: React.ReactNode
}) {
  const [open, setOpen] = useState<Entry | null>(null)

  if (loading) return <EntrySkeleton />

  if (entries.length === 0) {
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

  const byId = new Map(accounts.map((a) => [a.id, a]))
  const categoryName = categoryNames(categories)
  const groups = groupByMonth(entries)

  const describe = (entry: Entry) => {
    const parts = [entryKindLabel(entry.kind)]
    const category = entry.category_id ? categoryName.get(entry.category_id) : undefined
    if (category && entry.kind !== 'transfer') parts.push(category)
    if (entry.note) parts.push(entry.note)
    return parts.join(' · ')
  }

  return (
    <>
      <div className="hidden sm:block">
        <Table className="ledger table-fixed">
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
                  {column.key === 'account' && !showAccount ? '' : column.label}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((group) => (
              <GroupRows
                key={group.key}
                label={group.label}
                entries={group.items}
                accountOf={(e) => byId.get(e.account_id)}
                showAccount={showAccount}
                describe={describe}
                onOpen={setOpen}
              />
            ))}
          </TableBody>
        </Table>
      </div>

      <ul className="sm:hidden">
        {groups.map((group) => (
          <li key={group.key}>
            <h3 className="type-eyebrow sticky top-0 z-10 bg-paper-raised/95 py-2 backdrop-blur-sm">
              {group.label}
            </h3>
            <ul className="divide-y divide-rule">
              {group.items.map((entry) => {
                const account = byId.get(entry.account_id)
                return (
                  <li key={entry.id}>
                    <button
                      type="button"
                      onClick={() => setOpen(entry)}
                      className="flex w-full items-baseline gap-3 py-3 text-left transition-colors active:bg-paper-sunken"
                    >
                      <span className="type-figure w-6 shrink-0 pt-0.5 text-[0.8125rem] text-ink-faint">
                        {entry.booked_on.slice(8, 10)}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-[0.875rem]">
                          {entry.description || entryKindLabel(entry.kind)}
                        </span>
                        <span className="mt-0.5 block truncate text-[0.6875rem] text-ink-faint">
                          {describe(entry)}
                          {showAccount && account && ` · ${accountLabel(account)}`}
                        </span>
                      </span>
                      <Figure
                        amount={entry.amount}
                        size="sm"
                        tone={entry.amount.cents > 0 ? 'credit' : 'ink'}
                        className="shrink-0"
                      />
                    </button>
                  </li>
                )
              })}
            </ul>
          </li>
        ))}
      </ul>

      {open && (
        <EntryDialog
          entry={open}
          account={byId.get(open.account_id)}
          categories={categories ?? []}
          onClose={() => setOpen(null)}
        />
      )}
    </>
  )
}

function GroupRows({
  label,
  entries,
  accountOf,
  showAccount,
  describe,
  onOpen,
}: {
  label: string
  entries: Entry[]
  accountOf: (e: Entry) => Account | undefined
  showAccount: boolean
  describe: (e: Entry) => string
  onOpen: (e: Entry) => void
}) {
  return (
    <>
      <TableRow className="hover:bg-transparent">
        <TableCell
          colSpan={COLUMNS.length}
          className="type-eyebrow border-b border-rule bg-paper-sunken/60 px-3 py-1.5"
        >
          {label}
        </TableCell>
      </TableRow>
      {entries.map((entry) => {
        const account = accountOf(entry)
        const out = entry.amount.cents < 0
        const magnitude = { ...entry.amount, cents: Math.abs(entry.amount.cents) }
        return (
          <TableRow
            key={entry.id}
            className="cursor-pointer border-b border-rule"
            onClick={() => onOpen(entry)}
          >
            <TableCell className="type-figure px-3 py-2.5 align-baseline text-[0.8125rem] text-ink-faint">
              {entry.booked_on.slice(8, 10)}
            </TableCell>
            <TableCell className="overflow-hidden px-3 py-2.5 align-baseline whitespace-normal">
              <div className="flex min-w-0 items-baseline gap-2">
                <span className="min-w-0 truncate">
                  {entry.description || entryKindLabel(entry.kind)}
                </span>
                {entry.kind === 'transfer' && (
                  <Badge variant="secondary" className="type-figure shrink-0 text-ink-soft">
                    Transferencia
                  </Badge>
                )}
              </div>
              <span className="mt-0.5 block truncate text-[0.6875rem] text-ink-faint">
                {describe(entry)}
              </span>
            </TableCell>
            <TableCell className="type-figure overflow-hidden px-3 py-2.5 align-baseline text-[0.75rem] text-ink-soft">
              {showAccount && account && (
                <span className="block truncate" title={account.external_number}>
                  {accountLabel(account)} {numberShort(account.external_number)}
                </span>
              )}
            </TableCell>
            <TableCell className="col-debit px-3 py-2.5 text-right align-baseline">
              {out && <Figure amount={magnitude} size="sm" />}
            </TableCell>
            <TableCell className="px-3 py-2.5 text-right align-baseline">
              {!out && <Figure amount={magnitude} size="sm" tone="credit" />}
            </TableCell>
          </TableRow>
        )
      })}
    </>
  )
}

interface MonthGroup {
  key: string
  label: string
  items: Entry[]
}

/** Groups entries by month, keeping the API's newest-first order. */
function groupByMonth(entries: Entry[]): MonthGroup[] {
  const groups: MonthGroup[] = []
  for (const entry of entries) {
    const key = entry.booked_on.slice(0, 7)
    const last = groups[groups.length - 1]
    if (last && last.key === key) last.items.push(entry)
    else groups.push({ key, label: formatCivilMonth(entry.booked_on), items: [entry] })
  }
  return groups
}

/** Every category's name by id, children as "Padre › Hija". */
export function categoryNames(tree: CategoryNode[] | undefined): Map<string, string> {
  const names = new Map<string, string>()
  for (const node of tree ?? []) {
    names.set(node.id, node.name)
    for (const child of node.children) names.set(child.id, `${node.name} › ${child.name}`)
  }
  return names
}

function EntrySkeleton() {
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
