import { Link, useParams } from 'react-router-dom'

import { Figure } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDate } from '@/lib/format'
import {
  useCategories,
  useExternalAccounts,
  useExternalTransactions,
  useImportHistory,
  useSetExternalTransactionCategory,
  useSpendByCategory,
} from '@/lib/queries'

const INSTITUTION_LABEL: Record<string, string> = {
  banco_general: 'Banco General',
  bac: 'BAC',
}

/**
 * One imported account, in full — its own page rather than an inline expand.
 *
 * A credit card is how some customers actually run their money day to day, not
 * a footnote on the way to a bank account, so it earns the same kind of page a
 * real account gets: a header, its movements, and its history — even though
 * everything on it is declared, not verified by corebank.
 */
export function ImportedAccountPage() {
  const { id = '' } = useParams()
  const accounts = useExternalAccounts()
  const account = accounts.data?.accounts.find((candidate) => candidate.id === id)

  if (!accounts.isLoading && !account) {
    return (
      <div className="mx-auto max-w-lg py-16 text-center">
        <p className="type-eyebrow">Cuenta no encontrada</p>
        <h1 className="type-display mt-2 text-2xl">Esta cuenta importada no existe</h1>
        <Button asChild variant="outline" className="mt-5">
          <Link to="/cuentas">Ir a Cuentas</Link>
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <nav aria-label="Ruta" className="text-[0.8125rem] text-ink-faint">
        <Link to="/cuentas" className="hover:text-ink hover:underline">
          Cuentas
        </Link>
        <span aria-hidden="true" className="px-1.5">
          /
        </span>
        <span className="type-figure text-ink-soft">{account?.display_name}</span>
      </nav>

      <header>
        {account ? (
          <>
            <p className="type-eyebrow">
              {INSTITUTION_LABEL[account.institution] ?? account.institution} ·{' '}
              {account.account_number}
            </p>
            <h1 className="mt-1.5">
              <span className="sr-only">Saldo declarado: </span>
              <Figure
                amount={account.declared_balance}
                size="display"
                className="type-display"
                tone="faint"
              />
            </h1>
            <p className="type-figure mt-1 text-[0.875rem] text-ink-soft">
              Declarado a partir de lo importado — no verificado por corebank, y no forma
              parte de tu disponible ni de tu patrimonio total.
            </p>
          </>
        ) : (
          <div className="space-y-3">
            <Skeleton className="h-3 w-40" />
            <Skeleton className="h-12 w-64" />
          </div>
        )}
      </header>

      {account && (
        <div className="space-y-5">
          <ExternalTransactionsTable externalAccountId={account.id} />
          <ImportHistoryList externalAccountId={account.id} />
          <SpendByCategoryList externalAccountId={account.id} />
        </div>
      )}
    </div>
  )
}

function ExternalTransactionsTable({ externalAccountId }: { externalAccountId: string }) {
  const transactions = useExternalTransactions(externalAccountId)
  const categories = useCategories()
  const setCategory = useSetExternalTransactionCategory(externalAccountId)

  // Flattened once for the picker: a parent next to each of its own children,
  // rather than a second level of <optgroup> nesting the API doesn't need.
  const categoryOptions = (categories.data?.categories ?? []).flatMap((node) => [
    node,
    ...node.children,
  ])

  if (transactions.isLoading) {
    return <Skeleton className="h-24 w-full" />
  }

  if (!transactions.data || transactions.data.transactions.length === 0) {
    return (
      <section className="card p-4" aria-labelledby="movimientos">
        <h2 id="movimientos" className="type-eyebrow">
          Movimientos
        </h2>
        <p className="mt-2 text-[0.8125rem] text-ink-faint">
          Sin movimientos importados todavía.
        </p>
      </section>
    )
  }

  return (
    <section aria-labelledby="movimientos">
      <h2 id="movimientos" className="type-eyebrow mb-2">
        Movimientos
      </h2>
      <div className="overflow-hidden rounded-[5px] border border-rule">
        <Table className="table-fixed">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                Fecha
              </TableHead>
              <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                Descripción
              </TableHead>
              <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 text-right align-bottom">
                Monto
              </TableHead>
              <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                Categoría
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {transactions.data.transactions.map((transaction) => (
              <TableRow key={transaction.id}>
                <TableCell className="px-3 py-2 text-[0.8125rem] text-ink-soft">
                  {formatDate(transaction.occurred_at)}
                </TableCell>
                <TableCell className="truncate px-3 py-2 text-[0.8125rem]">
                  {transaction.description}
                </TableCell>
                <TableCell className="px-3 py-2 text-right">
                  <Figure amount={transaction.amount} size="sm" />
                </TableCell>
                <TableCell className="px-3 py-2">
                  <NativeSelect
                    size="sm"
                    value={transaction.category_id ?? ''}
                    disabled={setCategory.isPending || categories.isLoading}
                    onChange={(event) =>
                      setCategory.mutate({
                        transactionId: transaction.id,
                        categoryId: event.target.value || null,
                      })
                    }
                  >
                    <NativeSelectOption value="">Sin categoría</NativeSelectOption>
                    {categoryOptions.map((category) => (
                      <NativeSelectOption key={category.id} value={category.id}>
                        {category.name}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

function ImportHistoryList({ externalAccountId }: { externalAccountId: string }) {
  const history = useImportHistory(externalAccountId)

  if (!history.data || history.data.imports.length === 0) return null

  return (
    <section className="card p-4" aria-labelledby="historial-import">
      <h2 id="historial-import" className="type-eyebrow mb-2">
        Historial de importaciones
      </h2>
      <ul className="space-y-1.5 text-[0.8125rem] text-ink-soft">
        {history.data.imports.map((batch) => (
          <li key={batch.id} className="flex items-baseline justify-between gap-3">
            <span className="truncate">
              {formatDate(batch.created_at)} · {batch.filename}
            </span>
            <span className="type-figure shrink-0 text-ink-faint">
              {batch.imported}/{batch.row_count}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

function SpendByCategoryList({ externalAccountId }: { externalAccountId: string }) {
  const spend = useSpendByCategory(externalAccountId)
  const categories = useCategories()

  if (!spend.data || spend.data.spend.length === 0) return null

  const categoryOptions = (categories.data?.categories ?? []).flatMap((node) => [
    node,
    ...node.children,
  ])
  const nameFor = (categoryId?: string) =>
    categoryOptions.find((category) => category.id === categoryId)?.name ?? 'Sin categoría'

  return (
    <section className="card p-4" aria-labelledby="gasto-categoria">
      <h2 id="gasto-categoria" className="type-eyebrow mb-2">
        Gasto por categoría
      </h2>
      <ul className="space-y-1.5 text-[0.8125rem]">
        {spend.data.spend.map((row) => (
          <li
            key={row.category_id ?? 'none'}
            className="flex items-baseline justify-between gap-3"
          >
            <span className="text-ink-soft">{nameFor(row.category_id)}</span>
            <Figure amount={row.amount_cents} size="sm" />
          </li>
        ))}
      </ul>
    </section>
  )
}
