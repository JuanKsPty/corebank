import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { Field, Notice, Spinner } from '@/components/primitives'
import { useSession } from '@/lib/session'
import { emailProblem, isClean, type Errors } from '@/lib/validate'
import { AuthLayout, AuthLink, DemoCredentials } from './AuthLayout'

type FieldName = 'email' | 'password'

export function SignInPage() {
  const { signIn } = useSession()
  const navigate = useNavigate()
  const location = useLocation()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<Errors<FieldName>>({})
  const [failure, setFailure] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // Where the customer was headed before the session ended, so an expiry returns
  // them to the page they were on rather than to the dashboard.
  const returnTo = (location.state as { from?: string } | null)?.from ?? '/'

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
          <Notice
            tone="error"
            title={
              failure.status === 429
                ? 'Demasiados intentos'
                : failure.status === 401
                  ? 'No pudimos entrar'
                  : 'No se pudo completar el acceso'
            }
            requestId={failure.status >= 500 ? failure.requestId : undefined}
            onDismiss={() => setFailure(null)}
          >
            {failure.message}
          </Notice>
        )}

        <Field label="Correo" error={errors.email}>
          {(props) => (
            <input
              {...props}
              type="email"
              className="field-input"
              autoComplete="username"
              autoFocus
              value={email}
              onChange={(event) => {
                setEmail(event.target.value)
                // The message clears as soon as the customer starts fixing it,
                // rather than staying until they submit again.
                if (errors.email) setErrors((previous) => ({ ...previous, email: undefined }))
              }}
              placeholder="tu@correo.com"
            />
          )}
        </Field>

        <Field label="Contraseña" error={errors.password}>
          {(props) => (
            <input
              {...props}
              type="password"
              className="field-input"
              autoComplete="current-password"
              value={password}
              onChange={(event) => {
                setPassword(event.target.value)
                if (errors.password) setErrors((previous) => ({ ...previous, password: undefined }))
              }}
            />
          )}
        </Field>

        <button type="submit" className="btn btn-primary w-full" disabled={submitting}>
          {submitting && <Spinner />}
          {submitting ? 'Entrando' : 'Entrar'}
        </button>
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
