import { Link } from 'react-router-dom'

import type { Account } from '@/api/types'
import { FlowChart } from '@/components/FlowChart'
import { MovementList } from '@/components/MovementList'
import {
  BalanceComposition,
  EmptyState,
  Figure,
  Notice,
  SkeletonLine,
  cx,
} from '@/components/primitives'
import { accountTypeLabel, formatDate } from '@/lib/format'
import { useDashboard } from '@/lib/queries'
import { useSession } from '@/lib/session'

/**
 * The dashboard.
 *
 * The page opens with what the customer came to find out — how much they can spend —
 * and immediately qualifies it with how that number is composed. That is the thesis
 * of the whole product in one element: a balance here is a ledger position, not a
 * stored number, and when the assistant reserves funds you watch the composition
 * change rather than being told about it.
 *
 * The assistant is no longer part of this page. It lives in the shell, as a column at
 * wide widths and a sheet below them, so it is reachable from the statement and the
 * transfer form too — and so that asking it for a movement leaves the balance on
 * screen behind it, which is the thing worth watching when funds are reserved.
 */
export function DashboardPage() {
  const { user } = useSession()
  const dashboard = useDashboard()

  const firstName = user?.full_name.split(' ')[0] ?? ''

  if (dashboard.isError) {
    return (
      <Notice tone="error" title="No pudimos cargar tu resumen">
        Vuelve a intentarlo en un momento. Si sigue pasando, revisa que la API esté en
        marcha.
      </Notice>
    )
  }

  const data = dashboard.data
  const ownedAccounts = new Set(
    (data?.accounts ?? []).map((account) => account.account_number),
  )

  return (
    <div className="space-y-6">
      {/* --- the headline balance ---------------------------------------- */}
      <section aria-labelledby="saldo">
        <p className="type-eyebrow">Disponible</p>
        <h1 id="saldo" className="mt-1.5">
          <span className="sr-only">Saldo disponible: </span>
          {data ? (
            <Figure amount={data.total_available} size="display" className="type-display" />
          ) : (
            <span className="skeleton block h-12 w-64" aria-hidden="true" />
          )}
        </h1>
        <p className="mt-1 text-[0.9375rem] text-ink-soft">
          {firstName
            ? `${firstName}, esto es lo que puedes mover ahora.`
            : 'Esto es lo que puedes mover ahora.'}
        </p>

        {data && data.accounts.length > 0 && (
          <div className="mt-5 max-w-xl">
            <BalanceComposition
              posted={sumPosted(data.accounts)}
              held={sumHeld(data.accounts)}
              available={data.total_available}
            />
          </div>
        )}
      </section>

      {/* --- the accounts ----------------------------------------------- */}
      <section aria-labelledby="cuentas">
        <div className="mb-3 flex items-baseline justify-between gap-3">
          <h2 id="cuentas" className="type-eyebrow">
            Tus cuentas
          </h2>
          <Link
            to="/cuentas"
            className="text-[0.8125rem] font-medium text-copper hover:underline"
          >
            Ver todas
          </Link>
        </div>

        {dashboard.isLoading ? (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {[0, 1, 2].map((index) => (
              <div key={index} className="card p-4">
                <SkeletonLine className="w-24" />
                <SkeletonLine className="mt-3 h-5 w-32" />
              </div>
            ))}
          </div>
        ) : data && data.accounts.length > 0 ? (
          <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {data.accounts.map((account) => (
              <li key={account.id}>
                <AccountCard account={account} />
              </li>
            ))}
          </ul>
        ) : (
          <div className="card">
            <EmptyState title="No tienes cuentas abiertas">
              Tu cuenta se crea al registrarte. Si ves esto, algo salió mal en el registro.
            </EmptyState>
          </div>
        )}
      </section>

      {/* --- the flow chart ---------------------------------------------- */}
      <section aria-labelledby="flujo" className="card p-4">
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <h2 id="flujo" className="type-eyebrow">
            Entradas y salidas
          </h2>
          {data && data.flow.points.length > 0 && (
            <p className="type-figure text-[0.6875rem] text-ink-faint">
              {/* The label states the period the chart covers. When the window has
                    moved back to where the activity is, saying "últimos 30 días"
                    would be false. */}
              {data.flow.recent
                ? 'últimos 30 días'
                : `${formatDate(data.flow.from)} — ${formatDate(data.flow.to)}`}
            </p>
          )}
        </div>
        <div className="mt-3">
          {dashboard.isLoading ? (
            <div className="skeleton h-[180px] w-full" aria-hidden="true" />
          ) : data && data.flow.points.length > 0 ? (
            <FlowChart flow={data.flow} />
          ) : (
            <EmptyState title="Sin movimientos que graficar">
              Cuando entre o salga dinero lo verás aquí, día por día.
            </EmptyState>
          )}
        </div>
      </section>

      {/* --- recent movements -------------------------------------------- */}
      <section aria-labelledby="recientes" className="card">
        <div className="flex items-baseline justify-between gap-3 border-b border-rule px-4 py-3">
          <h2 id="recientes" className="type-eyebrow">
            Últimos movimientos
          </h2>
          <Link
            to="/historial"
            className="text-[0.8125rem] font-medium text-copper hover:underline"
          >
            Ver todo
          </Link>
        </div>
        <div className="px-4 py-1">
          <MovementList
            movements={data?.recent ?? []}
            ownedAccounts={ownedAccounts}
            loading={dashboard.isLoading}
            emptyTitle="Aún no tienes movimientos"
            emptyBody="Haz un ingreso o una transferencia y aparecerá aquí al instante."
            emptyAction={
              <Link to="/mover" className="btn btn-secondary">
                Mover dinero
              </Link>
            }
          />
        </div>
      </section>
    </div>
  )
}

function AccountCard({ account }: { account: Account }) {
  const held = account.held.cents > 0

  return (
    <Link
      to={`/cuentas/${account.account_number}`}
      className={cx(
        'card group block p-4 transition-colors hover:border-ink-faint',
        held && 'border-hold/45',
      )}
    >
      <div className="flex items-baseline justify-between gap-2">
        <p className="text-[0.8125rem] font-medium">
          {accountTypeLabel(account.account_type)}
        </p>
        <p className="type-figure text-[0.6875rem] text-ink-faint">
          ···{account.account_number.slice(-4)}
        </p>
      </div>

      <p className="mt-2.5">
        <Figure amount={account.available} size="lg" />
      </p>

      {held ? (
        <p className="mt-1 flex items-center gap-1.5 text-[0.75rem] text-hold">
          <span aria-hidden="true" className="size-1.5 rounded-full bg-hold" />
          <Figure amount={account.held} size="sm" tone="hold" /> retenidos
        </p>
      ) : (
        <p className="type-figure mt-1 text-[0.75rem] text-ink-faint">
          {account.account_number}
        </p>
      )}
    </Link>
  )
}

/**
 * The consolidated posted and held figures.
 *
 * Summed from cents, in a plain integer addition. The API gives the consolidated
 * *available* total but not the other two, and summing them here is exact — which
 * it would not be if these were decimal strings being added as floats.
 */
function sumPosted(accounts: Account[]) {
  const cents = accounts.reduce((total, account) => total + account.posted.cents, 0)
  return { cents, formatted: (cents / 100).toFixed(2), currency: 'USD' }
}

function sumHeld(accounts: Account[]) {
  const cents = accounts.reduce((total, account) => total + account.held.cents, 0)
  return { cents, formatted: (cents / 100).toFixed(2), currency: 'USD' }
}
