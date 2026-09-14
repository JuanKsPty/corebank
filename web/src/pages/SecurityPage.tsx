import { CircleAlertIcon } from 'lucide-react'
import { useState } from 'react'

import { ApiError } from '@/api/client'
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
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import {
  useDisableDeviceForPin,
  useEnableDeviceForPin,
  useRemovePin,
  useSecurityStatus,
  useSetPin,
} from '@/lib/queries'
import { isClean, pinProblem, type Errors } from '@/lib/validate'

type FieldName = 'current_password' | 'pin' | 'confirm_pin'

/**
 * Seguridad: where a PIN — a device-bound shortcut past the e-mail-and-password
 * form — is created, changed or removed, and where this browser's own quick
 * access is turned on or off.
 *
 * The PIN never stands on its own: SignInPage only offers it on a device this
 * page has explicitly trusted, which is why enabling it is a second, separate
 * step from creating the PIN itself.
 */
export function SecurityPage() {
  const status = useSecurityStatus()

  return (
    <div className="mx-auto max-w-2xl">
      <header>
        <p className="type-eyebrow">Seguridad</p>
        <h1 className="type-display mt-1.5 text-[1.75rem]">Acceso rápido con PIN</h1>
        <p className="mt-1.5 text-ink-soft">
          Un PIN de 6 dígitos te deja entrar más rápido desde este navegador, sin escribir
          tu correo y contraseña cada vez. Solo funciona en un dispositivo que actives aquí
          — en cualquier otro, se sigue pidiendo tu correo y contraseña.
        </p>
      </header>

      <div className="mt-6 space-y-5">
        {status.isLoading ? (
          <div className="card space-y-3 p-5" aria-hidden="true">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : (
          <>
            <PinCard hasPin={status.data?.has_pin ?? false} />
            <DeviceCard
              hasPin={status.data?.has_pin ?? false}
              deviceEnabled={status.data?.device_enabled ?? false}
            />
          </>
        )}
      </div>
    </div>
  )
}

function PinCard({ hasPin }: { hasPin: boolean }) {
  const setPin = useSetPin()

  const [currentPassword, setCurrentPassword] = useState('')
  const [pin, setPinValue] = useState('')
  const [confirmPin, setConfirmPin] = useState('')
  const [errors, setErrors] = useState<Errors<FieldName>>({})
  const [failure, setFailure] = useState<ApiError | null>(null)

  function validate(): Errors<FieldName> {
    return {
      current_password: currentPassword ? undefined : 'Ingresa tu contraseña actual.',
      pin: pinProblem(pin),
      confirm_pin: confirmPin === pin ? undefined : 'Los PIN no coinciden.',
    }
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()

    const found = validate()
    setErrors(found)
    if (!isClean(found)) return

    setFailure(null)
    try {
      await setPin.mutateAsync({ currentPassword, pin })
      setCurrentPassword('')
      setPinValue('')
      setConfirmPin('')
    } catch (error) {
      if (error instanceof ApiError) {
        setFailure(error)
        if (error.fields) setErrors(error.fields as Errors<FieldName>)
      } else {
        setFailure(
          new ApiError(0, {
            code: 'network',
            message: 'No se pudo conectar con el servidor. Revisa tu conexión.',
          }),
        )
      }
    }
  }

  return (
    <div className="card p-5">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-[0.9375rem] font-medium">
          {hasPin ? 'Cambiar tu PIN' : 'Crear un PIN'}
        </h2>
        {hasPin && <RemovePinDialog />}
      </div>

      <form onSubmit={handleSubmit} noValidate className="mt-4 space-y-4">
        {failure && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>No se pudo guardar el PIN</AlertTitle>
            <AlertDescription>{failure.message}</AlertDescription>
          </Alert>
        )}

        <Field data-invalid={errors.current_password ? true : undefined}>
          <FieldLabel htmlFor="contrasena-actual">Contraseña actual</FieldLabel>
          <Input
            id="contrasena-actual"
            type="password"
            autoComplete="current-password"
            value={currentPassword}
            aria-invalid={errors.current_password ? true : undefined}
            onChange={(event) => {
              setCurrentPassword(event.target.value)
              if (errors.current_password)
                setErrors((previous) => ({ ...previous, current_password: undefined }))
            }}
          />
          {errors.current_password && <FieldError>{errors.current_password}</FieldError>}
        </Field>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field data-invalid={errors.pin ? true : undefined}>
            <FieldLabel htmlFor="pin-nuevo">PIN nuevo</FieldLabel>
            <Input
              id="pin-nuevo"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              maxLength={6}
              value={pin}
              aria-invalid={errors.pin ? true : undefined}
              onChange={(event) => {
                setPinValue(event.target.value.replace(/\D/g, '').slice(0, 6))
                if (errors.pin) setErrors((previous) => ({ ...previous, pin: undefined }))
              }}
              className="text-center tracking-[0.4em]"
            />
            {errors.pin && <FieldError>{errors.pin}</FieldError>}
          </Field>

          <Field data-invalid={errors.confirm_pin ? true : undefined}>
            <FieldLabel htmlFor="pin-confirmar">Confirmar PIN</FieldLabel>
            <Input
              id="pin-confirmar"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              maxLength={6}
              value={confirmPin}
              aria-invalid={errors.confirm_pin ? true : undefined}
              onChange={(event) => {
                setConfirmPin(event.target.value.replace(/\D/g, '').slice(0, 6))
                if (errors.confirm_pin)
                  setErrors((previous) => ({ ...previous, confirm_pin: undefined }))
              }}
              className="text-center tracking-[0.4em]"
            />
            {errors.confirm_pin && <FieldError>{errors.confirm_pin}</FieldError>}
          </Field>
        </div>

        <Button
          type="submit"
          disabled={setPin.isPending}
          className="bg-copper text-white hover:bg-copper/90"
        >
          {setPin.isPending && <Spinner />}
          {hasPin ? 'Cambiar PIN' : 'Crear PIN'}
        </Button>
      </form>
    </div>
  )
}

