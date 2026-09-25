import { useState } from 'react'
import { DownloadIcon, ListFilterIcon, SearchIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { exportEntriesCSV } from '@/api/endpoints'
import type { Account, EntryKind } from '@/api/types'
import { DateField } from '@/components/DateField'
import { EntryList } from '@/components/EntryList'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { Spinner } from '@/components/ui/spinner'
import { accountLabel, numberShort } from '@/lib/format'
import { useAccounts, useCategories, useEntries } from '@/lib/queries'
import { downloadBlob } from '@/lib/utils'

const KINDS: Array<{ value: EntryKind | ''; label: string }> = [
  { value: '', label: 'Todos' },
  { value: 'expense', label: 'Gastos' },
  { value: 'income', label: 'Ingresos' },
  { value: 'transfer', label: 'Transferencias entre tus cuentas' },
  { value: 'fee', label: 'Comisiones' },
  { value: 'interest', label: 'Intereses' },
  { value: 'refund', label: 'Reembolsos' },
  { value: 'trade', label: 'Compraventa de valores' },
]

/**
 * The statement.
 *
 * Paging is forward-only through a cursor, and the pages are kept in a stack so "back"
 * is a pop rather than another request. That mirrors what the API actually offers: a
 * cursor names a position in a list that is still growing, and inventing a page number
 * on top of it would produce a control that skips and repeats rows as new movements
 * arrive.
 *
 * On a phone the filters move into a sheet. Five controls above the statement pushed the
 * movements off the screen entirely — the page was a form with a list underneath it,
 * when what somebody opens it for is the list.
 */
export function HistoryPage() {
  const accountsQuery = useAccounts()
  const categories = useCategories()

  const [accountNumber, setAccountNumber] = useState('')
  const [kind, setKind] = useState<EntryKind | ''>('')
  const [search, setSearch] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')

  // The cursor of each page visited, so going back never refetches from the start.
  const [trail, setTrail] = useState<string[]>([])
  const cursor = trail[trail.length - 1]

  const query = {
    accountId: accountNumber || undefined,
    kinds: kind ? [kind] : undefined,
    search: search.trim() || undefined,
    from: since || undefined,
    to: until || undefined,
    limit: 25,
    cursor,
  }
  const history = useEntries(query)
  const [exporting, setExporting] = useState(false)

  // The file holds every movement the filters match, not only this page of them.
  async function exportCSV() {
    setExporting(true)
    try {
      const { blob, filename } = await exportEntriesCSV(query)
      downloadBlob(blob, filename ?? 'movimientos.csv')
    } finally {
      setExporting(false)
    }
  }

  const accounts = accountsQuery.data?.accounts ?? []

  /**
   * Any filter change starts the listing again: a cursor from the old filter points into
   * a list that no longer exists.
   */
  function changeFilter(apply: () => void) {
    apply()
    setTrail([])
  }

  const active = [accountNumber, kind, search, since, until].filter(Boolean).length
  const filtered = active > 0

  const clearAll = () =>
    changeFilter(() => {
      setAccountNumber('')
      setKind('')
      setSearch('')
      setSince('')
      setUntil('')
    })

  const controls = (
    <Filters
      accounts={accounts}
      accountNumber={accountNumber}
      kind={kind}
      since={since}
      until={until}
      onAccountNumber={(value) => changeFilter(() => setAccountNumber(value))}
      onKind={(value) => changeFilter(() => setKind(value))}
      onSince={(value) => changeFilter(() => setSince(value))}
      onUntil={(value) => changeFilter(() => setUntil(value))}
    />
  )

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-baseline justify-between gap-3">
        {/* Hidden on a phone, where the app bar already says "Historial". */}
        <div className="hidden md:block">
          <p className="type-eyebrow">Historial</p>
          <h1 className="type-display mt-1.5 text-[1.75rem]">Todos tus movimientos</h1>
        </div>

        <div className="flex flex-1 items-center gap-2 md:flex-none">
          {/* The sheet is the phone's filter surface; above `md` the controls are simply
              on the page, where there is room for them. */}
          <Sheet>
            <SheetTrigger asChild>
              <Button variant="outline" className="md:hidden">
                <ListFilterIcon aria-hidden="true" />
                Filtros
                {active > 0 && (
                  <span className="type-figure ml-0.5 flex size-5 items-center justify-center rounded-full bg-copper text-[0.625rem] text-primary-foreground">
                    {active}
                  </span>
                )}
              </Button>
            </SheetTrigger>
            <SheetContent side="bottom" className="max-h-[85dvh] overflow-y-auto">
              <SheetHeader>
                <SheetTitle className="type-display text-[1.125rem]">
                  Filtrar movimientos
                </SheetTitle>
              </SheetHeader>
              <div className="px-4 pb-6">
                {controls}
                {filtered && (
                  <Button variant="outline" className="mt-4 w-full" onClick={clearAll}>
                    Limpiar los filtros
                  </Button>
                )}
              </div>
            </SheetContent>
          </Sheet>

          <SearchBox
            value={search}
            onChange={setSearch}
            onSubmit={() => setTrail([])}
            className="min-w-0 flex-1 md:w-64 md:flex-none"
          />
          <Button
            variant="outline"
            onClick={() => void exportCSV()}
            disabled={exporting}
            aria-label="Descargar CSV"
          >
            {exporting ? <Spinner /> : <DownloadIcon aria-hidden="true" />}
            <span className="hidden md:inline">CSV</span>
          </Button>
        </div>
      </header>

      {/* The controls on the page, from `md` up. */}
      <section className="card hidden p-4 md:block" aria-label="Filtros">
        {controls}
        {filtered && (
          <Button
            variant="ghost"
            size="sm"
            className="mt-3 text-ink-soft"
            onClick={clearAll}
          >
            Limpiar los filtros
          </Button>
        )}
      </section>

      {history.isError ? (
        <Alert variant="destructive">
          <AlertTitle>No pudimos cargar el historial</AlertTitle>
          <AlertDescription>
            Vuelve a intentarlo, o limpia los filtros si acabas de cambiarlos.
          </AlertDescription>
        </Alert>
      ) : (
        <section className="card">
          <div className="px-4 py-1">
            <EntryList
              entries={history.data?.entries ?? []}
              accounts={accounts}
              categories={categories.data?.categories}
              loading={history.isLoading}
              emptyTitle={
                filtered ? 'Ningún movimiento coincide' : 'Aún no tienes movimientos'
              }
              emptyBody={
                filtered
                  ? 'Prueba con un rango de fechas más amplio o quita algún filtro.'
                  : 'Importa un estado de cuenta de tu banco y tus movimientos aparecerán aquí.'
              }
              emptyAction={
                filtered ? (
                  <Button variant="outline" onClick={clearAll}>
                    Limpiar los filtros
                  </Button>
                ) : (
                  <Button asChild variant="outline">
                    <Link to="/cuentas">Importar estado de cuenta</Link>
                  </Button>
                )
              }
            />
          </div>

          {(trail.length > 0 || history.data?.has_more) && (
            <div className="flex items-center justify-between gap-3 border-t border-rule px-4 py-3">
              <Button
                variant="outline"
                size="sm"
                disabled={trail.length === 0 || history.isFetching}
                onClick={() => setTrail((previous) => previous.slice(0, -1))}
              >
                Anteriores
              </Button>

              <span className="flex items-center gap-2 text-[0.75rem] text-ink-faint">
                {history.isFetching && <Spinner />}
                Página {trail.length + 1}
              </span>

              <Button
                variant="outline"
                size="sm"
                disabled={!history.data?.next_cursor || history.isFetching}
                onClick={() => {
                  const next = history.data?.next_cursor
                  if (next) setTrail((previous) => [...previous, next])
                }}
              >
                Siguientes
              </Button>
            </div>
          )}
        </section>
      )}
    </div>
  )
}

