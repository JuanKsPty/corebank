import type { Account, Amount, EntryKind } from '@/api/types'

/**
 * Rendering money and dates.
 *
 * Every amount is formatted from integer cents. There is no arithmetic on a
 * decimal string anywhere in the app, and no float ever holds a monetary value —
 * `Intl.NumberFormat` is handed cents/100 only at the last moment, for display.
 */

const currency = new Intl.NumberFormat('es-PA', {
  style: 'currency',
  currency: 'USD',
  // Without this the es-PA locale writes the ISO code — "USD 85,047.62" — which is
  // not how anybody writes a balance, and it steals width from the one number the
  // page exists to show.
  currencyDisplay: 'narrowSymbol',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

/** Formats an amount as $1,234.56. */
export function money(amount: Amount | number): string {
  const cents = typeof amount === 'number' ? amount : amount.cents
  return currency.format(cents / 100)
}

/** Formats a decimal string the API gave as text, e.g. the chat's "100.00". */
export function moneyFromText(text: string): string {
  const parsed = Number.parseFloat(text)
  return Number.isFinite(parsed) ? currency.format(parsed) : text
}

/** Splits an amount so the cents can be set smaller than the dollars. */
export function moneyParts(amount: Amount | number): { whole: string; cents: string } {
  const formatted = money(amount)
  const separator = formatted.lastIndexOf('.')
  if (separator === -1) return { whole: formatted, cents: '' }
  return { whole: formatted.slice(0, separator), cents: formatted.slice(separator) }
}

const dateFormat = new Intl.DateTimeFormat('es', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
})
const dayMonthFormat = new Intl.DateTimeFormat('es', { day: '2-digit', month: 'short' })
const monthNameFormat = new Intl.DateTimeFormat('es', { month: 'long' })

export function formatDate(iso: string): string {
  return dateFormat.format(new Date(iso))
}

/**
 * "08 sept 2026" for a civil date ("2026-09-08") that has no time and no zone.
 *
 * `new Date("2026-09-08")` is UTC midnight, which a Panamanian browser renders as
 * the 7th; building the date from its parts in local time keeps the day the
 * source printed.
 */
export function formatCivilDate(ymd: string): string {
  const [year = 0, month = 1, day = 1] = ymd.split('-').map(Number)
  return dateFormat.format(new Date(year, month - 1, day))
}

export function formatDayMonth(iso: string): string {
  return dayMonthFormat.format(new Date(iso))
}

/**
 * "02" — the day alone, for a statement row under a month heading.
 *
 * A row inside a group headed "Agosto 2026" does not need to repeat the month, and
 * this is what lets the date column be narrow enough to actually fit. Deliberately
 * not `Intl` with a numeric month: `es-PA` returns "09/25/24" for
 * `{day:'2-digit',month:'2-digit',year:'2-digit'}` — month first, US order — which
 * would silently misdate every movement for a Panamanian reader.
 */
export function formatDayOfMonth(iso: string): string {
  return String(new Date(iso).getDate()).padStart(2, '0')
}

/** A stable key for grouping movements by the month they occurred in. */
export function monthKey(iso: string): string {
  const date = new Date(iso)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
}

/**
 * "Agosto 2026" — the heading over a month's movements.
 *
 * Assembled from the month name and the year rather than asking `Intl` for both,
 * because the Spanish long form is "agosto de 2026" and a heading is not a
 * sentence.
 */
export function formatMonth(iso: string): string {
  const date = new Date(iso)
  const month = monthNameFormat.format(date)
  return `${month.charAt(0).toUpperCase()}${month.slice(1)} ${date.getFullYear()}`
}

const ACCOUNT_TYPES: Record<string, string> = {
  checking: 'Cuenta corriente',
  savings: 'Cuenta de ahorros',
  credit_card: 'Tarjeta de crédito',
  brokerage: 'Inversiones',
}

export function accountTypeLabel(type: string): string {
  return ACCOUNT_TYPES[type] ?? type
}

const INSTITUTIONS: Record<string, string> = {
  banco_general: 'Banco General',
  bac: 'BAC',
  ibkr: 'Interactive Brokers',
}

export function institutionLabel(institution: string): string {
  return INSTITUTIONS[institution] ?? institution
}

/**
 * What to call an account on screen: the name its owner gave it, or the name its
 * bank gives it.
 *
 * The fallback lives here and only here, so no screen can show two accounts that
 * read identically because it forgot to fall back. The number is not folded in:
 * callers show it separately, because an alias is how a person recognises an
 * account and the number is how the bank identifies it.
 */
export function accountLabel(
  account: Pick<Account, 'alias' | 'display_name' | 'type'>,
): string {
  return account.alias?.trim() || account.display_name || accountTypeLabel(account.type)
}

/** The last four characters of an account number, as a person tells accounts apart. */
export function numberShort(number: string): string {
  const digits = number.replace(/[^0-9]/g, '')
  return digits.length >= 4 ? `···${digits.slice(-4)}` : number
}

const ENTRY_KINDS: Record<EntryKind, string> = {
  income: 'Ingreso',
  expense: 'Gasto',
  refund: 'Reembolso',
  fee: 'Comisión',
  interest: 'Intereses',
  transfer: 'Transferencia entre tus cuentas',
  trade: 'Compraventa de valores',
}

export function entryKindLabel(kind: EntryKind): string {
  return ENTRY_KINDS[kind] ?? kind
}

/** "01 jun" for a civil date, built from its parts so no zone can shift it. */
export function formatCivilDayMonth(ymd: string): string {
  const [year = 0, month = 1, day = 1] = ymd.split('-').map(Number)
  return dayMonthFormat.format(new Date(year, month - 1, day))
}

/** "Junio 2026" for a civil date's month. */
export function formatCivilMonth(ymd: string): string {
  const [year = 0, month = 1] = ymd.split('-').map(Number)
  const name = monthNameFormat.format(new Date(year, month - 1, 1))
  return `${name.charAt(0).toUpperCase()}${name.slice(1)} ${year}`
}

/** Today in the browser's own calendar, as YYYY-MM-DD. */
export function todayCivil(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}
