import { Link, useParams } from 'react-router-dom'

import { MovementList } from '@/components/MovementList'
import { BalanceComposition, Figure, Notice, SkeletonLine } from '@/components/primitives'
import { accountTypeLabel, formatDate } from '@/lib/format'
import { useHistory, useMe } from '@/lib/queries'

/**
 * One account.
 *
 * The same composition bar as the dashboard, scoped to a single account — which is
 * where it is most useful, because a reservation belongs to one account and the
 * consolidated view can only show the total of them.
 */
export function AccountPage() {
  const { number = '' } = useParams()
  const me = useMe()
  const history = useHistory({ accountNumber: number, limit: 25 })

  const accounts = me.data?.accounts ?? []
  const account = accounts.find((candidate) => candidate.account_number === number)
  const ownedAccounts = new Set(accounts.map((candidate) => candidate.account_number))

  // Only once the account list has actually loaded does "not found" mean anything.
  if (!me.isLoading && !account) {
    return (
      <div className="mx-auto max-w-lg py-16 text-center">
        <p className="type-eyebrow">Cuenta no encontrada</p>
        <h1 className="type-display mt-2 text-2xl">Esta cuenta no es tuya o no existe</h1>
        <p className="mt-2 text-ink-soft">
          Revisa el número, o vuelve al resumen para ver tus cuentas.
        </p>
        <Link to="/" className="btn btn-secondary mt-5">
          Ir al resumen
        </Link>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <nav aria-label="Ruta" className="text-[0.8125rem] text-ink-faint">
        <Link to="/" className="hover:text-ink hover:underline">
          Resumen
        </Link>
        <span aria-hidden="true" className="px-1.5">
          /
        </span>
        <span className="type-figure text-ink-soft">{number}</span>
      </nav>

      <header>
        {account ? (
          <>
            <p className="type-eyebrow">
              {accountTypeLabel(account.account_type)} · abierta el {formatDate(account.created_at)}
            </p>
            <h1 className="mt-1.5">
              <span className="sr-only">Saldo disponible: </span>
              <Figure amount={account.available} size="display" className="type-display" />
            </h1>
            <p className="type-figure mt-1 text-[0.875rem] text-ink-soft">{account.account_number}</p>

            <div className="mt-5 max-w-xl">
              <BalanceComposition
                posted={account.posted}
                held={account.held}
                available={account.available}
              />
            </div>
          </>
        ) : (
          <div className="space-y-3">
            <SkeletonLine className="w-40" />
            <div className="skeleton h-12 w-64" aria-hidden="true" />
          </div>
        )}
      </header>

      {account && account.held.cents > 0 && (
        <Notice tone="hold" title="Hay fondos retenidos en esta cuenta">
          Una operación está esperando tu confirmación. Búscala en el asistente del
          resumen: si no respondes, la reserva se libera sola.
        </Notice>
      )}

      <section className="card" aria-labelledby="movimientos">
        <div className="flex items-baseline justify-between gap-3 border-b border-rule px-4 py-3">
          <h2 id="movimientos" className="type-eyebrow">
            Movimientos de esta cuenta
          </h2>
          <Link
            to={`/historial`}
            className="text-[0.8125rem] font-medium text-copper hover:underline"
          >
            Ver con filtros
          </Link>
        </div>
        <div className="px-4 py-1">
          <MovementList
            movements={history.data?.transactions ?? []}
            ownedAccounts={ownedAccounts}
            loading={history.isLoading}
            emptyTitle="Esta cuenta no tiene movimientos"
            emptyBody="Cuando entre o salga dinero de aquí, lo verás en esta lista."
            emptyAction={
              <Link to="/mover" className="btn btn-secondary">
                Mover dinero
              </Link>
            }
          />
        </div>
        {history.data?.has_more && (
          <div className="border-t border-rule px-4 py-3 text-center">
            <Link to="/historial" className="text-[0.8125rem] font-medium text-copper hover:underline">
              Ver el historial completo de esta cuenta
            </Link>
          </div>
        )}
      </section>
    </div>
  )
}
