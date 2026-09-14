import { CircleAlertIcon, PencilIcon } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { ApiError } from '@/api/client'
import type { Account, InvestmentLinkStatus } from '@/api/types'
import { MovementList } from '@/components/MovementList'
import { BalanceComposition, Figure } from '@/components/primitives'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
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
import { accountLabel, accountTypeAside, accountTypeLabel, formatDate } from '@/lib/format'
import {
  useHistory,
  useInvestmentLink,
  useInvestmentTrades,
  useLinkInvestmentAccount,
  useMe,
  usePortfolio,
  useRenameAccount,
  useSyncInvestmentAccount,
} from '@/lib/queries'
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

      {account && account.account_type === 'investment' && (
        <InvestmentSection account={account} />
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

/**
 * An investment account's IBKR portfolio.
 *
 * Not linked yet and linked are two different screens, not one screen with a
 * conditional banner: before a link exists there is nothing to show but a way to
 * create one, and after it exists the account's own data is what the page is about.
 */
function InvestmentSection({ account }: { account: Account }) {
  const link = useInvestmentLink(account.account_number)

  // ibkr_not_linked is the normal shape of "no link yet", not a fault — every
  // other failure to load the link's own status is shown as one.
  const notLinked =
    link.isError && link.error instanceof ApiError && link.error.code === 'ibkr_not_linked'

  return (
    <section className="card" aria-labelledby="inversiones">
      <div className="border-b border-rule px-4 py-3">
        <h2 id="inversiones" className="type-eyebrow">
          Portafolio de IBKR
        </h2>
      </div>

      <div className="space-y-5 px-4 py-4">
        {link.isLoading && (
          <div className="space-y-2">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-4 w-64" />
          </div>
        )}

        {notLinked && <LinkAccountForm account={account} />}

        {link.isError && !notLinked && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>No se pudo cargar el estado de la cuenta de IBKR</AlertTitle>
            <AlertDescription>Inténtalo de nuevo en un momento.</AlertDescription>
          </Alert>
        )}

        {link.isSuccess && link.data && (
          <LinkedPortfolio account={account} link={link.data} />
        )}
      </div>
    </section>
  )
}

/**
 * The one-time form that creates a link.
 *
 * A dialog rather than an inline form, unlike `RenameAccount`: entering a broker
 * token is not something to leave half-typed in the middle of the page, and a
 * dialog is where this codebase already puts an action taken once and rarely
 * repeated (`OpenAccountDialog` in `AccountsPage.tsx`).
 */
function LinkAccountForm({ account }: { account: Account }) {
  const [open, setOpen] = useState(false)
  const [ibkrAccountId, setIbkrAccountId] = useState('')
  const [flexQueryId, setFlexQueryId] = useState('')
  const [flexToken, setFlexToken] = useState('')
  const link = useLinkInvestmentAccount()

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    link.mutate(
      {
        accountNumber: account.account_number,
        input: {
          ibkr_account_id: ibkrAccountId,
          flex_query_id: flexQueryId,
          flex_token: flexToken,
        },
      },
      {
        onSuccess: () => {
          setOpen(false)
          setIbkrAccountId('')
          setFlexQueryId('')
          setFlexToken('')
          link.reset()
        },
      },
    )
  }

  const failure = link.error
  const message =
    failure instanceof ApiError
      ? (failure.fields?.flex_token ?? failure.fields?.flex_query_id ?? failure.message)
      : failure
        ? 'No pudimos vincular la cuenta. Inténtalo de nuevo.'
        : null

  return (
    <div>
      <p className="text-[0.8125rem] text-ink-soft">
        Conecta esta cuenta con una Flex Query de Interactive Brokers para traer tu efectivo
        y tus posiciones reales.
      </p>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) link.reset()
        }}
      >
        <DialogTrigger asChild>
          <Button variant="outline" size="sm" className="mt-3">
            Vincular con IBKR
          </Button>
        </DialogTrigger>

        <DialogContent className="sm:max-w-[26rem]">
          <form onSubmit={submit}>
            <DialogHeader>
              <DialogTitle className="type-display text-[1.25rem]">
                Vincula tu cuenta de IBKR
              </DialogTitle>
              <DialogDescription>
                El token se guarda cifrado y no se vuelve a mostrar después de esto.
              </DialogDescription>
            </DialogHeader>

            <fieldset className="grid gap-4 py-4" disabled={link.isPending}>
              <Field>
                <FieldLabel htmlFor="ibkr-account-id">Número de cuenta de IBKR</FieldLabel>
                <Input
                  id="ibkr-account-id"
                  value={ibkrAccountId}
                  onChange={(event) => setIbkrAccountId(event.target.value)}
                  placeholder="U13446202"
                  autoComplete="off"
                  required
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="flex-query-id">Query ID de la Flex Query</FieldLabel>
                <Input
                  id="flex-query-id"
                  value={flexQueryId}
                  onChange={(event) => setFlexQueryId(event.target.value)}
                  autoComplete="off"
                  required
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="flex-token">Token de Flex Web Service</FieldLabel>
                <Input
                  id="flex-token"
                  type="password"
                  value={flexToken}
                  onChange={(event) => setFlexToken(event.target.value)}
                  autoComplete="off"
                  required
                />
              </Field>
            </fieldset>

            {message && (
              <p role="alert" className="mb-2 text-[0.8125rem] text-danger-text">
                {message}
              </p>
            )}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setOpen(false)}
                disabled={link.isPending}
              >
                Cancelar
              </Button>
              <Button
                type="submit"
                disabled={link.isPending || !ibkrAccountId || !flexQueryId || !flexToken}
                className="bg-copper text-white hover:bg-copper/90"
              >
                {link.isPending && <Spinner />}
                Vincular
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}

