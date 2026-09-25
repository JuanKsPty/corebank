import type { Amount, MovementKind, MovementStatus } from '@/api/types'

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

/** Seconds remaining until an ISO instant, floored at zero. */
export function secondsUntil(iso: string): number {
  return Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000))
}

/** "1:47" — a countdown for the confirmation card. */
export function formatCountdown(seconds: number): string {
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`
}

const ACCOUNT_TYPES: Record<string, string> = {
  savings: 'Ahorros',
  checking: 'Corriente',
  investment: 'Inversión',
}

export function accountTypeLabel(type: string): string {
  return ACCOUNT_TYPES[type] ?? type
}

/**
 * What to call an account on screen: the name its owner gave it, or its type.
 *
 * The fallback lives here and only here. Six screens label accounts, and if each one
 * worked out `alias || tipo` for itself, one of them would eventually be added without
 * it — and the screen that forgot would be the one showing two accounts that read
 * identically, which is the whole problem this exists to solve.
 *
 * The number is not folded in on purpose. Every caller shows it separately, because an
 * alias is how a person recognises an account and the number is how the bank identifies
 * one; aliases are not unique, so the number has to stay visible next to the name
 * rather than be replaced by it.
 */
export function accountLabel(account: { alias?: string; account_type: string }): string {
  return account.alias?.trim() || accountTypeLabel(account.account_type)
}

/**
 * The account's type, but only when the label above it is showing something else.
 *
 * Naming an account must not hide what kind of account it is: "Gastos del mes" does not
 * say whether it is a current account or an investment one, and that matters. So when an
 * alias takes the headline the type moves down beside the number, and when there is no
 * alias this returns nothing, because the headline is already the type and printing it
 * twice would be noise.
 */
export function accountTypeAside(account: {
  alias?: string
  account_type: string
}): string | null {
  return account.alias?.trim() ? accountTypeLabel(account.account_type) : null
}

const MOVEMENT_KINDS: Record<MovementKind, string> = {
  deposit: 'Depósito',
  withdrawal: 'Retiro',
  transfer: 'Transferencia',
  internal_transfer: 'Traspaso',
}

export function movementLabel(kind: MovementKind): string {
  return MOVEMENT_KINDS[kind] ?? kind
}

const STATUSES: Record<MovementStatus, string> = {
  pending: 'Por confirmar',
  completed: 'Completado',
  failed: 'Rechazado',
  voided: 'Cancelado',
  expired: 'Expirado',
}

export function statusLabel(status: MovementStatus): string {
  return STATUSES[status] ?? status
}

/** The sentinel the API uses for a counterparty outside the bank. */
export const EXTERNAL = 'EXTERNAL'

export function counterpartyLabel(accountNumber: string | undefined): string {
  if (!accountNumber) return '—'
  return accountNumber === EXTERNAL ? 'Externo' : accountNumber
}

/**
 * "···1300" — a counterparty reduced to what actually identifies it to a person.
 *
 * An account number here is always 19 characters: 16 digits in four dash-separated
 * groups. Spline Sans Mono advances 1200 units on a 2000-unit em for every glyph it
 * has, so at the 12px the statement uses those 19 characters need 19 × 7.2 ≈ 134px
 * — and the column they were being drawn in had a 104px content box. With
 * `white-space: nowrap` and nothing clipping them, the last 30px painted straight
 * over the rule dividing the debit and credit sides. That was the bug.
 *
 * Widening the column is not the fix: it would have to grow by a third to hold a
 * string whose leading twelve digits are the same on every row in the dataset. The
 * last four are what a person reads to tell two of their accounts apart, they are
 * what the account cards already show, and they need 7 glyphs ≈ 50px. The full
 * number stays one hover or one tap away.
 */
export function counterpartyShort(accountNumber: string | undefined): string {
  if (!accountNumber) return '—'
  if (accountNumber === EXTERNAL) return 'Externo'
  return `···${accountNumber.slice(-4)}`
}

/**
 * Which side of the account a movement falls on, from the customer's point of
 * view. A transfer between two of their own accounts is both, which is why the
 * decision needs the set of accounts they hold rather than just the movement.
 */
export type Side = 'debit' | 'credit'

export function sideOf(
  movement: { from_account?: string; to_account?: string },
  ownedAccounts: ReadonlySet<string>,
): Side {
  if (movement.from_account && ownedAccounts.has(movement.from_account)) return 'debit'
  return 'credit'
}
