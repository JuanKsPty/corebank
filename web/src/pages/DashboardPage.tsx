import { LandmarkIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { AccountCard } from '@/components/AccountCard'
import { EntryList } from '@/components/EntryList'
import { ImportStatementDialog } from '@/components/ImportStatementDialog'
import {
  MonthlyFlow,
  SpendingByCategory,
  TransferSuggestions,
} from '@/components/ReportsSection'
import { Figure } from '@/components/primitives'
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
import { useAccounts, useCategories, useEntries } from '@/lib/queries'
import { useSession } from '@/lib/session'

/**
 * The summary: what the owner is worth, what they owe, and the latest movements
 * across every account — each figure from what their banks printed.
 */
export function DashboardPage() {
  const { user } = useSession()
  const accounts = useAccounts()
  const recent = useEntries({ limit: 8 })
  const categories = useCategories()
  const firstName = user?.full_name.split(' ')[0] ?? ''

  if (accounts.isError) {
    return (
      <Alert variant="destructive">
        <AlertTitle>No pudimos cargar tu resumen</AlertTitle>
        <AlertDescription>Vuelve a intentarlo en un momento.</AlertDescription>
      </Alert>
    )
  }

  const list = accounts.data?.accounts ?? []
  const worth = accounts.data?.net_worth

  if (accounts.isSuccess && list.length === 0) {
    return (
      <Empty className="card">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <LandmarkIcon />
          </EmptyMedia>
          <EmptyTitle className="type-display text-[1.25rem]">
            {firstName ? `Hola, ${firstName}` : 'Hola'}
          </EmptyTitle>
          <EmptyDescription>
            Empieza importando un estado de cuenta de tu banco o vinculando tu cuenta de
            IBKR. Tus cuentas aparecen solas, con los saldos que dice tu banco.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent className="flex flex-row flex-wrap justify-center gap-2">
          <ImportStatementDialog
            trigger={
              <Button className="bg-copper text-primary-foreground hover:bg-copper/90">
                Importar estado de cuenta
              </Button>
            }
          />
          <Button asChild variant="outline">
            <Link to="/cuentas">Vincular IBKR</Link>
          </Button>
        </EmptyContent>
      </Empty>
    )
  }

  return (
    <div className="space-y-6">
      <section aria-labelledby="patrimonio">
        <p className="type-eyebrow">Patrimonio neto</p>
        <h1 id="patrimonio" className="mt-1.5">
          {worth ? (
            <Figure amount={worth.total} size="display" className="type-display" />
          ) : (
            <Skeleton className="h-12 w-56" />
          )}
        </h1>
        {worth && (
          <p className="mt-2 text-[0.875rem] text-ink-soft">
            Tienes <Figure amount={worth.assets} size="sm" /> · debes{' '}
            <Figure amount={worth.owed} size="sm" /> · inversiones{' '}
            <Figure amount={worth.holdings} size="sm" />
          </p>
        )}
        {worth?.incomplete && (
          <p className="mt-1 text-[0.8125rem] text-hold-text">
            Incompleto: {worth.unanchored} cuenta{worth.unanchored === 1 ? '' : 's'} sin
            saldo inicial. Ábrela e ingresa el saldo de tu estado de cuenta.
          </p>
        )}
      </section>

      <TransferSuggestions accounts={list} />

      <div className="grid gap-4 lg:grid-cols-2">
        <SpendingByCategory categories={categories.data?.categories} />
        <MonthlyFlow />
      </div>

      <section className="space-y-3">
        <h2 className="type-eyebrow">Tus cuentas</h2>
        <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {list.map((a) => (
            <li key={a.id}>
              <AccountCard account={a} />
            </li>
          ))}
        </ul>
      </section>

      <section className="card">
        <div className="flex items-baseline justify-between px-4 pt-4">
          <h2 className="type-eyebrow">Movimientos recientes</h2>
          <Link
            to="/historial"
            className="text-[0.8125rem] text-ink-soft underline-offset-2 hover:underline"
          >
            Ver todo
          </Link>
        </div>
        <div className="px-4 py-1">
          <EntryList
            entries={recent.data?.entries ?? []}
            accounts={list}
            categories={categories.data?.categories}
            loading={recent.isLoading}
            emptyTitle="Aún no tienes movimientos"
          />
        </div>
      </section>
    </div>
  )
}
