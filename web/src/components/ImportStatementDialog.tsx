import { useState } from 'react'
import { CircleAlertIcon, UploadIcon } from 'lucide-react'

import { ApiError } from '@/api/client'
import type { Account, ImportAccountResult, ImportResult } from '@/api/types'
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
import { accountLabel, formatCivilDate, money } from '@/lib/format'
import { useImportStatement } from '@/lib/queries'

/**
 * Importing a statement file.
 *
 * Which account a file belongs to is never chosen by hand: the file says, and the
 * account is created the first time it is seen. Opened from an account's own page,
 * the dialog passes that account along, and a file for any other account is refused
 * instead of being filed somewhere unexpected.
 */
export function ImportStatementDialog({
  trigger,
  account,
}: {
  trigger: React.ReactNode
  account?: Account
}) {
  const [open, setOpen] = useState(false)
  const upload = useImportStatement()

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) upload.reset()
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-[28rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.25rem]">
            Importar estado de cuenta
          </DialogTitle>
          <DialogDescription>
            {account ? <>Se importa a {accountLabel(account)}. </> : null}
            Tarjeta o cuenta de Banco General (.txt / .xlsx), o cuenta de BAC (.csv), tal
            como lo descargas del banco. Volver a subir un archivo, o uno que se solapa con
            otro, nunca duplica movimientos.
          </DialogDescription>
        </DialogHeader>

        <label
          className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[5px] border border-dashed border-rule bg-paper-sunken px-4 py-8 text-center text-[0.875rem] text-ink-soft transition-colors hover:border-ink-faint aria-disabled:pointer-events-none aria-disabled:opacity-60"
          aria-disabled={upload.isPending}
        >
          <UploadIcon className="size-5 text-ink-faint" aria-hidden="true" />
          {upload.isPending ? 'Importando…' : 'Elige un archivo'}
          <input
            type="file"
            accept=".txt,.csv,.xlsx"
            className="sr-only"
            disabled={upload.isPending}
            onChange={(event) => {
              const file = event.target.files?.[0]
              if (file) upload.mutate({ file, accountId: account?.id })
              event.target.value = ''
            }}
          />
        </label>

        {upload.isSuccess && upload.data && <ImportSummary result={upload.data} />}

        {upload.isError && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>No se pudo importar el archivo</AlertTitle>
            <AlertDescription>
              {upload.error instanceof ApiError
                ? upload.error.message
                : 'Inténtalo de nuevo en un momento.'}
            </AlertDescription>
          </Alert>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cerrar
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ImportSummary({ result }: { result: ImportResult }) {
  if (result.unchanged) {
    return (
      <p className="text-[0.8125rem] text-ink-soft">
        Este mismo archivo ya estaba importado; no cambió nada.
      </p>
    )
  }
  return (
    <div className="space-y-2">
      {result.accounts.map((a) => (
        <AccountSummary key={a.account_id} result={a} />
      ))}
    </div>
  )
}

function AccountSummary({ result }: { result: ImportAccountResult }) {
  return (
    <section className="space-y-1 rounded-[5px] border border-rule p-3 text-[0.8125rem]">
      <p className="font-medium">
        {result.display_name}
        {result.created && <span className="text-ink-faint"> · cuenta nueva</span>}
      </p>
      <p className="text-ink-soft">
        {formatCivilDate(result.period_start)} al {formatCivilDate(result.period_end)} ·{' '}
        {result.new} movimiento{result.new === 1 ? '' : 's'} nuevo
        {result.new === 1 ? '' : 's'}
        {result.duplicates > 0 &&
          `, ${result.duplicates} ya registrado${result.duplicates === 1 ? '' : 's'}`}
      </p>
      {result.opening && result.closing && (
        <p className="text-ink-faint">
          Saldo del banco: {money(result.opening)} → {money(result.closing)}
        </p>
      )}
      {result.needs_opening && (
        <p className="text-hold-text">
          Este archivo no trae saldos. Abre la tarjeta e ingresa el monto adeudado de tu
          estado de cuenta para que el saldo cuadre.
        </p>
      )}
      {result.warnings.map((w) => (
        <p key={w} className="text-hold-text">
          {w}
        </p>
      ))}
    </section>
  )
}
