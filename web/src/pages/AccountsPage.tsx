import { LandmarkIcon, UploadIcon } from 'lucide-react'

import type { Account, NetWorth } from '@/api/types'
import { AccountCard } from '@/components/AccountCard'
import { DryRunDialog } from '@/components/DryRunDialog'
import { ImportStatementDialog } from '@/components/ImportStatementDialog'
import { LinkIBKRDialog } from '@/components/LinkIBKRDialog'
import { Figure } from '@/components/primitives'
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
import { useAccounts } from '@/lib/queries'

const SECTIONS: { title: string; match: (a: Account) => boolean }[] = [
  { title: 'Cuentas', match: (a) => a.type === 'checking' || a.type === 'savings' },
  { title: 'Tarjetas', match: (a) => a.type === 'credit_card' },
  { title: 'Inversiones', match: (a) => a.type === 'brokerage' },
]

/**
 * Every account the owner's files and IBKR link have brought in, grouped the way
 * a person thinks of them: what they have, what they owe, what they invest.
 */
export function AccountsPage() {
  const accounts = useAccounts()
  const list = accounts.data?.accounts ?? []

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="hidden md:block">
          <p className="type-eyebrow">Cuentas</p>
          <h1 className="type-display mt-1.5 text-[1.75rem]">Tus cuentas</h1>
        </div>
        <div className="flex flex-wrap gap-2">
          <DryRunDialog />
          <LinkIBKRDialog trigger={<Button variant="outline">Vincular IBKR</Button>} />
          <ImportStatementDialog
            trigger={
              <Button className="bg-copper text-primary-foreground hover:bg-copper/90">
                <UploadIcon aria-hidden="true" />
                Importar estado de cuenta
              </Button>
            }
          />
        </div>
      </header>

      {accounts.isLoading ? (
        <ul className="grid gap-3 sm:grid-cols-2">
          {[0, 1].map((i) => (
            <li key={i} className="card space-y-3 p-5">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-7 w-40" />
            </li>
          ))}
        </ul>
      ) : list.length === 0 ? (
        <Empty className="card">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <LandmarkIcon />
            </EmptyMedia>
            <EmptyTitle className="type-display text-[1.0625rem]">
              Todavía no hay cuentas
            </EmptyTitle>
            <EmptyDescription>
              Importa un estado de cuenta de tu banco o vincula tu cuenta de IBKR. La cuenta
              aparece sola, con el nombre y el número que trae el archivo.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <ImportStatementDialog
              trigger={<Button variant="outline">Importar estado de cuenta</Button>}
            />
          </EmptyContent>
        </Empty>
      ) : (
        <>
          {SECTIONS.map((section) => {
            const items = list.filter(section.match)
            if (items.length === 0) return null
            return (
              <section key={section.title} className="space-y-3">
                <h2 className="type-eyebrow">{section.title}</h2>
                <ul className="grid gap-3 sm:grid-cols-2">
                  {items.map((a) => (
                    <li key={a.id}>
                      <AccountCard account={a} />
                    </li>
                  ))}
                </ul>
              </section>
            )
          })}
          {accounts.data && <NetWorthSummary worth={accounts.data.net_worth} />}
        </>
      )}
    </div>
  )
}

export function NetWorthSummary({ worth }: { worth: NetWorth }) {
  return (
    <div className="space-y-1 border-t border-rule pt-4 text-[0.875rem] text-ink-soft">
      <p className="flex items-baseline gap-2">
        <span>Patrimonio neto</span>
        <Figure amount={worth.total} size="sm" tone="copper" />
        {worth.incomplete && (
          <span className="text-[0.75rem] text-hold-text">
            (incompleto: {worth.unanchored} cuenta{worth.unanchored === 1 ? '' : 's'} sin
            saldo inicial)
          </span>
        )}
      </p>
      <p className="text-[0.8125rem] text-ink-faint">
        Tienes <Figure amount={worth.assets} size="sm" tone="faint" /> · debes{' '}
        <Figure amount={worth.owed} size="sm" tone="faint" /> · inversiones{' '}
        <Figure amount={worth.holdings} size="sm" tone="faint" />
      </p>
    </div>
  )
}
