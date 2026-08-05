import { ArrowUpIcon, PanelRightCloseIcon, SparklesIcon } from 'lucide-react'

import type { ChatEngine, ChatProvider } from '@/api/types'

import { ConfirmationCard } from '@/components/ConfirmationCard'
import { Badge } from '@/components/ui/badge'
import { Bubble, BubbleContent } from '@/components/ui/bubble'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { Message, MessageContent } from '@/components/ui/message'
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from '@/components/ui/message-scroller'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import { TOOL_LABELS, useAssistant, type Turn } from './AssistantProvider'

/**
 * The assistant's transcript, composer and all.
 *
 * A pure renderer: every piece of state it touches lives in the provider above it, so
 * this component can be mounted in the desktop column, in the phone's sheet, or in
 * both, without any of them owning a second conversation.
 *
 * A turn can carry three kinds of thing: what the assistant said, which tools it ran,
 * and — when it proposed a movement — the confirmation card. The card is placed in the
 * transcript rather than in a dialog on purpose. It belongs to the exchange that
 * produced it, and a dialog would make it feel like something the assistant did *to*
 * the customer instead of something it is asking them about. It is also why the sheet
 * on a phone stops short of the top of the screen: the balance stays visible behind
 * it, so the held funds can be watched leaving the available total.
 */
