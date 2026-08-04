import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { ApiError, streamChat } from '@/api/client'
import type { ChatEvent, ConfirmationCard as Card, Transaction } from '@/api/types'
import { keys, useChatHistory, useClearChat } from '@/lib/queries'
import { ConfirmationCard } from './ConfirmationCard'
import { Badge, EmptyState, Notice, Spinner, cx } from './primitives'

/**
 * The assistant.
 *
 * A conversation is rendered as a list of turns, and a turn can carry three kinds
 * of thing: what the assistant said, which tools it ran, and — when it proposed a
 * movement — the confirmation card. The card is placed in the transcript rather
 * than in a modal on purpose: it belongs to the exchange that produced it, and a
 * dialog would make it feel like something the assistant did *to* the customer
 * instead of something it is asking them about.
 *
 * The tool trace is shown, not hidden. When software can move your money, seeing
 * exactly which operations it performed is not developer detail — it is the audit
 * trail, and it is the reason to trust the answer above it.
 */

interface Turn {
  id: string
  role: 'user' | 'assistant'
  text?: string
  tools?: ToolRun[]
  card?: Card
  /** Set on a turn that failed, so the message is styled as a failure. */
  error?: string
}

interface ToolRun {
  name: string
  failed?: boolean
}

const TOOL_LABELS: Record<string, string> = {
  list_accounts: 'Consultó tus cuentas',
  get_balance: 'Consultó un saldo',
  list_transactions: 'Consultó tus movimientos',
  deposit: 'Registró un ingreso',
  prepare_withdrawal: 'Preparó un retiro',
  prepare_transfer: 'Preparó una transferencia',
}

export function ChatPanel({ pendingConfirmations }: { pendingConfirmations?: Transaction[] }) {
  const history = useChatHistory()
  const clear = useClearChat()
  const queryClient = useQueryClient()

  const [turns, setTurns] = useState<Turn[]>([])
  const [draft, setDraft] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [activeTool, setActiveTool] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)

  const scrollRef = useRef<HTMLDivElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const hydratedRef = useRef(false)

  // The stored conversation is loaded once, into the same list the live stream
  // appends to. Loading it on every render would fight the stream for the same
  // state.
  useEffect(() => {
    if (hydratedRef.current || !history.data) return
    hydratedRef.current = true

    setTurns(
      history.data.messages.map((message) => ({
        id: `stored-${message.id}`,
        role: message.role === 'user' ? 'user' : 'assistant',
        text: message.text,
        tools: message.tools?.map((name) => ({ name })),
      })),
    )
  }, [history.data])

  // A reservation the customer never answered has to reappear after a reload, or
  // the held funds are missing from the available balance with nothing on screen
  // explaining why.
  useEffect(() => {
    if (!pendingConfirmations?.length) return

    setTurns((previous) => {
      const known = new Set(
        previous.map((turn) => turn.card?.hold_id).filter((id): id is string => Boolean(id)),
      )
      const restored = pendingConfirmations
        .filter((tx) => tx.confirmation && !known.has(tx.confirmation.hold_id))
        .map<Turn>((tx) => ({
          id: `pending-${tx.confirmation!.hold_id}`,
          role: 'assistant',
          text: 'Tenías esta operación pendiente de confirmar.',
          card: {
            hold_id: tx.confirmation!.hold_id,
            kind: tx.kind,
            amount: tx.amount.formatted,
            from_account: tx.from_account ?? '',
            to_account: tx.to_account ?? '',
            expires_at: tx.confirmation!.expires_at,
          },
        }))

      return restored.length ? [...previous, ...restored] : previous
    })
  }, [pendingConfirmations])

  const scrollToEnd = useCallback(() => {
    const node = scrollRef.current
    if (node) node.scrollTop = node.scrollHeight
  }, [])

  useEffect(scrollToEnd, [turns, streaming, scrollToEnd])

  // A stream left running after the panel unmounts would keep writing into state
  // that no longer exists.
  useEffect(() => () => abortRef.current?.abort(), [])

  async function send(event: React.FormEvent) {
    event.preventDefault()

    const message = draft.trim()
    if (!message || streaming) return

    setDraft('')
    setFailure(null)
    setStreaming(true)

    const turnId = crypto.randomUUID()
    setTurns((previous) => [
      ...previous,
      { id: `${turnId}-user`, role: 'user', text: message },
      { id: turnId, role: 'assistant' },
    ])

    const controller = new AbortController()
    abortRef.current = controller

    const update = (change: (turn: Turn) => Turn) =>
      setTurns((previous) => previous.map((turn) => (turn.id === turnId ? change(turn) : turn)))

    try {
      for await (const event of streamChat(message, controller.signal)) {
        applyEvent(event, update, setActiveTool)
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        setFailure(
          error instanceof ApiError
            ? error.message
            : 'Se perdió la conexión con el asistente. Inténtalo de nuevo.',
        )
      }
    } finally {
      setStreaming(false)
      setActiveTool(null)
      abortRef.current = null
      // The assistant may have moved or reserved money, so everything that shows
      // a balance is now suspect.
      void queryClient.invalidateQueries({ queryKey: keys.dashboard })
      void queryClient.invalidateQueries({ queryKey: keys.me })
      void queryClient.invalidateQueries({ queryKey: ['history'] })
    }
  }

  const provider = history.data?.provider

  return (
    <section className="card flex h-full min-h-[32rem] flex-col overflow-hidden" aria-label="Asistente">
      <header className="flex items-center gap-3 border-b border-rule px-4 py-3">
        <div className="min-w-0 flex-1">
          <h2 className="type-display text-[0.9375rem]">Asistente</h2>
          {provider && (
            <p className="mt-0.5 flex items-center gap-1.5 text-[0.6875rem] text-ink-faint">
              {/* Which engine is answering is stated, not implied. A rule-based
                  fallback presented as an AI would be a lie about the product. */}
              {provider.is_ai ? (
                <>
                  <span aria-hidden="true" className="size-1.5 rounded-full bg-credit" />
                  Modelo {provider.name}
                </>
              ) : (
                <>
                  <span aria-hidden="true" className="size-1.5 rounded-full bg-hold" />
                  Sin IA configurada · {provider.name}
                </>
              )}
            </p>
          )}
        </div>
        {turns.length > 0 && (
          <button
            type="button"
            onClick={() => {
              clear.mutate(undefined, { onSuccess: () => setTurns([]) })
            }}
            disabled={clear.isPending || streaming}
            className="btn btn-secondary px-2.5 py-1 text-[0.75rem]"
          >
            Limpiar
          </button>
        )}
      </header>

      <div ref={scrollRef} className="flex-1 space-y-4 overflow-y-auto px-4 py-4">
        {history.isLoading ? (
          <ChatSkeleton />
        ) : turns.length === 0 ? (
          <Suggestions provider={provider?.is_ai ?? false} onPick={setDraft} />
        ) : (
          turns.map((turn) => <TurnView key={turn.id} turn={turn} />)
        )}

        {streaming && <Thinking tool={activeTool} />}

        {failure && (
          <Notice tone="error" title="El asistente se interrumpió" onDismiss={() => setFailure(null)}>
            {failure}
          </Notice>
        )}
      </div>

      <form onSubmit={send} className="border-t border-rule p-3">
        <div className="flex gap-2">
          <label htmlFor="chat-input" className="sr-only">
            Escribe tu petición
          </label>
          <input
            id="chat-input"
            className="field-input flex-1"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="¿Cuánto dinero tengo?"
            disabled={streaming}
            maxLength={2000}
            autoComplete="off"
          />
          <button
            type="submit"
            className="btn btn-ink shrink-0"
            disabled={streaming || !draft.trim()}
            aria-label="Enviar"
          >
            {streaming ? (
              <Spinner />
            ) : (
              <svg viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.7">
                <path d="M2.5 8h10M9 4.5L12.5 8 9 11.5" />
              </svg>
            )}
          </button>
        </div>
      </form>
    </section>
  )
}

