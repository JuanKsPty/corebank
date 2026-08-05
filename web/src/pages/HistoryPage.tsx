import { useState } from 'react'
import { DownloadIcon, ListFilterIcon, SearchIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { ApiError } from '@/api/client'
import * as api from '@/api/endpoints'
import type { HistoryQuery } from '@/api/endpoints'
import type { Account, MovementKind } from '@/api/types'
import { DateField } from '@/components/DateField'
import { MovementList } from '@/components/MovementList'
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
import { accountLabel } from '@/lib/format'
import { useHistory, useMe } from '@/lib/queries'
import { downloadBlob } from '@/lib/utils'

const KINDS: Array<{ value: MovementKind | ''; label: string }> = [
  { value: '', label: 'Todos' },
  { value: 'deposit', label: 'Depósitos' },
  { value: 'withdrawal', label: 'Retiros' },
  { value: 'transfer', label: 'Transferencias' },
  { value: 'internal_transfer', label: 'Traspasos' },
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
  const me = useMe()

  const [accountNumber, setAccountNumber] = useState('')
  const [kind, setKind] = useState<MovementKind | ''>('')
  const [search, setSearch] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')

  // The cursor of each page visited, so going back never refetches from the start.
  const [trail, setTrail] = useState<string[]>([])
  const cursor = trail[trail.length - 1]

  const query = {
    accountNumber: accountNumber || undefined,
    kind: kind || undefined,
    search: search.trim() || undefined,
    since: since || undefined,
    until: until || undefined,
    limit: 25,
    cursor,
  }
  const history = useHistory(query)

  const accounts = me.data?.accounts ?? []
  const ownedAccounts = new Set(accounts.map((account) => account.account_number))

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
                  <span className="type-figure ml-0.5 flex size-5 items-center justify-center rounded-full bg-copper text-[0.625rem] text-white">
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

          {/* Downloads what is on screen, filters and all — not the whole history.
              A button that ignored the filters would be a different feature wearing
              this one's label. */}
          <ExportButton query={query} filtered={filtered} />

          <Button asChild variant="outline" className="hidden shrink-0 md:inline-flex">
            <Link to="/mover">Mover dinero</Link>
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
            <MovementList
              movements={history.data?.transactions ?? []}
              ownedAccounts={ownedAccounts}
              loading={history.isLoading}
              emptyTitle={
                filtered ? 'Ningún movimiento coincide' : 'Aún no tienes movimientos'
              }
              emptyBody={
                filtered
                  ? 'Prueba con un rango de fechas más amplio o quita algún filtro.'
                  : 'Haz un ingreso o una transferencia y aparecerá aquí al instante.'
              }
              emptyAction={
                filtered ? (
                  <Button variant="outline" onClick={clearAll}>
                    Limpiar los filtros
                  </Button>
                ) : (
                  <Button asChild variant="outline">
                    <Link to="/mover">Mover dinero</Link>
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
  kind: MovementKind | ''
  since: string
  until: string
  onAccountNumber: (value: string) => void
  onKind: (value: MovementKind | '') => void
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
            <NativeSelectOption key={account.account_number} value={account.account_number}>
              {accountLabel(account)} · {account.account_number}
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
          onChange={(event) => onKind(event.target.value as MovementKind | '')}
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

/**
 * Downloads the filtered history as a CSV file.
 *
 * The whole point is that it exports what the screen is showing. Somebody who filtered
 * to one account and one month wants that month, and a button that quietly handed them
 * everything would be worse than no button — they would not notice until the numbers
 * failed to add up somewhere else.
 *
 * The failure is shown next to the button rather than as a toast. A download that does
 * nothing is indistinguishable from a browser being slow, so silence here reads as a
 * broken feature.
 */
function ExportButton({ query, filtered }: { query: HistoryQuery; filtered: boolean }) {
  const [working, setWorking] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  async function download() {
    setWorking(true)
    setFailure(null)
    try {
      const { blob, filename } = await api.exportHistoryCSV(query)
      downloadBlob(blob, filename ?? 'corebank-movimientos.csv')
    } catch (error) {
      setFailure(
        error instanceof ApiError ? error.message : 'No pudimos preparar la descarga.',
      )
    } finally {
      setWorking(false)
    }
  }

  return (
    <div className="relative shrink-0">
      <Button
        variant="outline"
        onClick={() => void download()}
        disabled={working}
        title={filtered ? 'Descarga los movimientos que estás viendo' : undefined}
      >
        {working ? <Spinner /> : <DownloadIcon aria-hidden="true" />}
        <span className="hidden sm:inline">{working ? 'Preparando' : 'CSV'}</span>
        <span className="sr-only sm:hidden">Descargar CSV</span>
      </Button>
      {failure && (
        <p
          role="alert"
          className="absolute top-full right-0 z-10 mt-1 w-56 rounded-[5px] border border-danger/40 bg-paper-raised px-2.5 py-1.5 text-[0.75rem] text-danger-text shadow-sm"
        >
          {failure}
        </p>
      )}
    </div>
  )
}