/** A linked account's sync control and its last-known portfolio. */
function LinkedPortfolio({
  account,
  link,
}: {
  account: Account
  link: InvestmentLinkStatus
}) {
  const sync = useSyncInvestmentAccount()
  const portfolio = usePortfolio(account.account_number)
  const trades = useInvestmentTrades(account.account_number)

  return (
    <div className="space-y-5">
      <div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-[0.8125rem] text-ink-faint">
            {link.last_synced_at
              ? `Última sincronización: ${formatDate(link.last_synced_at)}`
              : 'Todavía no se ha sincronizado.'}
          </p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => sync.mutate(account.account_number)}
            disabled={sync.isPending}
          >
            {sync.isPending && <Spinner />}
            {sync.isPending ? 'Sincronizando' : 'Sincronizar ahora'}
          </Button>
        </div>

        {link.last_sync_status === 'error' && link.last_sync_error && (
          <Alert variant="destructive" className="mt-3">
            <CircleAlertIcon />
            <AlertTitle>La última sincronización falló</AlertTitle>
            <AlertDescription>{link.last_sync_error}</AlertDescription>
          </Alert>
        )}

        {sync.isSuccess && sync.data && (
          <p className="mt-2 text-[0.8125rem] text-ink-soft">
            Efectivo: {sync.data.cash_movements_posted} nuevo(s),{' '}
            {sync.data.cash_movements_skipped} ya registrado(s) · Operaciones:{' '}
            {sync.data.trades_recorded} nueva(s) · Posiciones: {sync.data.positions}
          </p>
        )}

        {sync.isError && (
          <Alert variant="destructive" className="mt-3">
            <CircleAlertIcon />
            <AlertTitle>No se pudo sincronizar</AlertTitle>
            <AlertDescription>
              {sync.error instanceof ApiError
                ? sync.error.message
                : 'Inténtalo de nuevo en un momento.'}
            </AlertDescription>
          </Alert>
        )}
      </div>

      {portfolio.isLoading && (
        <div className="space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      )}

      {portfolio.data && (
        <>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <p className="type-eyebrow">Efectivo</p>
              <Figure amount={portfolio.data.cash} size="lg" className="mt-1" />
            </div>
            <div>
              <p className="type-eyebrow">Posiciones</p>
              <Figure amount={portfolio.data.holdings_value} size="lg" className="mt-1" />
            </div>
            <div>
              <p className="type-eyebrow">Total</p>
              <Figure
                amount={portfolio.data.total_value}
                size="lg"
                tone="copper"
                className="mt-1"
              />
            </div>
          </div>

          {portfolio.data.positions.length === 0 ? (
            <p className="text-[0.8125rem] text-ink-faint">
              Todavía no hay posiciones registradas. Sincroniza para traerlas.
            </p>
          ) : (
            <div className="overflow-hidden rounded-[5px] border border-rule">
              <Table className="table-fixed">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                      Símbolo
                    </TableHead>
                    <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 text-right align-bottom">
                      Cantidad
                    </TableHead>
                    <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 text-right align-bottom">
                      Precio
                    </TableHead>
                    <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 text-right align-bottom">
                      Valor
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {portfolio.data.positions.map((position) => (
                    <TableRow
                      key={position.symbol}
                      className="border-b border-rule last:border-0"
                    >
                      <TableCell className="px-3 py-2.5">
                        <span className="font-medium">{position.symbol}</span>
                        <span className="ml-1.5 text-[0.75rem] text-ink-faint">
                          {position.asset_class}
                        </span>
                      </TableCell>
                      <TableCell className="type-figure px-3 py-2.5 text-right text-[0.8125rem] text-ink-soft">
                        {position.quantity}
                      </TableCell>
                      <TableCell className="px-3 py-2.5 text-right">
                        <Figure amount={position.mark_price} size="sm" />
                      </TableCell>
                      <TableCell className="px-3 py-2.5 text-right">
                        <Figure amount={position.market_value} size="sm" />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </>
      )}

      {trades.data && trades.data.trades.length > 0 && (
        <div>
          <p className="type-eyebrow mb-2">Operaciones recientes</p>
          <div className="overflow-hidden rounded-[5px] border border-rule">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                    Fecha
                  </TableHead>
                  <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                    Símbolo
                  </TableHead>
                  <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 align-bottom">
                    Lado
                  </TableHead>
                  <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 text-right align-bottom">
                    Cantidad
                  </TableHead>
                  <TableHead className="type-eyebrow h-auto px-3 pb-2 pt-2 text-right align-bottom">
                    Precio
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {trades.data.trades.map((trade, index) => (
                  <TableRow
                    // Trades have no id of their own on the wire — the tuple below is
                    // as unique as this list gets, and it is stable across refetches
                    // of the same data.
                    key={`${trade.symbol}-${trade.trade_date}-${index}`}
                    className="border-b border-rule last:border-0"
                  >
                    <TableCell className="type-figure px-3 py-2.5 text-[0.8125rem] text-ink-faint">
                      {formatDate(trade.trade_date)}
                    </TableCell>
                    <TableCell className="px-3 py-2.5 font-medium">
                      {trade.symbol}
                    </TableCell>
                    <TableCell className="px-3 py-2.5 text-[0.8125rem] text-ink-soft">
                      {trade.side === 'buy' ? 'Compra' : 'Venta'}
                    </TableCell>
                    <TableCell className="type-figure px-3 py-2.5 text-right text-[0.8125rem] text-ink-soft">
                      {trade.quantity}
                    </TableCell>
                    <TableCell className="px-3 py-2.5 text-right">
                      <Figure amount={trade.price} size="sm" />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      )}
    </div>
  )
}
