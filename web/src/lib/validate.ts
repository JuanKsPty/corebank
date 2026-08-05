/**
 * Form validation.
 *
 * Written by hand rather than with a schema library, for the same reason the API's
 * validation is: the messages are the point. Each one says what to do about the
 * problem, in the interface's own voice, which a generated "String must contain at
 * least 8 character(s)" never does.
 *
 * The server validates everything again and is the authority. What happens here is
 * so the customer finds out before pressing the button.
 */

export type Errors<T extends string> = Partial<Record<T, string>>

export function emailProblem(value: string): string | undefined {
  const email = value.trim()
  if (!email) return 'El correo es obligatorio.'
  if (email.length > 254) return 'El correo es demasiado largo.'

  // A shape check, not a validity check: nothing short of sending a message
  // proves an address works, so this catches only the obvious mistakes.
  const at = email.lastIndexOf('@')
  if (at <= 0 || at === email.length - 1) return 'Falta la parte del @ en el correo.'
  const domain = email.slice(at + 1)
  if (!domain.includes('.') || domain.startsWith('.') || domain.endsWith('.')) {
    return 'El dominio del correo no parece válido.'
  }
  if (/[\s,;"'\\]/.test(email)) return 'El correo no puede contener espacios ni comas.'
  return undefined
}

/** Mirrors the server's policy: 8 characters, at most 72 bytes, a letter and a digit. */
export function passwordProblem(value: string): string | undefined {
  if (!value) return 'La contraseña es obligatoria.'
  if ([...value].length < 8) return 'Usa al menos 8 caracteres.'
  if (new TextEncoder().encode(value).length > 72) {
    return 'La contraseña es demasiado larga (máximo 72 bytes).'
  }
  if (!/\p{L}/u.test(value) || !/\p{Nd}/u.test(value)) {
    return 'Incluye al menos una letra y un número.'
  }
  return undefined
}

export function fullNameProblem(value: string): string | undefined {
  const name = value.trim()
  if (!name) return 'El nombre es obligatorio.'
  if (name.length > 120) return 'El nombre es demasiado largo.'
  return undefined
}

/**
 * amountProblem validates a typed amount without ever parsing it to a float.
 *
 * The text is what gets sent, so the check has to be on the text: a value with
 * three decimals must be refused rather than silently rounded, because a rounded
 * amount in a ledger is a bug.
 */
export function amountProblem(value: string, availableCents?: number): string | undefined {
  const raw = value.trim()
  if (!raw) return 'Indica un monto.'
  if (!/^\d+(\.\d{1,2})?$/.test(raw)) {
    if (/,/.test(raw))
      return 'Escribe el monto sin separador de miles, por ejemplo 1500.50.'
    if (/^\d+\.\d{3,}$/.test(raw)) return 'El monto admite como máximo dos decimales.'
    return 'Escribe solo dígitos y un punto decimal, por ejemplo 150.50.'
  }

  const cents = amountToCents(raw)
  if (cents === null) return 'El monto está fuera del rango admitido.'
  if (cents === 0) return 'El monto debe ser mayor que cero.'
  if (availableCents !== undefined && cents > availableCents) {
    return 'El monto supera el saldo disponible de esta cuenta.'
  }
  return undefined
}

/**
 * amountToCents converts validated amount text to integer cents.
 *
 * Digit arithmetic, not `parseFloat(x) * 100`: the naive version turns 8.87 into
 * 886 cents. This is the same reasoning the Go side applies, and the same test
 * value proves it.
 */
export function amountToCents(value: string): number | null {
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(value.trim())
  if (!match) return null

  const whole = Number.parseInt(match[1] ?? '0', 10)
  const fraction = (match[2] ?? '').padEnd(2, '0')
  const cents = whole * 100 + Number.parseInt(fraction || '0', 10)

  return Number.isSafeInteger(cents) ? cents : null
}

/** Account numbers are 16 digits, written or pasted with or without separators. */
export function accountNumberProblem(value: string, required = true): string | undefined {
  const raw = value.trim()
  if (!raw) return required ? 'Indica la cuenta de destino.' : undefined

  const digits = raw.replace(/[-\s]/g, '')
  if (!/^\d{16}$/.test(digits)) {
    return 'Un número de cuenta tiene 16 dígitos, por ejemplo 4001-6588-5247-0001.'
  }
  return undefined
}

/** Normalises a pasted account number into the grouped form the API uses. */
export function normaliseAccountNumber(value: string): string {
  const digits = value.replace(/[-\s]/g, '')
  if (digits.length !== 16) return value.trim()
  return digits.replace(/(\d{4})(?=\d)/g, '$1-')
}

/** True when no field has a problem. */
/**
 * The longest alias an account may carry.
 *
 * Forty, matching the server's limit and the column's CHECK constraint. It is enforced
 * here as a `maxLength` on the input so the limit is felt while typing rather than
 * discovered on submit — the server still refuses anything longer, because a limit only
 * the browser knows is not a limit.
 *
 * Counted the way both the server and PostgreSQL count it: in characters, not bytes, so
 * "Ahorros de mamá" costs fifteen and not sixteen.
 */
export const MAX_ALIAS = 40

export function isClean<T extends string>(errors: Errors<T>): boolean {
  return Object.values(errors).every((message) => !message)
}
