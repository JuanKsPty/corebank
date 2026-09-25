import { useState } from 'react'
import {
  CircleAlertIcon,
  PencilIcon,
  RefreshCwIcon,
  Trash2Icon,
  UploadIcon,
} from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router-dom'

import { ApiError } from '@/api/client'
import type { Account, Checkpoint } from '@/api/types'
import { amountOf, headline, reconciliationNote } from '@/components/AccountCard'
import { CheckpointDialog } from '@/components/CheckpointDialog'
import { EntryList } from '@/components/EntryList'
import { ImportStatementDialog } from '@/components/ImportStatementDialog'
import { LinkIBKRDialog } from '@/components/LinkIBKRDialog'
import { ReconciliationPanel } from '@/components/ReconciliationPanel'
import { Figure } from '@/components/primitives'
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
  accountLabel,
  accountTypeLabel,
  formatCivilDate,
  formatDate,
  institutionLabel,
  money,
} from '@/lib/format'
import {
  useAccount,
  useCategories,
  useCheckpoints,
  useDeleteAccount,
  useDeleteCheckpoint,
  useEntryPages,
  useInvestmentLink,
  useInvestmentTrades,
  usePinCheckpoint,
  usePortfolio,
  useSyncInvestmentAccount,
  useUpdateAccount,
} from '@/lib/queries'
import { MAX_ALIAS } from '@/lib/validate'
import { cn } from '@/lib/utils'

const SOURCE_LABEL: Record<Checkpoint['source'], string> = {
  statement_opening: 'Inicio de estado de cuenta',
  statement_closing: 'Cierre de estado de cuenta',
  broker: 'Reporte de IBKR',
  manual: 'Ingresado por ti',
}

export function AccountPage() {
  const { id = '' } = useParams()
  const account = useAccount(id)
  const a = account.data

  if (account.isError) {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon />
        <AlertTitle>No encontramos esta cuenta</AlertTitle>
        <AlertDescription>
          <Link to="/cuentas" className="underline">
            Volver a tus cuentas
          </Link>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <div className="space-y-6">
      <header className="space-y-3">
        {a ? <Heading account={a} /> : <Skeleton className="h-12 w-64" />}
      </header>

      {a && <Actions account={a} />}
      {a && a.type === 'brokerage' && <InvestmentPanel account={a} />}
      {a && a.movements > 0 && <ReconciliationPanel account={a} />}
      {a && <Checkpoints account={a} />}
      {a && <Movements account={a} />}
      {a && <DangerZone account={a} />}
    </div>
  )
}

function Heading({ account }: { account: Account }) {
  const { amount, label } = headline(account)
  const note = reconciliationNote(account)
  return (
    <>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="type-eyebrow">{accountTypeLabel(account.type)}</p>
          <h1 className="type-display mt-1.5 truncate text-[1.75rem]">
            {accountLabel(account)}
          </h1>
          <p className="type-figure mt-1 text-[0.8125rem] text-ink-faint">
            {institutionLabel(account.institution)} · {account.external_number}
          </p>
        </div>
        <RenameDialog account={account} />
      </div>
      <div>
        <Figure amount={amount} size="display" tone={account.anchored ? 'ink' : 'faint'} />
        <p className="type-eyebrow mt-1">{label}</p>
        {account.type === 'brokerage' && (
          <p className="type-figure mt-1 text-[0.8125rem] text-ink-faint">
            Efectivo {money(account.balance)} · Posiciones{' '}
            {money(account.holdings ?? amountOf(0))}
          </p>
        )}
        <p
          className={cn(
            'mt-2 text-[0.8125rem]',
            note.tone === 'warn'
              ? 'text-hold-text'
              : note.tone === 'ok'
                ? 'text-credit'
                : 'text-ink-faint',
          )}
        >
          {note.text}
        </p>
      </div>
    </>
  )
}

function Actions({ account }: { account: Account }) {
  return (
    <div className="flex flex-wrap gap-2">
      {account.type !== 'brokerage' && (
        <ImportStatementDialog
          account={account}
          trigger={
            <Button variant="outline">
              <UploadIcon aria-hidden="true" />
              Importar estado de cuenta
            </Button>
          }
        />
      )}
      <CheckpointDialog
        account={account}
        anchor={!account.anchored}
        trigger={
          <Button variant={account.anchored ? 'outline' : 'default'}>
            {account.class === 'liability'
              ? 'Ingresar monto adeudado'
              : 'Ingresar saldo del banco'}
          </Button>
        }
      />
    </div>
  )
}

