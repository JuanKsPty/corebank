import { ChevronRightIcon, CircleAlertIcon, UploadIcon } from 'lucide-react'
import { useRef } from 'react'
import { Link } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { Figure } from '@/components/primitives'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { useExternalAccounts, useImportBankStatement } from '@/lib/queries'

const INSTITUTION_LABEL: Record<string, string> = {
  banco_general: 'Banco General',
  bac: 'BAC',
}

const FORMAT_LABEL: Record<string, string> = {
  bg_card: 'Tarjeta de Banco General',
  bg_account: 'Cuenta de Banco General',
  bac_account: 'Cuenta de BAC',
}

/**
 * Importing bank statements.
 *
 * There is exactly one decision to make here — which file — and the account it
 * belongs to is never chosen by hand: the backend reads the institution and
 * account number straight out of the file itself. That is why this page has no
 * account picker anywhere, upload or otherwise; a file declares its own home.
 *
 * Each imported account is its own page (`/importar/:id`) rather than an inline
 * expand here — a card statement is how some customers actually run their
 * money, and it earns the same depth of screen a real account gets.
 */
export function ImportPage() {
  return (
    <div className="space-y-6">
      <header>
        <p className="type-eyebrow">Importar</p>
        <h1 className="type-display mt-1.5 text-[1.75rem]">Estados de cuenta</h1>
        <p className="mt-1.5 max-w-prose text-[0.9375rem] text-ink-soft">
          Sube el archivo tal como lo descargas del banco. La cuenta a la que pertenece se
          identifica sola.
        </p>
      </header>

      <UploadCard />
      <ExternalAccountsSection />
    </div>
  )
}

function UploadCard() {
  const upload = useImportBankStatement()
  const inputRef = useRef<HTMLInputElement>(null)

  const failure = upload.error
  const message =
    failure instanceof ApiError
      ? failure.message
      : failure
        ? 'No se pudo importar el archivo. Inténtalo de nuevo.'
        : null

  return (
    <section className="card p-5" aria-labelledby="subir">
      <h2 id="subir" className="type-eyebrow">
        Subir un archivo
      </h2>
      <p className="mt-1 text-[0.8125rem] text-ink-soft">
        Tarjeta o cuenta de Banco General (.txt / .xlsx), o cuenta de BAC (.csv).
      </p>

      <label
        className="mt-4 flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[5px] border border-dashed border-rule bg-paper-sunken px-4 py-8 text-center text-[0.875rem] text-ink-soft transition-colors hover:border-ink-faint aria-disabled:pointer-events-none aria-disabled:opacity-60"
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
            if (file) upload.mutate(file)
            // Cleared so choosing the exact same file twice in a row still fires
            // a change event the second time.
            event.target.value = ''
          }}
        />
      </label>

      {upload.isPending && (
        <p className="mt-3 flex items-center gap-2 text-[0.8125rem] text-ink-soft">
          <Spinner /> Leyendo el archivo…
        </p>
      )}

      {upload.isSuccess && upload.data && (
        <p className="mt-3 text-[0.8125rem] text-ink-soft">
          {FORMAT_LABEL[upload.data.format] ?? upload.data.format} · {upload.data.imported}{' '}
          importado{upload.data.imported === 1 ? '' : 's'}, {upload.data.skipped_duplicates}{' '}
          ya registrado{upload.data.skipped_duplicates === 1 ? '' : 's'} de{' '}
          {upload.data.total_rows} fila{upload.data.total_rows === 1 ? '' : 's'}.
        </p>
      )}

      {upload.isError && (
        <Alert variant="destructive" className="mt-3">
          <CircleAlertIcon />
          <AlertTitle>No se pudo importar el archivo</AlertTitle>
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      )}
    </section>
  )
}

function ExternalAccountsSection() {
  const accounts = useExternalAccounts()

  return (
    <section aria-labelledby="cuentas-externas">
      <h2 id="cuentas-externas" className="type-eyebrow mb-3">
        Cuentas importadas
      </h2>

      {accounts.isLoading && (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      )}

      {accounts.data && accounts.data.accounts.length === 0 && (
        <p className="text-[0.875rem] text-ink-soft">
          Todavía no has importado ningún archivo.
        </p>
      )}

      {accounts.data && accounts.data.accounts.length > 0 && (
        <ul className="grid gap-3 sm:grid-cols-2">
          {accounts.data.accounts.map((account) => (
            <li key={account.id}>
              <Link
                to={`/importar/${account.id}`}
                className="card group flex items-center justify-between gap-3 p-4 transition-colors hover:border-ink-faint"
              >
                <div className="min-w-0">
                  <p className="truncate text-[0.9375rem] font-medium">
                    {account.display_name}
                  </p>
                  <p className="type-figure mt-0.5 text-[0.75rem] text-ink-faint">
                    {INSTITUTION_LABEL[account.institution] ?? account.institution} ·{' '}
                    {account.account_number}
                  </p>
                  <p className="mt-2">
                    <Figure amount={account.declared_balance} size="lg" tone="faint" />
                  </p>
                </div>
                <ChevronRightIcon
                  className="size-4 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5"
                  aria-hidden="true"
                />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
