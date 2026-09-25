import { useState } from 'react'
import { CircleAlertIcon, FileSearchIcon, UploadIcon } from 'lucide-react'

import { ApiError } from '@/api/client'
import type { Amount, StatementReport } from '@/api/types'
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
import { formatCivilDate, money } from '@/lib/format'
import { useDryRunStatement } from '@/lib/queries'

/**
 * Reads a statement file without importing it.
 *
 * It shows what the parser took from the file — the account, the period and the
 * balances the bank printed — so it can be compared with the bank's own document
 * before anything is stored. Nothing here writes.
 */
export function DryRunDialog() {
  const [open, setOpen] = useState(false)
  const check = useDryRunStatement()

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) check.reset()
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost">
          <FileSearchIcon aria-hidden="true" />
          Revisar un archivo
        </Button>
      </DialogTrigger>

      <DialogContent className="sm:max-w-[30rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.25rem]">
            Revisar un archivo
          </DialogTitle>
          <DialogDescription>
            Lee el estado de cuenta y muestra lo que entiende de él, sin importar nada.
            Compara los saldos con los de tu banco.
          </DialogDescription>
        </DialogHeader>

        <label
          className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[5px] border border-dashed border-rule bg-paper-sunken px-4 py-6 text-center text-[0.875rem] text-ink-soft transition-colors hover:border-ink-faint aria-disabled:pointer-events-none aria-disabled:opacity-60"
          aria-disabled={check.isPending}
        >
          <UploadIcon className="size-5 text-ink-faint" aria-hidden="true" />
          {check.isPending ? 'Leyendo…' : 'Elige un archivo'}
          <input
            type="file"
            accept=".txt,.csv,.xlsx"
            className="sr-only"
            disabled={check.isPending}
            onChange={(event) => {
              const file = event.target.files?.[0]
              if (file) check.mutate(file)
              event.target.value = ''
            }}
          />
        </label>

        {check.isError && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>No se pudo leer el archivo</AlertTitle>
            <AlertDescription>
              {check.error instanceof ApiError
                ? check.error.message
                : 'Inténtalo de nuevo.'}
            </AlertDescription>
          </Alert>
        )}

        {check.data?.statements.map((s) => (
          <Report key={`${s.institution}-${s.external_number}`} report={s} />
        ))}

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cerrar
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Report({ report }: { report: StatementReport }) {
  const rows: [string, string][] = [
    ['Cuenta', `${report.display_name} · ${report.external_number}`],
    [
      'Período',
      report.period_start && report.period_end
        ? `${formatCivilDate(report.period_start)} al ${formatCivilDate(report.period_end)}`
        : '—',
    ],
    ['Movimientos', String(report.line_count)],
    ['Saldo inicial', show(report.opening)],
    ['Saldo final', show(report.closing)],
    ['Entradas', money(report.money_in)],
    ['Salidas', money(report.money_out)],
  ]

  return (
    <section className="space-y-2 rounded-[5px] border border-rule p-3">
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[0.8125rem]">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-ink-faint">{label}</dt>
            <dd className="type-figure text-right">{value}</dd>
          </div>
        ))}
      </dl>
      {report.class === 'liability' && !report.opening && (
        <p className="text-[0.75rem] text-ink-soft">
          Este archivo de tarjeta no trae saldos: el saldo adeudado se toma del estado de
          cuenta en PDF.
        </p>
      )}
      {report.warnings.length > 0 ? (
        <ul className="list-disc space-y-1 pl-4 text-[0.75rem] text-hold-text">
          {report.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : (
        report.opening && (
          <p className="text-[0.75rem] text-credit">
            Los saldos del banco cuadran con los movimientos.
          </p>
        )
      )}
    </section>
  )
}

function show(amount?: Amount): string {
  return amount ? money(amount) : '—'
}