export function AssistantPanel({
  className,
  onClose,
}: {
  className?: string
  /** Present only where there is somewhere to close to — the docked column. */
  onClose?: () => void
}) {
  const {
    turns,
    draft,
    setDraft,
    streaming,
    activeTool,
    failure,
    dismissFailure,
    send,
    clear,
    clearing,
    provider,
    loadingHistory,
  } = useAssistant()

  return (
    <div className={cn('flex min-h-0 flex-col bg-paper-raised', className)}>
      <header className="flex items-center gap-3 border-b border-rule px-4 py-3">
        <div className="min-w-0 flex-1">
          <h2 className="type-display text-[0.9375rem]">Asistente</h2>
          {provider && <EngineLabel provider={provider} />}
        </div>
        {turns.length > 0 && (
          <Button
            variant="ghost"
            size="sm"
            onClick={clear}
            disabled={clearing || streaming}
            className="text-[0.75rem] text-ink-soft"
          >
            Limpiar
          </Button>
        )}
        {onClose && (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onClose}
            aria-label="Cerrar el asistente"
            className="text-ink-faint hover:text-ink"
          >
            <PanelRightCloseIcon className="size-4" aria-hidden="true" />
          </Button>
        )}
      </header>

      {/* The provider owns the scroll behaviour: it keeps the view pinned to the newest
          turn while an answer streams in, and stops doing so the moment the customer
          scrolls up to read something. That replaces a hand-written effect that set
          scrollTop on every state change and therefore yanked the transcript away from
          anybody trying to reread an earlier reply. */}
      <MessageScrollerProvider autoScroll defaultScrollPosition="end">
        <MessageScroller className="flex-1">
          <MessageScrollerViewport className="px-4 py-4">
            <MessageScrollerContent className="gap-4">
              {loadingHistory ? (
                <ChatSkeleton />
              ) : turns.length === 0 ? (
                <Opening onPick={(text) => send(text)} engine={provider?.engine ?? 'ai'} />
              ) : (
                turns.map((turn) => (
                  <MessageScrollerItem key={turn.id} messageId={turn.id}>
                    <TurnView turn={turn} />
                  </MessageScrollerItem>
                ))
              )}

              {streaming && <Working tool={activeTool} />}

              {failure && (
                <div
                  role="alert"
                  className="flex items-start gap-2 rounded-[5px] border border-danger/40 bg-danger/8 px-3 py-2 text-[0.8125rem] text-danger-text"
                >
                  <span className="min-w-0 flex-1">
                    <span className="block font-medium">El asistente se interrumpió</span>
                    {failure}
                  </span>
                  <button
                    type="button"
                    onClick={dismissFailure}
                    aria-label="Descartar"
                    className="shrink-0 opacity-60 hover:opacity-100"
                  >
                    ✕
                  </button>
                </div>
              )}
            </MessageScrollerContent>
          </MessageScrollerViewport>
          <MessageScrollerButton />
        </MessageScroller>
      </MessageScrollerProvider>

      <form
        onSubmit={(event) => {
          event.preventDefault()
          send()
        }}
        className="border-t border-rule p-3"
      >
        {/* Only when the budget is what ran out, and only above the composer — the
            moment somebody is about to type is the moment it matters. The header
            label says the same thing, but a conversation that was fluent a minute ago
            and is suddenly terse needs the reason where the next message is written,
            not in a caption. */}
        {provider?.engine === 'budget_exhausted' && turns.length > 0 && (
          <p className="mb-2 text-[0.75rem] leading-snug text-hold-text">
            Se agotó el presupuesto de IA de esta demo. Sigo funcionando con reglas: puedo
            consultar saldos y movimientos, y preparar movimientos para que los confirmes.
          </p>
        )}
        <label htmlFor="chat-input" className="sr-only">
          Escribe tu petición
        </label>
        <InputGroup>
          <InputGroupInput
            id="chat-input"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="¿Cuánto dinero tengo?"
            disabled={streaming}
            maxLength={2000}
            autoComplete="off"
          />
          <InputGroupAddon align="inline-end">
            <InputGroupButton
              type="submit"
              disabled={streaming || !draft.trim()}
              aria-label="Enviar"
              className="bg-ink text-paper hover:bg-ink/90"
              size="icon-xs"
            >
              {streaming ? <Spinner /> : <ArrowUpIcon aria-hidden="true" />}
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
      </form>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Turns
// ---------------------------------------------------------------------------

function TurnView({ turn }: { turn: Turn }) {
  if (turn.role === 'user') {
    return (
      <Message align="end">
        <MessageContent>
          <Bubble variant="default">
            <BubbleContent className="text-[0.875rem]">{turn.text}</BubbleContent>
          </Bubble>
        </MessageContent>
      </Message>
    )
  }

  return (
    <Message align="start">
      <MessageContent className="gap-2">
        {turn.tools && turn.tools.length > 0 && (
          <ul className="flex flex-wrap gap-1.5" aria-label="Operaciones realizadas">
            {turn.tools.map((tool, index) => (
              <li key={`${tool.name}-${index}`}>
                <Badge
                  variant="outline"
                  className={cn(
                    'type-figure text-[0.6875rem]',
                    tool.failed
                      ? 'border-danger/30 bg-danger/10 text-danger-text'
                      : 'border-rule bg-paper-sunken text-ink-soft',
                  )}
                >
                  {tool.failed ? '✕' : '✓'} {TOOL_LABELS[tool.name] ?? tool.name}
                </Badge>
              </li>
            ))}
          </ul>
        )}

        {turn.text && (
          <div className="space-y-2 text-[0.875rem] leading-relaxed">
            {renderAssistantText(turn.text)}
          </div>
        )}

        {turn.card && <ConfirmationCard card={turn.card} />}

        {turn.error && (
          <p className="text-[0.8125rem] text-danger-text" role="alert">
            {turn.error}
          </p>
        )}
      </MessageContent>
    </Message>
  )
}

/**
 * Renders a reply, deciding line by line rather than block by block.
 *
 * A reply routinely mixes the two: "Tienes $X en total:" followed immediately by a
 * bulleted account list. Deciding per block meant such a paragraph rendered as one run
 * of text with its newlines collapsed.
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

function Working({ tool }: { tool: string | null }) {
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
      {tool ? `${TOOL_LABELS[tool] ?? tool}…` : 'Pensando…'}
    </p>
  )
}

/**
 * Says which engine is answering, and when it is not the model, why.
 *
 * Four states rather than two. `is_ai` alone collapses three different situations
 * into one "no AI" that explains nothing: a project running without credentials,
 * a demo whose shared budget has been spent, and a model that is briefly
 * unreachable. The first is the normal way this repository runs on a fresh clone;
 * the second means somebody's money is gone and no amount of retrying will bring the
 * assistant back; the third clears by itself. Telling them apart is the difference
 * between a label and an excuse.
 */
function EngineLabel({ provider }: { provider: ChatProvider }) {
  const { tone, text } = describeEngine(provider)

  return (
    <p className="mt-0.5 flex items-center gap-1.5 text-[0.6875rem] text-ink-faint">
      <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', tone)} />
      <span className="truncate" title={text}>
        {text}
      </span>
    </p>
  )
}

function describeEngine(provider: ChatProvider): { tone: string; text: string } {
  switch (provider.engine) {
    case 'ai':
      return { tone: 'bg-credit', text: `Modelo ${provider.name}` }

    case 'budget_exhausted':
      // The honest version. "Unavailable, try again in a moment" would be a lie:
      // waiting does not refill a budget.
      return { tone: 'bg-hold', text: 'Presupuesto de IA agotado · respondo con reglas' }

    case 'degraded':
      return { tone: 'bg-hold', text: 'IA no disponible ahora · respondo con reglas' }

    case 'unconfigured':
    default:
      return { tone: 'bg-hold', text: 'Sin IA configurada · respondo con reglas' }
  }
}

/**
 * The first thing in an empty conversation.
 *
 * The previous version reused the generic empty state, whose illustration is a ruled
 * page with nothing on it — an icon that means "there is nothing here", which is the
 * wrong thing to say about a conversation that has not started yet. The suggestions
 * are the point of this screen, so they are the primary action rather than a footnote
 * under an apology, and tapping one sends it instead of merely typing it for you.
 */
function Opening({
  onPick,
  engine,
}: {
  onPick: (text: string) => void
  engine: ChatEngine
}) {
  const suggestions = [
    '¿Cuánto dinero tengo?',
    'Muéstrame mis últimos 5 movimientos',
    'Ingresa $200 en mi cuenta',
    'Retira $50',
  ]

  return (
    <Empty className="border-0 bg-transparent py-2">
      <EmptyHeader>
        <EmptyMedia variant="icon" className="bg-copper/10 text-copper">
          <SparklesIcon />
        </EmptyMedia>
        <EmptyTitle className="type-display text-[1rem]">
          Pídeme lo que necesites
        </EmptyTitle>
        <EmptyDescription className="text-[0.8125rem]">
          Consulto saldos y movimientos, registro ingresos, y preparo retiros y
          transferencias para que los confirmes.
          {/* Why the phrasing has to be concrete, when it does. Somebody whose
              sentence was not understood deserves to know it was the engine and not
              their wording. */}
          {engine !== 'ai' &&
            ' Ahora respondo con reglas, así que entiendo frases concretas como estas.'}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <ul className="flex w-full flex-col gap-1.5">
          {suggestions.map((suggestion) => (
            <li key={suggestion}>
              <button
                type="button"
                onClick={() => onPick(suggestion)}
                className={cn(
                  'w-full rounded-[5px] border border-rule bg-paper-raised px-3 py-2 text-left',
                  'text-[0.8125rem] text-ink-soft transition-colors',
                  'hover:border-copper hover:text-copper',
                )}
              >
                {suggestion}
              </button>
            </li>
          ))}
        </ul>
      </EmptyContent>
    </Empty>
  )
}

function ChatSkeleton() {
  return (
    <div className="space-y-4" aria-hidden="true">
      <div className="flex justify-end">
        <Skeleton className="h-8 w-40 rounded-[5px]" />
      </div>
      <div className="space-y-1.5">
        <Skeleton className="h-3 w-3/4" />
        <Skeleton className="h-3 w-1/2" />
      </div>
    </div>
  )
}
