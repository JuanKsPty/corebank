/**
 * The API's wire shapes, written by hand to match the Go DTOs.
 *
 * Hand-written rather than generated from a spec: with this many endpoints a
 * schema plus codegen would cost more to keep in step than the types do, and
 * these carry comments explaining what a field means — which generated types
 * never do.
 */

/** An amount, always integer cents plus the same value as text. */
export interface Amount {
  /** The authoritative value. Compute with this; never with `formatted`. */
  cents: number
  /** The same amount as a decimal string, e.g. "32354.53". */
  formatted: string
  currency: string
}

export type AccountType = 'savings' | 'checking' | 'investment'

export interface Account {
  id: string
  account_number: string
  account_type: AccountType
  /**
   * What the customer calls this account, or empty if they have not named it.
   * Read it through `accountLabel`, which falls back to the type in one place.
   */
  alias: string
  /** What can be spent: posted minus anything held by an unconfirmed movement. */
  available: Amount
  /** The settled balance, before reservations are deducted. */
  posted: Amount
  /** Funds reserved by a movement awaiting confirmation. */
  held: Amount
  currency: string
  created_at: string
}

export interface User {
  id: string
  email: string
  full_name: string
  created_at: string
}

export interface Session {
  access_token: string
  token_type: string
  /** When the access token expires, so the client refreshes before it fails. */
  expires_at: string
  user: User
}

export interface Me {
  user: User
  accounts: Account[]
  total_available: Amount
}

export type MovementKind = 'deposit' | 'withdrawal' | 'transfer' | 'internal_transfer'

export type MovementStatus = 'pending' | 'completed' | 'failed' | 'voided' | 'expired'

/** Present only while a movement holds funds and waits for an answer. */
export interface Confirmation {
  hold_id: string
  expires_at: string
}

export interface Transaction {
  id: string
  kind: MovementKind
  status: MovementStatus
  amount: Amount
  /** "EXTERNAL" means the counterparty is outside this bank. */
  from_account?: string
  to_account?: string
  description: string
  /** Whether the customer did this themselves or the assistant did it for them. */
  origin: 'api' | 'chat'
  failure_code?: string
  confirmation?: Confirmation
  occurred_at: string
}

export interface TransactionPage {
  transactions: Transaction[]
  /** Opaque; pass it back verbatim to get the next page. */
  next_cursor?: string
  has_more: boolean
}

export interface FlowPoint {
  day: string
  in: Amount
  out: Amount
}

/** The daily series together with the period it actually covers. */
export interface Flow {
  points: FlowPoint[]
  from: string
  to: string
  /**
   * False when the window had to move back to the customer's most recent activity —
   * which is what the imported dataset produces, since its history ends in 2024.
   * The chart labels the period rather than claiming "last 30 days".
   */
  recent: boolean
}

export interface Dashboard {
  accounts: Account[]
  total_available: Amount
  recent: Transaction[]
  flow: Flow
  /** Reservations still waiting, so a reload restores their cards. */
  pending_confirmations: Transaction[]
}

/** The card the interface renders when the assistant proposes a movement. */
export interface ConfirmationCard {
  hold_id: string
  kind: MovementKind
  /** Decimal string, e.g. "100.00". */
  amount: string
  from_account: string
  to_account: string
  balance_if_confirmed?: string
  expires_at: string
}

/**
 * Why a rule-based reply is rule-based.
 *
 * `is_ai` alone cannot tell these apart, and they are not the same message. Running
 * without a key is how the project works on a machine with no credentials; running
 * out of budget means the demo's money is gone. One of those is worth explaining.
 */
export type ChatEngine = 'ai' | 'unconfigured' | 'budget_exhausted' | 'degraded'

export interface ChatProvider {
  name: string
  /** False for the rule-based fallback, which the interface labels as such. */
  is_ai: boolean
  engine: ChatEngine
}

export interface ChatMessage {
  id: number
  role: 'user' | 'assistant' | 'tool'
  text?: string
  /** Names of the tools this turn ran, for the trace shown under the message. */
  tools?: string[]
  created_at: string
}

export interface ChatHistory {
  messages: ChatMessage[]
  provider: ChatProvider
}

/** The single error shape every endpoint returns. */
export interface ApiErrorBody {
  error: {
    code: string
    message: string
    /** Per-field messages, keyed by the JSON field name. */
    fields?: Record<string, string>
    request_id?: string
  }
}

// --- investments -------------------------------------------------------------

/** An investment account's IBKR link, or its absence. */
export interface InvestmentLinkStatus {
  ibkr_account_id: string
  /** Absent when the account has never synced. */
  last_synced_at?: string
  last_sync_status: 'never' | 'ok' | 'error'
  /** Only present when `last_sync_status` is `'error'`. */
  last_sync_error?: string
}

/** What one sync did — a healthy no-op run looks the same shape as one that moved money. */
export interface InvestmentSyncResult {
  cash_movements_posted: number
  cash_movements_skipped: number
  trades_recorded: number
  trades_skipped: number
  positions: number
}

/** One holding, as of the account's last sync — not a live quote. */
export interface InvestmentPosition {
  symbol: string
  asset_class: string
  quantity: number
  mark_price: Amount
  market_value: Amount
  cost_basis: Amount
  as_of: string
}

/**
 * An investment account's value from both of its sources of truth.
 *
 * `cash` is what the ledger says — the one number here corebank actually verified.
 * `holdings_value` is the sum of the positions' own market values, as of the last
 * sync. They are shown separately for the same reason `BalanceComposition` never
 * blends settled and held: a figure this app cannot verify must never be folded
 * into one it can.
 */
export interface Portfolio {
  cash: Amount
  holdings_value: Amount
  total_value: Amount
  positions: InvestmentPosition[]
}

export interface InvestmentTrade {
  symbol: string
  asset_class: string
  side: 'buy' | 'sell'
  quantity: number
  price: Amount
  commission: Amount
  net_cash: Amount
  trade_date: string
}

/** One server-sent event from the chat stream. */
export type ChatEvent =
  | { kind: 'message'; text: string }
  | { kind: 'tool_call'; tool: string }
  | { kind: 'tool_result'; tool: string; failed?: boolean }
  | { kind: 'confirmation'; confirmation: ConfirmationCard }
  | { kind: 'error'; code: string; message: string }
  // The closing event carries which engine actually answered. It can differ from
  // what the page was told on load — a spend ceiling reached mid-session, a key
  // that stopped working — and a label that only refreshes with the page would
  // credit this reply to a model that did not write it.
  | { kind: 'done'; provider?: ChatProvider }