/** Removing the PIN also revokes every device trusted to use one — see DeviceCard. */
function RemovePinDialog() {
  const removePin = useRemovePin()
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')

  const failure = removePin.error
  const message =
    failure instanceof ApiError
      ? (failure.fields?.current_password ?? failure.message)
      : failure
        ? 'No se pudo conectar con el servidor. Revisa tu conexión.'
        : null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) {
          setPassword('')
          removePin.reset()
        }
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          Quitar PIN
        </Button>
      </DialogTrigger>

      <DialogContent className="sm:max-w-[26rem]">
        <DialogHeader>
          <DialogTitle className="type-display text-[1.25rem]">Quitar el PIN</DialogTitle>
          <DialogDescription>
            También se desactiva el acceso rápido en cualquier dispositivo donde lo hayas
            activado. Confirma tu contraseña actual para continuar.
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-1.5">
          <label
            htmlFor="quitar-pin-password"
            className="text-[0.8125rem] font-medium text-ink-soft"
          >
            Contraseña actual
          </label>
          <Input
            id="quitar-pin-password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            disabled={removePin.isPending}
          />
        </div>

        {message && (
          <p role="alert" className="text-[0.8125rem] text-danger-text">
            {message}
          </p>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => setOpen(false)}
            disabled={removePin.isPending}
          >
            Cancelar
          </Button>
          <Button
            variant="destructive"
            disabled={removePin.isPending || !password}
            onClick={() =>
              removePin.mutate(password, {
                onSuccess: () => {
                  setOpen(false)
                  setPassword('')
                },
              })
            }
          >
            {removePin.isPending && <Spinner />}
            Quitar PIN
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DeviceCard({
  hasPin,
  deviceEnabled,
}: {
  hasPin: boolean
  deviceEnabled: boolean
}) {
  const enable = useEnableDeviceForPin()
  const disable = useDisableDeviceForPin()
  const [failure, setFailure] = useState<ApiError | null>(null)

  async function handleToggle() {
    setFailure(null)
    try {
      if (deviceEnabled) {
        await disable.mutateAsync()
      } else {
        await enable.mutateAsync()
      }
    } catch (error) {
      setFailure(
        error instanceof ApiError
          ? error
          : new ApiError(0, {
              code: 'network',
              message: 'No se pudo conectar con el servidor. Revisa tu conexión.',
            }),
      )
    }
  }

  const pending = enable.isPending || disable.isPending

  return (
    <div className="card p-5">
      <h2 className="text-[0.9375rem] font-medium">Este dispositivo</h2>

      {!hasPin ? (
        <p className="mt-2 text-[0.875rem] text-ink-soft">
          Crea un PIN arriba para poder activar el acceso rápido en este navegador.
        </p>
      ) : (
        <>
          <p className="mt-2 text-[0.875rem] text-ink-soft">
            {deviceEnabled
              ? 'El acceso rápido está activo en este navegador: la próxima vez podrás entrar con solo tu PIN.'
              : 'Actívalo para entrar la próxima vez desde este navegador con solo tu PIN, sin tu correo y contraseña.'}
          </p>

          {failure && (
            <Alert variant="destructive" className="mt-3">
              <CircleAlertIcon />
              <AlertDescription>{failure.message}</AlertDescription>
            </Alert>
          )}

          <Button
            type="button"
            variant={deviceEnabled ? 'outline' : undefined}
            className={
              deviceEnabled ? 'mt-4' : 'mt-4 bg-copper text-white hover:bg-copper/90'
            }
            disabled={pending}
            onClick={() => void handleToggle()}
          >
            {pending && <Spinner />}
            {deviceEnabled
              ? 'Desactivar en este dispositivo'
              : 'Activar en este dispositivo'}
          </Button>
        </>
      )}
    </div>
  )
}
