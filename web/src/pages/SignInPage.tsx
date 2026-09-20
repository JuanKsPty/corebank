import { CircleAlertIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import { fetchDeviceStatus } from '@/api/endpoints'
import { ApiError } from '@/api/client'
import type { DeviceStatus } from '@/api/types'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { useSession } from '@/lib/session'
import { useIsMobile } from '@/lib/useIsMobile'
import { emailProblem, isClean, pinProblem, type Errors } from '@/lib/validate'
import { AuthLayout, AuthLink } from './AuthLayout'

type FieldName = 'email' | 'password'

export function SignInPage() {
  const { signIn, signInWithPin } = useSession()
  const navigate = useNavigate()
  const location = useLocation()

  const state = location.state as { from?: string } | null

  const isMobile = useIsMobile()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<Errors<FieldName>>({})
  const [failure, setFailure] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // The PIN is a phone shortcut, on purpose: the point is that a phone stays
  // unlocked with a keypad while a computer keeps typing a password. So a
  // desktop never even asks whether this device is trusted — `device` starts
  // (and, without a phone in the picture, stays) at `{ trusted: false }`
  // rather than 'checking', which is what lets the e-mail form below render
  // immediately instead of behind a spinner nobody on a desktop needs.
  const [device, setDevice] = useState<DeviceStatus | 'checking'>(() =>
    isMobile ? 'checking' : { trusted: false },
  )
  // Set once the customer explicitly asks for the e-mail form instead of the PIN
  // this device offers — a shared computer, or someone else's turn to sign in.
  const [useEmailForm, setUseEmailForm] = useState(false)

  const [pin, setPin] = useState('')
  const [pinError, setPinError] = useState<string | undefined>()

  useEffect(() => {
    if (!isMobile) return

    let cancelled = false
    void (async () => {
      try {
        const status = await fetchDeviceStatus()
        if (!cancelled) setDevice(status)
      } catch {
        // A device that cannot be confirmed is treated as untrusted rather than
        // retried: the e-mail form below is always a safe fallback, and trusting
        // a device the server could not vouch for would not be.
        if (!cancelled) setDevice({ trusted: false })
      }
    })()
    return () => {
      cancelled = true
    }
  }, [isMobile])

  // Where the customer was headed before the session ended, so an expiry returns
  // them to the page they were on rather than to the dashboard.
  const returnTo = state?.from ?? '/'

  function validate(): Errors<FieldName> {
    return {
      email: emailProblem(email),
      // Deliberately not the full password policy: on a login the only thing that
      // matters is whether it is empty. Telling somebody their existing password
      // is too short would be nonsense.
      password: password ? undefined : 'La contraseña es obligatoria.',
    }
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()

    const found = validate()
    setErrors(found)
    if (!isClean(found)) return

    setSubmitting(true)
    setFailure(null)
    try {
      await signIn(email, password)
      navigate(returnTo, { replace: true })
    } catch (error) {
      if (error instanceof ApiError) {
        setFailure(error)
        // Field-level messages when the server sent them, so a rejected e-mail
        // lands on the e-mail input.
        if (error.fields) setErrors(error.fields as Errors<FieldName>)
      } else {
        setFailure(
          new ApiError(0, {
            code: 'network',
            message: 'No se pudo conectar con el servidor. Revisa tu conexión.',
          }),
        )
      }
    } finally {
      setSubmitting(false)
    }
  }

  async function handlePinSubmit(event: React.FormEvent) {
    event.preventDefault()

    const problem = pinProblem(pin)
    setPinError(problem)
    if (problem) return

    setSubmitting(true)
    setFailure(null)
    try {
      await signInWithPin(pin)
      navigate(returnTo, { replace: true })
    } catch (error) {
      setPin('')
      if (error instanceof ApiError) {
        if (error.code === 'device_not_trusted') {
          // The device itself is dead — too many wrong PINs, or it was disabled
          // from Seguridad in the meantime. A PIN box that can never work again
          // is worse than falling back to the form that always does.
          setDevice({ trusted: false })
        }
        setFailure(error)
      } else {
        setFailure(
          new ApiError(0, {
            code: 'network',
            message: 'No se pudo conectar con el servidor. Revisa tu conexión.',
          }),
        )
      }
    } finally {
      setSubmitting(false)
    }
  }

  if (device === 'checking') {
    return (
      <AuthLayout title="Entra a tus cuentas" subtitle="Un momento…" footer={null}>
        <div className="flex justify-center py-2">
          <Spinner />
        </div>
      </AuthLayout>
    )
  }

  const showPinForm = isMobile && device.trusted && !useEmailForm

  if (showPinForm) {
    const firstName = device.full_name?.split(' ')[0] ?? ''

    return (
      <AuthLayout
        title={firstName ? `Hola, ${firstName}` : 'Bienvenido de nuevo'}
        subtitle="Ingresa tu PIN para entrar."
        footer={
          <button
            type="button"
            onClick={() => setUseEmailForm(true)}
            className="font-medium text-copper underline decoration-copper/35 underline-offset-2 hover:decoration-copper"
          >
            Usar mi correo y contraseña
          </button>
        }
      >
        <form onSubmit={handlePinSubmit} noValidate className="space-y-4">
          {failure && (
            <Alert variant="destructive">
              <CircleAlertIcon />
              <AlertTitle>
                {failure.status === 429 ? 'Demasiados intentos' : 'No pudimos entrar'}
              </AlertTitle>
              <AlertDescription>{failure.message}</AlertDescription>
            </Alert>
          )}

          <Field data-invalid={pinError ? true : undefined}>
            <FieldLabel htmlFor="pin">PIN</FieldLabel>
            <Input
              id="pin"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              autoFocus
              maxLength={6}
              value={pin}
              aria-invalid={pinError ? true : undefined}
              onChange={(event) => {
                setPin(event.target.value.replace(/\D/g, '').slice(0, 6))
                if (pinError) setPinError(undefined)
              }}
              placeholder="••••••"
              className="text-center text-lg tracking-[0.5em]"
            />
            {pinError && <FieldError>{pinError}</FieldError>}
          </Field>

          <Button
            type="submit"
            disabled={submitting}
            className="w-full bg-copper text-primary-foreground hover:bg-copper/90"
          >
            {submitting && <Spinner />}
            {submitting ? 'Entrando' : 'Entrar'}
          </Button>
        </form>
      </AuthLayout>
    )
  }

  return (
    <AuthLayout
      title="Entra a tus cuentas"
      subtitle="Con tu correo y contraseña."
      footer={
        <>
          ¿No tienes cuenta? <AuthLink to="/registro">Ábrela en un minuto</AuthLink>.
          {isMobile && device.trusted && (
            <>
              {' '}
              <button
                type="button"
                onClick={() => setUseEmailForm(false)}
                className="font-medium text-copper underline decoration-copper/35 underline-offset-2 hover:decoration-copper"
              >
                Usar mi PIN
              </button>
              .
            </>
          )}
        </>
      }
    >
      <form onSubmit={handleSubmit} noValidate className="space-y-4">
        {failure && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>
              {failure.status === 429
                ? 'Demasiados intentos'
                : failure.status === 401
                  ? 'No pudimos entrar'
                  : 'No se pudo completar el acceso'}
            </AlertTitle>
            <AlertDescription>
              {failure.message}
              {/* The request id is shown on a server fault only, because it is the one
                  thing that makes the failure findable in the logs. */}
              {failure.status >= 500 && failure.requestId && (
                <span className="type-figure mt-1 block text-[0.6875rem] opacity-70">
                  ref {failure.requestId}
                </span>
              )}
            </AlertDescription>
          </Alert>
        )}

        <Field data-invalid={errors.email ? true : undefined}>
          <FieldLabel htmlFor="correo">Correo</FieldLabel>
          <Input
            id="correo"
            type="email"
            autoComplete="username"
            autoFocus
            value={email}
            aria-invalid={errors.email ? true : undefined}
            onChange={(event) => {
              setEmail(event.target.value)
              // The message clears as soon as the customer starts fixing it, rather
              // than staying until they submit again.
              if (errors.email) setErrors((previous) => ({ ...previous, email: undefined }))
            }}
            placeholder="tu@correo.com"
          />
          {errors.email && <FieldError>{errors.email}</FieldError>}
        </Field>

        <Field data-invalid={errors.password ? true : undefined}>
          <FieldLabel htmlFor="contrasena">Contraseña</FieldLabel>
          <Input
            id="contrasena"
            type="password"
            autoComplete="current-password"
            value={password}
            aria-invalid={errors.password ? true : undefined}
            onChange={(event) => {
              setPassword(event.target.value)
              if (errors.password)
                setErrors((previous) => ({ ...previous, password: undefined }))
            }}
          />
          {errors.password && <FieldError>{errors.password}</FieldError>}
        </Field>

        <Button
          type="submit"
          disabled={submitting}
          className="w-full bg-copper text-primary-foreground hover:bg-copper/90"
        >
          {submitting && <Spinner />}
          {submitting ? 'Entrando' : 'Entrar'}
        </Button>
      </form>
    </AuthLayout>
  )
}
