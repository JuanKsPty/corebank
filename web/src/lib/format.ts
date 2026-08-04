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

const dateFormat = new Intl.DateTimeFormat('es', { day: '2-digit', month: 'short', year: 'numeric' })
const dayMonthFormat = new Intl.DateTimeFormat('es', { day: '2-digit', month: 'short' })

export function formatDate(iso: string): string {
  return dateFormat.format(new Date(iso))
}

export function formatDayMonth(iso: string): string {
  return dayMonthFormat.format(new Date(iso))
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