/** applyEvent folds one stream event into the assistant's turn. */
function applyEvent(
  event: ChatEvent,
  update: (change: (turn: Turn) => Turn) => void,
  setActiveTool: (tool: string | null) => void,
) {
  switch (event.kind) {
    case 'message':
      // Turns arrive whole rather than token by token, so successive messages in
      // one exchange are joined rather than replacing each other.
      update((turn) => ({ ...turn, text: turn.text ? `${turn.text}\n\n${event.text}` : event.text }))
      break

    case 'tool_call':
      setActiveTool(event.tool)
      update((turn) => ({ ...turn, tools: [...(turn.tools ?? []), { name: event.tool }] }))
      break

    case 'tool_result':
      setActiveTool(null)
      update((turn) => {
        const tools = [...(turn.tools ?? [])]
        // Mark the most recent run of this tool, not the first: the same tool can
        // be called twice in one exchange.
        for (let i = tools.length - 1; i >= 0; i--) {
          if (tools[i]?.name === event.tool) {
            tools[i] = { name: event.tool, failed: event.failed }
            break
          }
        }
        return { ...turn, tools }
      })
      break

    case 'confirmation':
      update((turn) => ({ ...turn, card: event.confirmation }))
      break

    case 'error':
      update((turn) => ({ ...turn, error: event.message }))
      break

    case 'done':
      setActiveTool(null)
      break
  }
}

