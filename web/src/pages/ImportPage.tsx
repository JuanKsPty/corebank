import { CircleAlertIcon, UploadIcon } from 'lucide-react'
import { useRef, useState } from 'react'

import { ApiError } from '@/api/client'
import type { ExternalAccount } from '@/api/types'
import { Figure } from '@/components/primitives'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
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
  useImportBankStatement,
  useImportHistory,
  useSetExternalTransactionCategory,
  useSpendByCategory,
} from '@/lib/queries'

const INSTITUTION_LABEL: Record<string, string> = {
  banco_general: 'Banco General',
  bac: 'BAC',
}

const FORMAT_LABEL: Record<string, string> = {
  bg_card: 'Tarjeta de Banco General',
  bg_account: 'Cuenta de Banco General',
  bac_account: 'Cuenta de BAC',
}

/**
 * Importing bank statements.
 *
 * There is exactly one decision to make here — which file — and the account it
 * belongs to is never chosen by hand: the backend reads the institution and
 * account number straight out of the file itself. That is why this page has no
 * account picker anywhere, upload or otherwise; a file declares its own home.
 */
export function ImportPage() {
  return (
    <div className="space-y-6">
      <header>
        <p className="type-eyebrow">Importar</p>
        <h1 className="type-display mt-1.5 text-[1.75rem]">Estados de cuenta</h1>
        <p className="mt-1.5 max-w-prose text-[0.9375rem] text-ink-soft">
          Sube el archivo tal como lo descargas del banco. La cuenta a la que pertenece se
          identifica sola.
        </p>
      </header>

      <UploadCard />
      <ExternalAccountsSection />
    </div>
  )
}

function UploadCard() {
  const upload = useImportBankStatement()
  const inputRef = useRef<HTMLInputElement>(null)

  const handleFile = (file: File | undefined) => {
    if (!file) return
    upload.mutate(file)
  }

  const failure = upload.error
  const message =
    failure instanceof ApiError
      ? failure.message
      : failure
        ? 'No se pudo importar el archivo. Inténtalo de nuevo.'
        : null

  return (
    <section className="card p-5" aria-labelledby="subir">
      <h2 id="subir" className="type-eyebrow">
        Subir un archivo
      </h2>
      <p className="mt-1 text-[0.8125rem] text-ink-soft">
        Tarjeta o cuenta de Banco General (.txt / .xlsx), o cuenta de BAC (.csv).
      </p>

      <label
        className="mt-4 flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[5px] border border-dashed border-rule bg-paper-sunken px-4 py-8 text-center text-[0.875rem] text-ink-soft transition-colors hover:border-ink-faint aria-disabled:pointer-events-none aria-disabled:opacity-60"
        aria-disabled={upload.isPending}
      >
        <UploadIcon className="size-5 text-ink-faint" aria-hidden="true" />
        {upload.isPending ? 'Procesando…' : 'Elige un archivo o arrástralo aquí'}
        <input
          ref={inputRef}
          type="file"
          accept=".txt,.csv,.xlsx"
          className="sr-only"
          disabled={upload.isPending}
          onChange={(event) => {
            handleFile(event.target.files?.[0])
            // Cleared so choosing the exact same file twice in a row still fires
            // a change event the second time.
            event.target.value = ''
          }}
        />
      </label>

      {upload.isPending && (
        <p className="mt-3 flex items-center gap-2 text-[0.8125rem] text-ink-soft">
          <Spinner /> Leyendo el archivo…
        </p>
      )}

      {upload.isSuccess && upload.data && (
        <p className="mt-3 text-[0.8125rem] text-ink-soft">
          {FORMAT_LABEL[upload.data.format] ?? upload.data.format} · {upload.data.imported}{' '}
          importado{upload.data.imported === 1 ? '' : 's'}, {upload.data.skipped_duplicates}{' '}
          ya registrado{upload.data.skipped_duplicates === 1 ? '' : 's'} de{' '}
          {upload.data.total_rows} fila{upload.data.total_rows === 1 ? '' : 's'}.
        </p>
      )}

      {upload.isError && (
        <Alert variant="destructive" className="mt-3">
          <CircleAlertIcon />
          <AlertTitle>No se pudo importar el archivo</AlertTitle>
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      )}
    </section>
  )
}

function ExternalAccountsSection() {
  const accounts = useExternalAccounts()

  return (
    <section aria-labelledby="cuentas-externas">
      <h2 id="cuentas-externas" className="type-eyebrow mb-3">
        Cuentas importadas
      </h2>

      {accounts.isLoading && (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      )}

      {accounts.data && accounts.data.accounts.length === 0 && (
        <p className="text-[0.875rem] text-ink-soft">
          Todavía no has importado ningún archivo.
        </p>
      )}

      {accounts.data && accounts.data.accounts.length > 0 && (
        <div className="space-y-3">
          {accounts.data.accounts.map((account) => (
            <ExternalAccountCard key={account.id} account={account} />
          ))}
        </div>
      )}
    </section>
  )
}

function ExternalAccountCard({ account }: { account: ExternalAccount }) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="card">
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        className="flex w-full items-center justify-between gap-3 p-4 text-left"
        aria-expanded={expanded}
      >
        <div className="min-w-0">
          <p className="truncate text-[0.9375rem] font-medium">{account.display_name}</p>
          <p className="type-figure mt-0.5 text-[0.75rem] text-ink-faint">
            {INSTITUTION_LABEL[account.institution] ?? account.institution} ·{' '}
            {account.account_number}
          </p>
        </div>
        <span className="shrink-0 text-[0.8125rem] font-medium text-copper">
          {expanded ? 'Ocultar' : 'Ver movimientos'}
        </span>
      </button>

      {expanded && (
        <div className="space-y-5 border-t border-rule px-4 py-4">
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
      <p className="text-[0.8125rem] text-ink-faint">Sin movimientos importados todavía.</p>
    )
  }

  return (
    <div>
      <p className="type-eyebrow mb-2">Movimientos</p>
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
    </div>
  )
}

function ImportHistoryList({ externalAccountId }: { externalAccountId: string }) {
  const history = useImportHistory(externalAccountId)

  if (!history.data || history.data.imports.length === 0) return null

  return (
    <div>
      <p className="type-eyebrow mb-2">Historial de importaciones</p>
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
    </div>
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
    <div>
      <p className="type-eyebrow mb-2">Gasto por categoría</p>
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
    </div>
  )
}
