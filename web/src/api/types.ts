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

export type AccountClass = 'asset' | 'liability'
export type AccountType = 'checking' | 'savings' | 'credit_card' | 'brokerage'
export type Institution = 'banco_general' | 'bac' | 'ibkr'

/**
 * A real-world account: a bank account, a card or a brokerage account, keyed the
 * way its institution keys it.
 */
export interface Account {
  id: string
  class: AccountClass
  type: AccountType
  institution: Institution
  /** The number as the bank prints it, e.g. "04-98-97-958835-1" or "**** 1111". */
  external_number: string
  display_name: string
  /** What the owner calls it, when they have named it. Read through `accountLabel`. */
  alias?: string
  currency: string
  /**
   * Signed from the owner's point of view: a card that owes $14.30 has -14.30.
   * Only meaningful when `anchored`.
   */
  balance: Amount
  /** A card's balance as the bank prints it: a positive amount owed. */
  owed?: Amount
  /**
   * Whether a stated balance — a statement's opening, IBKR's cash report, or one
   * the owner entered — fixes where the account started. Without one the balance
   * is only the sum of the imported movements.
   */
  anchored: boolean
  /** A brokerage account's positions, at market value. */
  holdings?: Amount
  /** The latest stated balance against the computed one. */
  drift?: Drift
  movements: number
  last_day: string | null
}

/** How far a stated balance is from what the movements add up to. */
export interface Drift {
  as_of: string
  stated: Amount
  computed: Amount
  /** stated − computed. Zero means the account matches the bank. */
  difference: Amount
  source: 'statement_opening' | 'statement_closing' | 'broker' | 'manual'
}

/** Assets minus debts plus investments. */
export interface NetWorth {
  assets: Amount
  owed: Amount
  holdings: Amount
  total: Amount
  /** True while some account with movements has no starting balance. */
  incomplete: boolean
  unanchored: number
}

export interface AccountList {
  accounts: Account[]
  net_worth: NetWorth
}

/** A balance somebody stated. */
export interface Checkpoint {
  id: string
  as_of: string
  balance: Amount
  source: Drift['source']
  /** The anchor the opening balance is derived from. */
  pinned: boolean
  due_on?: string
  minimum_payment?: Amount
  note?: string
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
  net_worth: NetWorth
}

/** Whether this browser can skip straight to a PIN, and whose it is. */
export interface DeviceStatus {
  trusted: boolean
  full_name?: string
}

/** What the Seguridad screen renders its PIN section from. */
export interface SecurityStatus {
  has_pin: boolean
  device_enabled: boolean
}

/**
 * What a movement counts as. A transfer between the owner's own accounts and a
 * trade never count as spending or income.
 */
export type EntryKind =
  'income' | 'expense' | 'refund' | 'fee' | 'interest' | 'transfer' | 'trade'

/** One line a statement or IBKR printed. */
export interface Entry {
  id: string
  account_id: string
  /** The day the bank printed (YYYY-MM-DD), with no time zone to shift it. */
  booked_on: string
  posted_on?: string
  booked_at?: string
  /** Signed from the owner's point of view: negative is money leaving. */
  amount: Amount
  kind: EntryKind
  /** What the importer decided, when the owner or a rule overrode it. */
  imported_kind: EntryKind
  description: string
  bank_ref?: string
  bank_category?: string
  /** The balance the bank printed after this line. */
  bank_balance?: Amount
  category_id?: string
  category_source?: 'rule' | 'user' | 'assistant'
  note?: string
}

export interface EntryPage {
  entries: Entry[]
  /** Opaque; pass it back verbatim to get the next page. */
  next_cursor?: string
  has_more: boolean
}

/**
 * Why the assistant is not answering, when it is not.
 *
 * `is_ai` alone cannot tell these apart, and they are not the same message. Running
 * without a key is a configuration choice; running out of budget means the money
 * for the model is gone. One of those is worth explaining.
 */
export type ChatEngine = 'ai' | 'unconfigured' | 'budget_exhausted' | 'degraded'

