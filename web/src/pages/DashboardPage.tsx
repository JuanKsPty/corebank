import { ChartColumnIcon, WalletIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { cn } from '@/lib/utils'
import type { Account } from '@/api/types'
import { FlowChart } from '@/components/FlowChart'
import { MovementList } from '@/components/MovementList'
import { BalanceComposition, Figure } from '@/components/primitives'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { accountLabel, accountTypeAside, formatDate } from '@/lib/format'
import { useAccountTotal, useDashboard, useHoldingsValue } from '@/lib/queries'
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
      <Alert variant="destructive">
        <AlertTitle>No pudimos cargar tu resumen</AlertTitle>
        <AlertDescription>
          Vuelve a intentarlo en un momento. Si sigue pasando, revisa que la API esté en
          marcha.
        </AlertDescription>
      </Alert>
    )
  }

  const data = dashboard.data
  const ownedAccounts = new Set(
    (data?.accounts ?? []).map((account) => account.account_number),
  )
  const investmentAccountNumbers = (data?.accounts ?? [])
    .filter((account) => account.account_type === 'investment')
    .map((account) => account.account_number)
  const { holdings } = useHoldingsValue(investmentAccountNumbers)

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
            <Skeleton className="block h-12 w-64" />
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

        {data && holdings.cents > 0 && (
          <p className="mt-3 flex items-baseline gap-2 text-[0.875rem] text-ink-soft">
            <span>Patrimonio total (incluye inversiones)</span>
            <Figure
              amount={{
                cents: data.total_available.cents + holdings.cents,
                formatted: ((data.total_available.cents + holdings.cents) / 100).toFixed(2),
                currency: 'USD',
              }}
              size="sm"
              tone="copper"
            />
          </p>
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
                <Skeleton className="h-3 w-24" />
                <Skeleton className="h-3 mt-3 h-5 w-32" />
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
            <Empty className="border-0 bg-transparent">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <WalletIcon />
                </EmptyMedia>
                <EmptyTitle className="type-display text-[1.0625rem]">
                  No tienes cuentas abiertas
                </EmptyTitle>
                <EmptyDescription>
                  Tu primera cuenta se crea al registrarte. Si ves esto, ábrela a mano.
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button asChild variant="outline">
                  <Link to="/cuentas">Abrir una cuenta</Link>
                </Button>
              </EmptyContent>
            </Empty>
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
            <Skeleton className="h-[180px] w-full" />
          ) : data && data.flow.points.length > 0 ? (
            <FlowChart flow={data.flow} />
          ) : (
            <Empty className="border-0 bg-transparent">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <ChartColumnIcon />
                </EmptyMedia>
                <EmptyTitle className="type-display text-[1.0625rem]">
                  Sin movimientos que graficar
                </EmptyTitle>
                <EmptyDescription>
                  Cuando entre o salga dinero lo verás aquí, día por día.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
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
              <Button asChild variant="outline">
                <Link to="/mover">Mover dinero</Link>
              </Button>
            }
          />
        </div>
      </section>
    </div>
  )
}

function AccountCard({ account }: { account: Account }) {
  const held = account.held.cents > 0
  const { total, cash, showTotal } = useAccountTotal(account)

  return (
    <Link
      to={`/cuentas/${account.account_number}`}
      className={cn(
        'card group block p-4 transition-colors hover:border-ink-faint',
        held && 'border-hold/45',
      )}
    >
      <div className="flex items-baseline justify-between gap-2">
        <p className="min-w-0 truncate text-[0.8125rem] font-medium">
          {accountLabel(account)}
        </p>
        <p className="type-figure shrink-0 text-[0.6875rem] text-ink-faint">
          ···{account.account_number.slice(-4)}
          {accountTypeAside(account) && ` · ${accountTypeAside(account)}`}
        </p>
      </div>

      <p className="mt-2.5">
        <Figure
          amount={showTotal && total ? total : account.available}
          size="lg"
          tone={showTotal ? 'copper' : 'ink'}
        />
      </p>
      {showTotal && (
        <p className="type-figure mt-0.5 text-[0.6875rem] text-ink-faint">
          Efectivo: <Figure amount={cash ?? account.available} size="sm" />
        </p>
      )}

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
