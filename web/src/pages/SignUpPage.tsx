import { CircleAlertIcon } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
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
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>No se pudo abrir la cuenta</AlertTitle>
            <AlertDescription>
              {failure.message}
              {failure.status >= 500 && failure.requestId && (
                <span className="type-figure mt-1 block text-[0.6875rem] opacity-70">
                  ref {failure.requestId}
                </span>
              )}
            </AlertDescription>
          </Alert>
        )}

        <Field data-invalid={errors.full_name ? true : undefined}>
          <FieldLabel htmlFor="nombre">Nombre completo</FieldLabel>
          <Input
            id="nombre"
            autoComplete="name"
            autoFocus
            value={fullName}
            aria-invalid={errors.full_name ? true : undefined}
            onChange={(event) => {
              setFullName(event.target.value)
              if (errors.full_name) setErrors((p) => ({ ...p, full_name: undefined }))
            }}
            placeholder="Ana Pérez"
          />
          {errors.full_name && <FieldError>{errors.full_name}</FieldError>}
        </Field>

        <Field data-invalid={errors.email ? true : undefined}>
          <FieldLabel htmlFor="correo">Correo</FieldLabel>
          <Input
            id="correo"
            type="email"
            autoComplete="username"
            value={email}
            aria-invalid={errors.email ? true : undefined}
            onChange={(event) => {
              setEmail(event.target.value)
              if (errors.email) setErrors((p) => ({ ...p, email: undefined }))
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
            autoComplete="new-password"
            value={password}
            aria-invalid={errors.password ? true : undefined}
            onChange={(event) => {
              setPassword(event.target.value)
              if (errors.password) setErrors((p) => ({ ...p, password: undefined }))
            }}
          />
          {errors.password ? (
            <FieldError>{errors.password}</FieldError>
          ) : (
            <FieldDescription>
              Al menos 8 caracteres, con una letra y un número.
            </FieldDescription>
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
                <svg
                  viewBox="0 0 16 16"
                  className="size-3"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                >
                  <path d="M3.5 8.5l3 3 6-7" />
                </svg>
                Cumple los requisitos
              </>
            )}
          </p>
        )}

        <FieldSet data-invalid={errors.account_type ? true : undefined}>
          <FieldLegend variant="label">Tu primera cuenta</FieldLegend>
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
          {errors.account_type && <FieldError>{errors.account_type}</FieldError>}
        </FieldSet>

        <Button
          type="submit"
          disabled={submitting}
          className="w-full bg-copper text-primary-foreground hover:bg-copper/90"
        >
          {submitting && <Spinner />}
          {submitting ? 'Abriendo tu cuenta' : 'Abrir mi cuenta'}
        </Button>
      </form>
    </AuthLayout>
  )
}
