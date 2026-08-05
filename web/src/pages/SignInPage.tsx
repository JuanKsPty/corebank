import { CircleAlertIcon } from 'lucide-react'
import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { useSession } from '@/lib/session'
import { emailProblem, isClean, type Errors } from '@/lib/validate'
import { AuthLayout, AuthLink, DEMO_ACCOUNTS, DemoCredentials } from './AuthLayout'

type FieldName = 'email' | 'password'

export function SignInPage() {
  const { signIn } = useSession()
  const navigate = useNavigate()
  const location = useLocation()

  const state = location.state as { from?: string; demo?: boolean } | null

  // Arriving from the landing page's "entrar con la cuenta de prueba" fills the form,
  // so that button costs one click rather than one click and a look-up. It fills
  // rather than submits: signing somebody in before they have seen the screen takes
  // the decision away from them, and the credentials are worth seeing — they are how
  // anybody evaluating this gets back in later.
  const demo = state?.demo ? DEMO_ACCOUNTS[0] : null

  const [email, setEmail] = useState(demo?.email ?? '')
  const [password, setPassword] = useState(demo?.password ?? '')
  const [errors, setErrors] = useState<Errors<FieldName>>({})
  const [failure, setFailure] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)

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

  return (
    <AuthLayout
      title="Entra a tus cuentas"
      subtitle="Con tu correo y contraseña."
      footer={
        <>
          ¿No tienes cuenta? <AuthLink to="/registro">Ábrela en un minuto</AuthLink>.
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
          className="w-full bg-copper text-white hover:bg-copper/90"
        >
          {submitting && <Spinner />}
          {submitting ? 'Entrando' : 'Entrar'}
        </Button>
      </form>

      <div className="mt-5">
        <DemoCredentials
          onUse={(demoEmail, demoPassword) => {
            setEmail(demoEmail)
            setPassword(demoPassword)
            setErrors({})
            setFailure(null)
          }}
        />
      </div>
    </AuthLayout>
  )
}
