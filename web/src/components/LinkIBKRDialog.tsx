import { useState } from 'react'
import { CircleAlertIcon } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Spinner } from '@/components/ui/spinner'
import { useLinkIBKR, useRelinkIBKR } from '@/lib/queries'

/**
 * Connecting an IBKR account through a Flex Query. The first link creates the
 * brokerage account; given `accountId`, it replaces an existing link's query and
 * token instead.
 */
export function LinkIBKRDialog({
  trigger,
  accountId,
}: {
  trigger: React.ReactNode
  accountId?: string
}) {
  const [open, setOpen] = useState(false)
  const [ibkrAccountId, setIbkrAccountId] = useState('')
  const [flexQueryId, setFlexQueryId] = useState('')
  const [flexToken, setFlexToken] = useState('')
  const link = useLinkIBKR()
  const relink = useRelinkIBKR(accountId ?? '')
  const pending = link.isPending || relink.isPending
  const failure = link.error ?? relink.error
  const navigate = useNavigate()

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const done = () => {
      setOpen(false)
      setFlexToken('')
    }
    if (accountId) {
      relink.mutate(
        { flex_query_id: flexQueryId, flex_token: flexToken },
        { onSuccess: done },
      )
    } else {
      link.mutate(
        {
          ibkr_account_id: ibkrAccountId,
          flex_query_id: flexQueryId,
          flex_token: flexToken,
        },
        {
          onSuccess: (result) => {
            done()
            navigate(`/cuentas/${result.account_id}`)
          },
        },
      )
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) {
          link.reset()
          relink.reset()
        }
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-[26rem]">
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle className="type-display text-[1.25rem]">
              {accountId ? 'Cambiar la Flex Query' : 'Vincula tu cuenta de IBKR'}
            </DialogTitle>
            <DialogDescription>
              El token se guarda cifrado y no se vuelve a mostrar. En IBKR, usa una Activity
              Flex Query en XML con período móvil (por ejemplo «Last 365 Calendar Days») y
              las secciones Open Positions, Trades (Execution), Cash Transactions (Detail) y
              Cash Report.
            </DialogDescription>
          </DialogHeader>

          <fieldset className="grid gap-4" disabled={pending}>
            {!accountId && (
              <Field>
                <FieldLabel htmlFor="ibkr-account-id">Número de cuenta de IBKR</FieldLabel>
                <Input
                  id="ibkr-account-id"
                  value={ibkrAccountId}
                  onChange={(e) => setIbkrAccountId(e.target.value)}
                  placeholder="U1234567"
                  autoComplete="off"
                  required
                />
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor="flex-query-id">Query ID de la Flex Query</FieldLabel>
              <Input
                id="flex-query-id"
                value={flexQueryId}
                onChange={(e) => setFlexQueryId(e.target.value)}
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
                onChange={(e) => setFlexToken(e.target.value)}
                autoComplete="off"
                required
              />
            </Field>
          </fieldset>

          {failure && (
            <Alert variant="destructive">
              <CircleAlertIcon />
              <AlertDescription>
                {failure instanceof ApiError
                  ? failure.message
                  : 'No pudimos vincular la cuenta.'}
              </AlertDescription>
            </Alert>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              Cancelar
            </Button>
            <Button
              type="submit"
              disabled={pending}
              className="bg-copper text-primary-foreground hover:bg-copper/90"
            >
              {pending && <Spinner />}
              Vincular
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