function Checkpoints({ account }: { account: Account }) {
  const list = useCheckpoints(account.id)
  const pin = usePinCheckpoint(account.id)
  const remove = useDeleteCheckpoint(account.id)
  const items = [...(list.data?.checkpoints ?? [])].reverse()
  if (items.length === 0) return null

  return (
    <section className="card p-4">
      <h2 className="type-eyebrow mb-3">Saldos declarados</h2>
      <ul className="divide-y divide-rule text-[0.8125rem]">
        {items.map((c) => (
          <li key={c.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-2">
            <span className="type-figure w-28 shrink-0 text-ink-faint">
              {formatCivilDate(c.as_of)}
            </span>
            <Figure
              amount={
                account.class === 'liability' ? amountOf(-c.balance.cents) : c.balance
              }
              size="sm"
            />
            <span className="text-ink-soft">
              {SOURCE_LABEL[c.source]}
              {c.pinned && ' · saldo de partida'}
            </span>
            <span className="ml-auto flex gap-1">
              <Button
                variant="ghost"
                size="sm"
                disabled={pin.isPending}
                onClick={() => pin.mutate(c.pinned ? null : c.id)}
              >
                {c.pinned ? 'Quitar como partida' : 'Usar como partida'}
              </Button>
              {c.source === 'manual' && (
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(c.id)}
                  aria-label="Borrar este saldo"
                >
                  <Trash2Icon aria-hidden="true" />
                </Button>
              )}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

function Movements({ account }: { account: Account }) {
  const pages = useEntryPages({ accountId: account.id, limit: 50 })
  const categories = useCategories()
  const entries = pages.data?.pages.flatMap((p) => p.entries) ?? []

  return (
    <section className="card">
      <div className="px-4 pt-4">
        <h2 className="type-eyebrow">Movimientos</h2>
      </div>
      <div className="px-4 py-1">
        <EntryList
          entries={entries}
          accounts={[account]}
          categories={categories.data?.categories}
          loading={pages.isLoading}
          showAccount={false}
          emptyTitle="Esta cuenta no tiene movimientos"
          emptyBody={
            account.type === 'brokerage'
              ? 'Sincroniza con IBKR para traer tus movimientos.'
              : 'Importa un estado de cuenta de esta cuenta y sus movimientos aparecerán aquí.'
          }
        />
      </div>
      {pages.hasNextPage && (
        <div className="border-t border-rule p-3 text-center">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => pages.fetchNextPage()}
            disabled={pages.isFetchingNextPage}
          >
            {pages.isFetchingNextPage && <Spinner />}
            Ver más
          </Button>
        </div>
      )}
    </section>
  )
}

function InvestmentPanel({ account }: { account: Account }) {
  const link = useInvestmentLink(account.id)
  const sync = useSyncInvestmentAccount(account.id)
  const portfolio = usePortfolio(account.id)
  const trades = useInvestmentTrades(account.id)
  const result = sync.data

  return (
    <section className="card space-y-4 p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="type-eyebrow">Interactive Brokers</h2>
          <p className="mt-1 text-[0.8125rem] text-ink-faint">
            {link.data?.last_synced_at
              ? `Última sincronización: ${formatDate(link.data.last_synced_at)}`
              : 'Todavía no se ha sincronizado.'}
          </p>
        </div>
        <div className="flex gap-2">
          <LinkIBKRDialog
            accountId={account.id}
            trigger={
              <Button variant="ghost" size="sm">
                Cambiar Flex Query
              </Button>
            }
          />
          <Button
            variant="outline"
            size="sm"
            onClick={() => sync.mutate()}
            disabled={sync.isPending}
          >
            {sync.isPending ? <Spinner /> : <RefreshCwIcon aria-hidden="true" />}
            {sync.isPending ? 'Sincronizando' : 'Sincronizar ahora'}
          </Button>
        </div>
      </div>

      {link.data?.last_sync_status === 'error' &&
        link.data.last_sync_error &&
        !sync.isPending && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>La última sincronización falló</AlertTitle>
            <AlertDescription>{link.data.last_sync_error}</AlertDescription>
          </Alert>
        )}
      {sync.isError && (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>No se pudo sincronizar</AlertTitle>
          <AlertDescription>
            {sync.error instanceof ApiError
              ? sync.error.message
              : 'Inténtalo de nuevo en un momento.'}
          </AlertDescription>
        </Alert>
      )}
      {result && (
        <div className="space-y-2 text-[0.8125rem] text-ink-soft">
          <p>
            {result.period_from && result.period_to
              ? `Datos de IBKR del ${formatCivilDate(result.period_from)} al ${formatCivilDate(result.period_to)}. `
              : 'El reporte de IBKR no indica su período. '}
            Efectivo: {result.cash_new} nuevo(s), {result.cash_duplicate} ya registrado(s) ·
            Operaciones: {result.trades_new} nueva(s) · Posiciones: {result.positions}
            {result.reported_cash &&
              ` · Efectivo según IBKR: ${money(result.reported_cash)}`}
          </p>
          {result.warnings.length > 0 && (
            <Alert className="border-hold/40 bg-hold/8 text-hold-text">
              <CircleAlertIcon className="text-hold" />
              <AlertTitle>La sincronización terminó, pero revisa esto</AlertTitle>
              <AlertDescription>
                <ul className="list-disc space-y-1 pl-4">
                  {result.warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}
        </div>
      )}

      {portfolio.data && portfolio.data.positions.length > 0 && (
        <div>
          <h3 className="type-eyebrow mb-2">Posiciones</h3>
          <ul className="divide-y divide-rule text-[0.8125rem]">
            {portfolio.data.positions.map((p) => (
              <li key={p.symbol} className="flex items-baseline gap-3 py-2">
                <span className="w-16 font-medium">{p.symbol}</span>
                <span className="type-figure text-ink-faint">{p.quantity}</span>
                <span className="ml-auto">
                  {p.market_value && <Figure amount={p.market_value} size="sm" />}
                </span>
              </li>
            ))}
          </ul>
          <p className="mt-1 text-[0.75rem] text-ink-faint">
            Al {formatCivilDate(portfolio.data.positions[0]!.as_of)}, según IBKR.
          </p>
        </div>
      )}

      {trades.data && trades.data.trades.length > 0 && (
        <div>
          <h3 className="type-eyebrow mb-2">Operaciones recientes</h3>
          <ul className="divide-y divide-rule text-[0.8125rem]">
            {trades.data.trades.map((t) => (
              <li key={t.id} className="flex items-baseline gap-3 py-2">
                <span className="type-figure w-24 text-ink-faint">
                  {formatCivilDate(t.trade_date)}
                </span>
                <span>
                  {t.side === 'buy' ? 'Compra' : 'Venta'} {t.quantity} {t.symbol}
                </span>
                <span className="ml-auto">
                  <Figure amount={t.net_cash} size="sm" />
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}

function RenameDialog({ account }: { account: Account }) {
  const [open, setOpen] = useState(false)
  const [alias, setAlias] = useState(account.alias ?? '')
  const [type, setType] = useState(account.type)
  const update = useUpdateAccount(account.id)
  const bank = account.type === 'checking' || account.type === 'savings'

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (next) {
          setAlias(account.alias ?? '')
          setType(account.type)
        } else update.reset()
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" aria-label="Renombrar la cuenta">
          <PencilIcon aria-hidden="true" />
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-[24rem]">
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            const patch: { alias?: string; type?: 'checking' | 'savings' } = { alias }
            if (bank && type !== account.type) patch.type = type as 'checking' | 'savings'
            update.mutate(patch, { onSuccess: () => setOpen(false) })
          }}
        >
          <DialogHeader>
            <DialogTitle className="type-display text-[1.25rem]">
              Nombre de la cuenta
            </DialogTitle>
            <DialogDescription>
              Cómo la reconoces. Déjalo vacío para usar el del banco.
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="alias">Alias</FieldLabel>
            <Input
              id="alias"
              value={alias}
              maxLength={MAX_ALIAS}
              onChange={(e) => setAlias(e.target.value)}
            />
          </Field>
          {bank && (
            <div className="flex gap-4 text-[0.875rem]">
              {(['checking', 'savings'] as const).map((t) => (
                <label key={t} className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="type"
                    checked={type === t}
                    onChange={() => setType(t)}
                  />
                  {accountTypeLabel(t)}
                </label>
              ))}
            </div>
          )}
          {update.isError && (
            <Alert variant="destructive">
              <CircleAlertIcon />
              <AlertDescription>
                {update.error instanceof ApiError
                  ? (update.error.fields?.alias ?? update.error.message)
                  : 'No se pudo guardar.'}
              </AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              Cancelar
            </Button>
            <Button
              type="submit"
              disabled={update.isPending}
              className="bg-copper text-primary-foreground hover:bg-copper/90"
            >
              {update.isPending && <Spinner />}
              Guardar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DangerZone({ account }: { account: Account }) {
  const [open, setOpen] = useState(false)
  const remove = useDeleteAccount()
  const navigate = useNavigate()

  return (
    <div className="border-t border-rule pt-4">
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>
          <Button variant="ghost" size="sm" className="text-danger-text">
            <Trash2Icon aria-hidden="true" />
            Eliminar esta cuenta
          </Button>
        </DialogTrigger>
        <DialogContent className="sm:max-w-[24rem]">
          <DialogHeader>
            <DialogTitle className="type-display text-[1.25rem]">
              ¿Eliminar {accountLabel(account)}?
            </DialogTitle>
            <DialogDescription>
              Se borran sus {account.movements} movimientos, sus saldos declarados y sus
              importaciones{account.type === 'brokerage' ? ', y el vínculo con IBKR' : ''}.
              Volver a importar sus archivos la recrea igual.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancelar
            </Button>
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() =>
                remove.mutate(account.id, { onSuccess: () => navigate('/cuentas') })
              }
            >
              {remove.isPending && <Spinner />}
              Eliminar
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