/**
 * The four filters, rendered identically on the page and inside the phone's sheet.
 *
 * One component rather than two copies: the previous shell kept two renderings of the
 * navigation in step only because they were three lines apart in one file, and that is
 * not a property worth relying on twice.
 */
function Filters({
  accounts,
  accountNumber,
  kind,
  since,
  until,
  onAccountNumber,
  onKind,
  onSince,
  onUntil,
}: {
  accounts: Account[]
  accountNumber: string
  kind: EntryKind | ''
  since: string
  until: string
  onAccountNumber: (value: string) => void
  onKind: (value: EntryKind | '') => void
  onSince: (value: string) => void
  onUntil: (value: string) => void
}) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <Field>
        <FieldLabel htmlFor="filtro-cuenta">Cuenta</FieldLabel>
        <NativeSelect
          id="filtro-cuenta"
          className="w-full"
          value={accountNumber}
          onChange={(event) => onAccountNumber(event.target.value)}
        >
          <NativeSelectOption value="">Todas</NativeSelectOption>
          {accounts.map((account) => (
            <NativeSelectOption key={account.id} value={account.id}>
              {accountLabel(account)} · {numberShort(account.external_number)}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </Field>

      <Field>
        <FieldLabel htmlFor="filtro-tipo">Tipo</FieldLabel>
        <NativeSelect
          id="filtro-tipo"
          className="w-full"
          value={kind}
          onChange={(event) => onKind(event.target.value as EntryKind | '')}
        >
          {KINDS.map((option) => (
            <NativeSelectOption key={option.value} value={option.value}>
              {option.label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </Field>

      <DateField label="Desde" value={since} onChange={onSince} max={until || undefined} />
      <DateField label="Hasta" value={until} onChange={onUntil} min={since || undefined} />
    </div>
  )
}

/** The concept search, with its button inside the field rather than beside it. */
function SearchBox({
  value,
  onChange,
  onSubmit,
  className,
}: {
  value: string
  onChange: (value: string) => void
  onSubmit: () => void
  className?: string
}) {
  return (
    <form
      className={className}
      onSubmit={(event) => {
        event.preventDefault()
        onSubmit()
      }}
    >
      <label htmlFor="buscar" className="sr-only">
        Buscar en el concepto
      </label>
      <InputGroup>
        <InputGroupAddon>
          <SearchIcon className="size-4" aria-hidden="true" />
        </InputGroupAddon>
        <InputGroupInput
          id="buscar"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder="Buscar…"
          autoComplete="off"
        />
        <InputGroupAddon align="inline-end">
          <InputGroupButton type="submit" size="icon-xs" aria-label="Buscar">
            <SearchIcon className="size-3.5" aria-hidden="true" />
          </InputGroupButton>
        </InputGroupAddon>
      </InputGroup>
    </form>
  )
}
