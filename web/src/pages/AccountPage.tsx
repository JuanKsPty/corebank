import { CircleAlertIcon, PencilIcon } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { ApiError } from '@/api/client'
import type { Account } from '@/api/types'
import { MovementList } from '@/components/MovementList'
import { BalanceComposition, Figure } from '@/components/primitives'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { accountLabel, accountTypeAside, accountTypeLabel, formatDate } from '@/lib/format'
import { useHistory, useMe, useRenameAccount } from '@/lib/queries'
import { MAX_ALIAS } from '@/lib/validate'

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
        <Button asChild variant="outline" className="mt-5">
          <Link to="/panel">Ir al resumen</Link>
        </Button>
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
            {/* The name comes first, then the balance. On every other screen this
                account is one of several and the label is how it is picked out; on the
                page about this one account, the label is what the page is about. */}
            <p className="type-eyebrow">
              {accountLabel(account)} · abierta el {formatDate(account.created_at)}
            </p>
            <h1 className="mt-1.5">
              <span className="sr-only">Saldo disponible: </span>
              <Figure amount={account.available} size="display" className="type-display" />
            </h1>
            <p className="type-figure mt-1 text-[0.875rem] text-ink-soft">
              {account.account_number}
              {accountTypeAside(account) && (
                <span className="font-sans"> · {accountTypeAside(account)}</span>
              )}
            </p>

            <RenameAccount account={account} />

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
            <Skeleton className="h-3 w-40" />
            <Skeleton className="h-12 w-64" />
          </div>
        )}
      </header>

      {account && account.held.cents > 0 && (
        <Alert className="border-hold/40 bg-hold/8 text-hold-text">
          <CircleAlertIcon className="text-hold" />
          <AlertTitle>Hay fondos retenidos en esta cuenta</AlertTitle>
          <AlertDescription className="text-hold-text/85">
            Una operación está esperando tu confirmación. Ábrela en el asistente: si no
            respondes, la reserva se libera sola.
          </AlertDescription>
        </Alert>
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
              <Button asChild variant="outline">
                <Link to="/mover">Mover dinero</Link>
              </Button>
            }
          />
        </div>
        {history.data?.has_more && (
          <div className="border-t border-rule px-4 py-3 text-center">
            <Link
              to="/historial"
              className="text-[0.8125rem] font-medium text-copper hover:underline"
            >
              Ver el historial completo de esta cuenta
            </Link>
          </div>
        )}
      </section>
    </div>
  )
}

/**
 * Naming an account, from the page about that account.
 *
 * Shown as a line of text with a button rather than a form that is always open, because
 * naming an account is something somebody does once and then not again for months. An
 * input sitting permanently above the statement would be a field to skip past every
 * visit in exchange for a task nobody repeats.
 *
 * Clearing the field is a real action and not a mistake to guard against: it is how a
 * name is removed, and the account goes back to being labelled by its type. So the
 * button stays enabled on an empty field when the account currently has a name.
 */
function RenameAccount({ account }: { account: Account }) {
  const [editing, setEditing] = useState(false)
  const [alias, setAlias] = useState(account.alias)
  const rename = useRenameAccount()

  const start = () => {
    setAlias(account.alias)
    rename.reset()
    setEditing(true)
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    rename.mutate(
      { number: account.account_number, alias },
      { onSuccess: () => setEditing(false) },
    )
  }

  if (!editing) {
    return (
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" onClick={start}>
          <PencilIcon aria-hidden="true" />
          {account.alias ? 'Cambiar el alias' : 'Ponerle un alias'}
        </Button>
        {!account.alias && (
          <p className="text-[0.75rem] text-ink-faint">
            Útil si tienes otra cuenta del mismo tipo.
          </p>
        )}
      </div>
    )
  }

  const failure = rename.error
  const message =
    failure instanceof ApiError
      ? (failure.fields?.alias ?? failure.message)
      : failure
        ? 'No pudimos cambiar el alias. Inténtalo de nuevo.'
        : null

  return (
    <form onSubmit={submit} className="mt-4 max-w-md">
      <label htmlFor="rename-alias" className="text-[0.8125rem] font-medium text-ink-soft">
        Alias de la cuenta
      </label>
      <div className="mt-1.5 flex flex-wrap items-start gap-2">
        <Input
          id="rename-alias"
          value={alias}
          onChange={(event) => setAlias(event.target.value)}
          placeholder="Gastos del mes"
          maxLength={MAX_ALIAS}
          disabled={rename.isPending}
          autoComplete="off"
          autoFocus
          aria-invalid={message ? true : undefined}
          aria-describedby={message ? 'rename-alias-error' : undefined}
          className="min-w-0 flex-1"
        />
        <Button
          type="submit"
          disabled={rename.isPending || alias === account.alias}
          className="bg-ink text-paper hover:bg-ink/90"
        >
          {rename.isPending && <Spinner />}
          Guardar
        </Button>
        <Button
          type="button"
          variant="ghost"
          disabled={rename.isPending}
          onClick={() => setEditing(false)}
        >
          Cancelar
        </Button>
      </div>

      {message ? (
        <p
          id="rename-alias-error"
          role="alert"
          className="mt-1.5 text-[0.75rem] text-danger-text"
        >
          {message}
        </p>
      ) : (
        <p className="mt-1.5 text-[0.75rem] text-ink-faint">
          Déjalo vacío para volver a mostrarla como «
          {accountTypeLabel(account.account_type)}».
        </p>
      )}
    </form>
  )
}