function TurnView({ turn }: { turn: Turn }) {
  if (turn.role === 'user') {
    return (
      <div className="flex justify-end">
        <p className="max-w-[85%] rounded-[5px] rounded-br-sm bg-ink px-3 py-2 text-[0.875rem] text-paper">
          {turn.text}
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {turn.tools && turn.tools.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label="Operaciones realizadas">
          {turn.tools.map((tool, index) => (
            <li key={`${tool.name}-${index}`}>
              <Badge tone={tool.failed ? 'danger' : 'neutral'}>
                {tool.failed ? '✕' : '✓'} {TOOL_LABELS[tool.name] ?? tool.name}
              </Badge>
            </li>
          ))}
        </ul>
      )}

      {turn.text && (
        <div className="max-w-[92%] space-y-2 text-[0.875rem] leading-relaxed">
          {renderAssistantText(turn.text)}
        </div>
      )}

      {turn.card && <div className="max-w-[92%] pt-1">
        <ConfirmationCard card={turn.card} />
      </div>}

      {turn.error && (
        <p className="text-[0.8125rem] text-[#8f2d24]" role="alert">
          {turn.error}
        </p>
      )}
    </div>
  )
}

/**
 * renderAssistantText renders the reply's paragraphs, bullets and bold runs.
 *
 * Deliberately a handful of cases rather than a markdown library: the assistant is
 * instructed to write plain prose with the occasional list, and rendering arbitrary
 * markdown from a model into a banking page is a larger surface than it is worth.
 */
function renderAssistantText(text: string): React.ReactNode {
  const blocks: React.ReactNode[] = []
  let bullets: string[] = []

  const flushBullets = () => {
    if (bullets.length === 0) return
    blocks.push(
      <ul key={`list-${blocks.length}`} className="space-y-0.5">
        {bullets.map((line, index) => (
          <li key={index} className="type-figure text-[0.8125rem]">
            {emphasise(line)}
          </li>
        ))}
      </ul>,
    )
    bullets = []
  }

  // Line by line rather than block by block, because a reply routinely mixes the
  // two: "Tienes $X en total:" followed immediately by a bulleted account list.
  // Deciding per block meant such a paragraph rendered as one run of text with its
  // newlines collapsed.
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) {
      flushBullets()
      continue
    }
    if (trimmed.startsWith('•')) {
      bullets.push(trimmed.replace(/^•\s*/, '· '))
      continue
    }
    flushBullets()
    blocks.push(<p key={`p-${blocks.length}`}>{emphasise(trimmed)}</p>)
  }
  flushBullets()

  return blocks
}

/** Renders **bold** runs and leaves everything else as text. */
function emphasise(text: string): React.ReactNode {
  const parts = text.split(/(\*\*[^*]+\*\*)/g)
  return parts.map((part, index) =>
    part.startsWith('**') && part.endsWith('**') ? (
      <strong key={index} className="font-semibold">
        {part.slice(2, -2)}
      </strong>
    ) : (
      part
    ),
  )
}

function Thinking({ tool }: { tool: string | null }) {
  return (
    <p className="flex items-center gap-2 text-[0.8125rem] text-ink-faint" role="status">
      <span aria-hidden="true" className="flex gap-1">
        {[0, 1, 2].map((index) => (
          <span
            key={index}
            className="size-1.5 animate-pulse rounded-full bg-ink-faint"
            style={{ animationDelay: `${index * 160}ms` }}
          />
        ))}
      </span>
      {tool ? (TOOL_LABELS[tool] ?? tool) + '…' : 'Pensando…'}
    </p>
  )
}

function Suggestions({ provider, onPick }: { provider: boolean; onPick: (text: string) => void }) {
  const suggestions = [
    '¿Cuánto dinero tengo?',
    'Muéstrame mis últimos 5 movimientos',
    'Ingresa $200 en mi cuenta',
    'Retira $50',
  ]

  return (
    <div className="py-2">
      <EmptyState title="Pregúntame por tus cuentas">
        Consulto saldos y movimientos, registro ingresos, y preparo retiros y
        transferencias para que los confirmes.
        {!provider && (
          <span className="mt-1 block text-[0.8125rem] text-ink-faint">
            Sin clave de IA entiendo frases concretas como las de abajo.
          </span>
        )}
      </EmptyState>

      <ul className="mt-1 flex flex-wrap justify-center gap-1.5">
        {suggestions.map((suggestion) => (
          <li key={suggestion}>
            <button
              type="button"
              onClick={() => onPick(suggestion)}
              className={cx(
                'rounded-full border border-rule bg-paper-raised px-2.5 py-1 text-[0.75rem]',
                'text-ink-soft transition-colors hover:border-copper hover:text-copper',
              )}
            >
              {suggestion}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

function ChatSkeleton() {
  return (
    <div className="space-y-4" aria-hidden="true">
      <div className="flex justify-end">
        <div className="skeleton h-8 w-40 rounded-[5px]" />
      </div>
      <div className="space-y-1.5">
        <div className="skeleton h-3 w-3/4" />
        <div className="skeleton h-3 w-1/2" />
      </div>
    </div>
  )
}
