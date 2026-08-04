import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { Field, Notice, Spinner } from '@/components/primitives'
import { useSession } from '@/lib/session'
import {
  emailProblem,
  fullNameProblem,
  isClean,
  passwordProblem,
  type Errors,
} from '@/lib/validate'
import { AuthLayout, AuthLink } from './AuthLayout'

type FieldName = 'full_name' | 'email' | 'password' | 'account_type'

const ACCOUNT_TYPES = [
  { value: 'savings', label: 'Ahorros', detail: 'Para guardar' },
  { value: 'checking', label: 'Corriente', detail: 'Para el día a día' },
  { value: 'investment', label: 'Inversión', detail: 'Para hacer crecer' },
] as const

export function SignUpPage() {
  const { signUp } = useSession()
  const navigate = useNavigate()

  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [accountType, setAccountType] = useState<string>('savings')
  const [errors, setErrors] = useState<Errors<FieldName>>({})
  const [failure, setFailure] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const passwordIssue = password ? passwordProblem(password) : undefined

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()

    const found: Errors<FieldName> = {
      full_name: fullNameProblem(fullName),
      email: emailProblem(email),
      password: passwordProblem(password),
    }
    setErrors(found)
    if (!isClean(found)) return

    setSubmitting(true)
    setFailure(null)
    try {
      await signUp({
        full_name: fullName.trim(),
        email: email.trim(),
        password,
        account_type: accountType,
      })
      navigate('/', { replace: true })
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
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthLayout
      title="Abre tu cuenta"
      subtitle="Te creamos la cuenta bancaria en el momento."
      footer={
        <>
          ¿Ya eres cliente? <AuthLink to="/entrar">Entra</AuthLink>.
        </>
      }
    >
      <form onSubmit={handleSubmit} noValidate className="space-y-4">
        {/* A field-level failure is already shown on the field; this only appears
            for something the form cannot attribute to one input. */}
        {failure && !failure.fields && (
          <Notice
            tone="error"
            title="No se pudo abrir la cuenta"
            requestId={failure.status >= 500 ? failure.requestId : undefined}
            onDismiss={() => setFailure(null)}
          >
            {failure.message}
          </Notice>
        )}

        <Field label="Nombre completo" error={errors.full_name}>
          {(props) => (
            <input
              {...props}
              className="field-input"
              autoComplete="name"
              autoFocus
              value={fullName}
              onChange={(event) => {
                setFullName(event.target.value)
                if (errors.full_name) setErrors((p) => ({ ...p, full_name: undefined }))
              }}
              placeholder="Ana Pérez"
            />
          )}
        </Field>

        <Field label="Correo" error={errors.email}>
          {(props) => (
            <input
              {...props}
              type="email"
              className="field-input"
              autoComplete="username"
              value={email}
              onChange={(event) => {
                setEmail(event.target.value)
                if (errors.email) setErrors((p) => ({ ...p, email: undefined }))
              }}
              placeholder="tu@correo.com"
            />
          )}
        </Field>

        <Field
          label="Contraseña"
          error={errors.password}
          hint="Al menos 8 caracteres, con una letra y un número."
        >
          {(props) => (
            <input
              {...props}
              type="password"
              className="field-input"
              autoComplete="new-password"
              value={password}
              onChange={(event) => {
                setPassword(event.target.value)
                if (errors.password) setErrors((p) => ({ ...p, password: undefined }))
              }}
            />
          )}
        </Field>

        {/* Live confirmation while typing, so somebody learns the password is
            acceptable before they commit to it — the check runs on every keystroke
            but only ever says "cumple", never scolds mid-word. */}
        {password && !errors.password && (
          <p className="-mt-2 flex items-center gap-1.5 text-[0.75rem] text-credit">
            {passwordIssue ? (
              <span className="text-ink-faint">{passwordIssue}</span>
            ) : (
              <>
                <svg viewBox="0 0 16 16" className="size-3" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M3.5 8.5l3 3 6-7" />
                </svg>
                Cumple los requisitos
              </>
            )}
          </p>
        )}

        <fieldset>
          <legend className="field-label">Tu primera cuenta</legend>
          <div className="grid gap-2 sm:grid-cols-3">
            {ACCOUNT_TYPES.map((type) => (
              <label
                key={type.value}
                className={`cursor-pointer rounded-[5px] border px-3 py-2.5 transition-colors ${
                  accountType === type.value
                    ? 'border-copper bg-copper/8'
                    : 'border-rule hover:border-ink-faint'
                }`}
              >
                <input
                  type="radio"
                  name="account_type"
                  value={type.value}
                  checked={accountType === type.value}
                  onChange={() => setAccountType(type.value)}
                  className="sr-only"
                />
                <span className="block text-[0.875rem] font-medium">{type.label}</span>
                <span className="block text-[0.6875rem] text-ink-faint">{type.detail}</span>
              </label>
            ))}
          </div>
          {errors.account_type && <p className="field-error">{errors.account_type}</p>}
        </fieldset>

        <button type="submit" className="btn btn-primary w-full" disabled={submitting}>
          {submitting && <Spinner />}
          {submitting ? 'Abriendo tu cuenta' : 'Abrir mi cuenta'}
        </button>
      </form>
    </AuthLayout>
  )
}
