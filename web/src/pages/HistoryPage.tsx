import { useState } from 'react'
import { Link } from 'react-router-dom'

import type { MovementKind } from '@/api/types'
import { MovementList } from '@/components/MovementList'
import { Notice, Spinner, cx } from '@/components/primitives'
import { accountTypeLabel } from '@/lib/format'
import { useHistory, useMe } from '@/lib/queries'

/**
 * The statement.
 *
 * Paging is forward-only through a cursor, and the pages are kept in a stack so
 * "back" is a pop rather than another request. That mirrors what the API actually
 * offers: a cursor names a position in a list that is still growing, and inventing
 * a page number on top of it would produce a control that skips and repeats rows as
 * new movements arrive.
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

  /** Any filter change starts the listing again: a cursor from the old filter
      points into a list that no longer exists. */
  function changeFilter(apply: () => void) {
    apply()
    setTrail([])
  }

  const filtered = Boolean(accountNumber || kind || search || since || until)

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-baseline justify-between gap-3">
        <div>
          <p className="type-eyebrow">Historial</p>
          <h1 className="type-display mt-1.5 text-[1.75rem]">Todos tus movimientos</h1>
        </div>
        <Link to="/mover" className="btn btn-secondary">
          Mover dinero
        </Link>
      </header>

      <section className="card p-4" aria-label="Filtros">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <label className="block">
            <span className="field-label">Cuenta</span>
            <select
              className="field-input"
              value={accountNumber}
              onChange={(event) => changeFilter(() => setAccountNumber(event.target.value))}
            >
              <option value="">Todas</option>
              {accounts.map((account) => (
                <option key={account.account_number} value={account.account_number}>
                  {accountTypeLabel(account.account_type)} · {account.account_number}
                </option>
              ))}
            </select>
          </label>

          <label className="block">
            <span className="field-label">Tipo</span>
            <select
              className="field-input"
              value={kind}
              onChange={(event) => changeFilter(() => setKind(event.target.value as MovementKind | ''))}
            >
              <option value="">Todos</option>
              <option value="deposit">Depósitos</option>
              <option value="withdrawal">Retiros</option>
              <option value="transfer">Transferencias</option>
              <option value="internal_transfer">Traspasos</option>
            </select>
          </label>

          <label className="block">
            <span className="field-label">Desde</span>
            <input
              type="date"
              className="field-input"
              value={since}
              max={until || undefined}
              onChange={(event) => changeFilter(() => setSince(event.target.value))}
            />
          </label>

          <label className="block">
            <span className="field-label">Hasta</span>
            <input
              type="date"
              className="field-input"
              value={until}
              min={since || undefined}
              onChange={(event) => changeFilter(() => setUntil(event.target.value))}
            />
          </label>
        </div>

        <form
          className="mt-3 flex gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            setTrail([])
          }}
        >
          <label className="flex-1">
            <span className="sr-only">Buscar en el concepto</span>
            <input
              className="field-input"
              placeholder="Buscar en el concepto…"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </label>
          <button type="submit" className="btn btn-secondary shrink-0">
            Buscar
          </button>
          {filtered && (
            <button
              type="button"
              className="btn btn-secondary shrink-0"
              onClick={() =>
                changeFilter(() => {
                  setAccountNumber('')
                  setKind('')
                  setSearch('')
                  setSince('')
                  setUntil('')
                })
              }
            >
              Limpiar
            </button>
          )}
        </form>
      </section>

      {history.isError ? (
        <Notice tone="error" title="No pudimos cargar el historial">
          Vuelve a intentarlo, o limpia los filtros si acabas de cambiarlos.
        </Notice>
      ) : (
        <section className="card">
          <div className="px-4 py-1">
            <MovementList
              movements={history.data?.transactions ?? []}
              ownedAccounts={ownedAccounts}
              loading={history.isLoading}
              emptyTitle={filtered ? 'Ningún movimiento coincide' : 'Aún no tienes movimientos'}
              emptyBody={
                filtered
                  ? 'Prueba con un rango de fechas más amplio o quita algún filtro.'
                  : 'Haz un ingreso o una transferencia y aparecerá aquí al instante.'
              }
              emptyAction={
                filtered ? undefined : (
                  <Link to="/mover" className="btn btn-secondary">
                    Mover dinero
                  </Link>
                )
              }
            />
          </div>

          {(trail.length > 0 || history.data?.has_more) && (
            <div className="flex items-center justify-between gap-3 border-t border-rule px-4 py-3">
              <button
                type="button"
                className="btn btn-secondary"
                disabled={trail.length === 0 || history.isFetching}
                onClick={() => setTrail((previous) => previous.slice(0, -1))}
              >
                Anteriores
              </button>

              <span className={cx('flex items-center gap-2 text-[0.75rem] text-ink-faint')}>
                {history.isFetching && <Spinner />}
                Página {trail.length + 1}
              </span>

              <button
                type="button"
                className="btn btn-secondary"
                disabled={!history.data?.next_cursor || history.isFetching}
                onClick={() => {
                  const next = history.data?.next_cursor
                  if (next) setTrail((previous) => [...previous, next])
                }}
              >
                Siguientes
              </button>
            </div>
          )}
        </section>
      )}
    </div>
  )
}
