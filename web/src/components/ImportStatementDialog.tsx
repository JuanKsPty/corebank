import { useRef, useState } from 'react'
import { CircleAlertIcon, UploadIcon } from 'lucide-react'

import { ApiError } from '@/api/client'
import type { Account, ImportResult } from '@/api/types'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { accountLabel } from '@/lib/format'
import { useImportBankStatement, useMe } from '@/lib/queries'

const FORMAT_LABEL: Record<string, string> = {
  bg_card: 'Tarjeta de Banco General',
  bg_account: 'Cuenta de Banco General',
  bac_account: 'Cuenta de BAC',
}

/**
 * Importing a bank statement.
 *
 * Which external account a file belongs to is never chosen by hand — the
 * backend reads that straight out of the file itself. Which of the
 * customer's real accounts a bank-account statement (never a card) posts
 * to is a separate choice, and this dialog settles it by *where it was
 * opened from* instead of a picker that could be gotten wrong: give it an
 * `account` (an account's own page) and every import goes there; leave it
 * out (the accounts list) and a bank-account file always opens a new
 * account, same as dropping the file always did before.
 */
export function ImportStatementDialog({
  trigger,
  account,
}: {
  trigger: React.ReactNode
  account?: Account
}) {
  const [open, setOpen] = useState(false)
  const upload = useImportBankStatement()
  const inputRef = useRef<HTMLInputElement>(null)
  const me = useMe()

  const failure = upload.error
  const message =
    failure instanceof ApiError
      ? (failure.fields?.account_number ?? failure.message)
      : failure
        ? 'No se pudo importar el archivo. Inténtalo de nuevo.'
        : null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) upload.reset()
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>

      <DialogContent className="sm:max-w-[26rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.25rem]">
            Importar estado de cuenta
          </DialogTitle>
          <DialogDescription>
            {account ? (
              <>Se importa a {accountLabel(account)}. </>
            ) : (
              <>
                Una cuenta bancaria (no una tarjeta) abre una cuenta nueva automáticamente.
                ¿Ya tienes la cuenta abierta? Impórtalo desde su propia página.{' '}
              </>
            )}
            Tarjeta o cuenta de Banco General (.txt / .xlsx), o cuenta de BAC (.csv) — tal
            como lo descargas del banco.
          </DialogDescription>
        </DialogHeader>

        <label
          className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[5px] border border-dashed border-rule bg-paper-sunken px-4 py-8 text-center text-[0.875rem] text-ink-soft transition-colors hover:border-ink-faint aria-disabled:pointer-events-none aria-disabled:opacity-60"
          aria-disabled={upload.isPending}
        >
          <UploadIcon className="size-5 text-ink-faint" aria-hidden="true" />
          {upload.isPending ? 'Procesando…' : 'Elige un archivo o arrástralo aquí'}
          <input
            ref={inputRef}
            type="file"
            accept=".txt,.csv,.xlsx"
            className="sr-only"
            disabled={upload.isPending}
            onChange={(event) => {
              const file = event.target.files?.[0]
              if (file) upload.mutate({ file, accountNumber: account?.account_number })
              // Cleared so choosing the exact same file twice in a row still fires a
              // change event the second time.
              event.target.value = ''
            }}
          />
        </label>

        {upload.isSuccess && upload.data && (
          <p className="text-[0.8125rem] text-ink-soft">
            {importSummary(upload.data, account, me.data?.accounts ?? [])}
          </p>
        )}

        {upload.isError && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>No se pudo importar el archivo</AlertTitle>
            <AlertDescription>{message}</AlertDescription>
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

/**
 * Describes what one import did, naming the real account when it posted to
 * one — the account this dialog was opened with, or (opened from the
 * accounts list) whichever one a repeat import resolved to behind the
 * scenes, looked up in the customer's own list for a friendly name.
 */
function importSummary(
  result: ImportResult,
  account: Account | undefined,
  allAccounts: Account[],
): string {
  const counts = `${result.imported} importado${result.imported === 1 ? '' : 's'}, ${
    result.skipped_duplicates
  } ya registrado${result.skipped_duplicates === 1 ? '' : 's'} de ${result.total_rows} fila${
    result.total_rows === 1 ? '' : 's'
  }`

  if (result.is_card) {
    return `${FORMAT_LABEL[result.format] ?? result.format} · ${counts}.`
  }
  if (account) {
    return `${counts} en ${accountLabel(account)}.`
  }

  const matched = allAccounts.find((a) => a.account_number === result.linked_account_number)
  const accountName = matched ? accountLabel(matched) : result.linked_account_number
  return `${counts} en tu cuenta ${accountName}.`
}