export interface ChatProvider {
  name: string
  /** False when the assistant is unavailable, which the interface says plainly. */
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

/** A brokerage account's IBKR link. Never includes the token. */
export interface InvestmentLinkStatus {
  ibkr_account_id: string
  last_synced_at?: string
  last_sync_status: 'never' | 'ok' | 'error'
  last_sync_error?: string
}

/** What one sync saw and did. */
export interface InvestmentSyncResult {
  run_id: string
  cash_new: number
  cash_duplicate: number
  /** Rows in a currency other than USD, left out and counted. */
  cash_failed: number
  trades_new: number
  trades_duplicate: number
  positions: number
  /** The period IBKR's report covers (YYYY-MM-DD). */
  period_from: string | null
  period_to: string | null
  generated_at?: string
  /** IBKR's own ending cash, which the computed cash must agree with. */
  reported_cash?: Amount
  missing_sections: string[]
  /** Spanish, ready to show. */
  warnings: string[]
}

/** One holding, as of the last sync. */
export interface InvestmentPosition {
  symbol: string
  asset_class: string
  quantity: number
  mark_price?: Amount
  market_value?: Amount
  cost_basis?: Amount
  as_of: string
}

export interface Portfolio {
  holdings: Amount
  positions: InvestmentPosition[]
}

export interface InvestmentTrade {
  id: string
  symbol: string
  asset_class: string
  side: 'buy' | 'sell'
  quantity: number
  price: Amount
  commission: Amount
  net_cash: Amount
  trade_date: string
}

// --- imports -------------------------------------------------------------------

/** What one statement in an uploaded file did to its account. */
export interface ImportAccountResult {
  account_id: string
  display_name: string
  /** The file introduced this account. */
  created: boolean
  period_start: string
  period_end: string
  lines: number
  new: number
  duplicates: number
  opening?: Amount
  closing?: Amount
  chain_break?: { line_no: number; bank: Amount; computed: Amount }
  warnings: string[]
  /** A card whose file prints no balance, with no starting balance yet. */
  needs_opening: boolean
}

export interface ImportResult {
  source: 'bg_account' | 'bac_account' | 'bg_card'
  /** The exact same file was already imported; nothing changed. */
  unchanged: boolean
  accounts: ImportAccountResult[]
}

/** One import or IBKR sync, as recorded. */
export interface ImportRun {
  id: string
  source: 'bg_account' | 'bac_account' | 'bg_card' | 'ibkr_flex'
  status: 'ok' | 'unchanged' | 'error'
  filename?: string
  period_start: string | null
  period_end: string | null
  lines_seen: number
  lines_new: number
  lines_duplicate: number
  lines_failed: number
  details: { warnings?: string[]; missing_sections?: string[] }
  error?: string
  created_at: string
}

// --- categories ------------------------------------------------------------------

export interface Category {
  id: string
  parent_id?: string
  name: string
  kind: 'income' | 'expense'
  color?: string
  icon?: string
  is_system: boolean
}

export interface CategoryNode extends Category {
  children: Category[]
}

/** One server-sent event from the chat stream. */
export type ChatEvent =
  | { kind: 'message'; text: string }
  | { kind: 'tool_call'; tool: string }
  | { kind: 'tool_result'; tool: string; failed?: boolean }
  | { kind: 'error'; code: string; message: string }
  // The closing event carries which engine actually answered. It can differ from
  // what the page was told on load — a spend ceiling reached mid-session, a key
  // that stopped working — and a label that only refreshes with the page would
  // credit this reply to a model that did not write it.
  | { kind: 'done'; provider?: ChatProvider }

/** What a dry run read from one statement, written nowhere. */
export interface StatementReport {
  institution: string
  external_number: string
  display_name: string
  class: 'asset' | 'liability'
  type: string
  currency: string
  /** Civil dates (YYYY-MM-DD). */
  period_start: string | null
  period_end: string | null
  /** The bank's own balances; absent when the format prints none (a card). */
  opening?: Amount
  closing?: Amount
  available?: Amount
  held?: Amount
  line_count: number
  money_in: Amount
  money_out: Amount
  /** The first line whose printed balance disagrees with the computed one. */
  chain_break?: { line_no: number; bank: Amount; computed: Amount }
  warnings: string[]
}

export interface StatementFileReport {
  source: 'bg_account' | 'bac_account' | 'bg_card'
  statements: StatementReport[]
}

// --- reconciliation ------------------------------------------------------------

export interface ReconciliationCheck {
  checkpoint: Checkpoint
  computed: Amount
  /** stated − computed. */
  difference: Amount
}

export interface StatementCheck {
  id: string
  period_start: string
  period_end: string
  line_count: number
  opening?: ReconciliationCheck
  closing?: ReconciliationCheck
  /** This statement starts more than a day after the previous one ends. */
  gap: boolean
  warnings: string[]
}

export interface ReconciliationLine {
  id: string
  booked_on: string
  description: string
  amount: Amount
  /** The running balance the movements add up to. */
  computed: Amount
  /** The running balance the bank printed, when it prints one. */
  bank?: Amount
  /** bank − computed. */
  difference?: Amount
}

/** Where an account stops matching its bank, if it does. */
export interface Reconciliation {
  account: Account
  anchor?: Checkpoint
  opening: Amount
  checks: ReconciliationCheck[]
  statements: StatementCheck[]
  lines: ReconciliationLine[]
  /** The line where the difference with the bank starts. */
  first_break?: string
}

// --- reports, transfers, rules ---------------------------------------------------

export interface CategoryTotal {
  /** Null for movements with no category. */
  category_id: string | null
  /** Positive: spending as money spent, a refund reducing it. */
  total: Amount
  count: number
}

export interface FlowPoint {
  /** The day, or the first of the month for a monthly series. */
  day: string
  in: Amount
  out: Amount
}

/** Two movements that look like one transfer between the owner's accounts. */
export interface TransferSuggestion {
  out: Entry
  in: Entry
}

export interface CategoryRule {
  id: string
  match_text: string
  category_id?: string
  /** Matching movements count as transfers between the owner's accounts. */
  transfer: boolean
  priority: number
  applied?: number
}
