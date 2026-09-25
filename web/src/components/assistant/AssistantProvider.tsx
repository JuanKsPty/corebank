import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { ApiError, streamChat } from '@/api/client'
import type { ChatEvent, ChatProvider } from '@/api/types'
import { keys, useChatHistory, useClearChat } from '@/lib/queries'

/**
 * The assistant's conversation, owned above the layout.
 *
 * This state used to live inside the panel component, which was fine while the panel
 * existed in exactly one place. It no longer does: on a wide screen the transcript is
 * a column beside the page, and on a phone it rises out of the tab bar in a sheet.
 * Had the state stayed in the view, those two would each own a copy — two `turns`
 * arrays, and worse, two `streamChat` connections writing into different state, with
 * a resize across the breakpoint tearing down whichever one was mid-answer.
 *
 * So the stream lives here, above the fork, and both surfaces are pure renderers of
 * it. Mounting, unmounting or duplicating a view is now free, and a reply that
 * arrives while the sheet is shut is still there when it opens.
 */

export interface ToolRun {
  name: string
  failed?: boolean
}

export interface Turn {
  id: string
  role: 'user' | 'assistant'
  text?: string
  tools?: ToolRun[]
  /** Set on a turn that failed, so the message is styled as a failure. */
  error?: string
}

/**
 * What each tool did, in the customer's words.
 *
 * The trace is shown, not hidden. When software can move your money, seeing exactly
 * which operations it performed is not developer detail — it is the audit trail, and
 * it is the reason to trust the answer above it.
 */
export const TOOL_LABELS: Record<string, string> = {
  list_accounts: 'Consultó tus cuentas',
  get_balance: 'Consultó un saldo',
  list_transactions: 'Consultó tus movimientos',
  deposit: 'Registró un ingreso',
  prepare_withdrawal: 'Preparó un retiro',
  prepare_transfer: 'Preparó una transferencia',
}

interface AssistantContextValue {
  turns: Turn[]
  draft: string
  setDraft: (value: string) => void
  streaming: boolean
  activeTool: string | null
  failure: string | null
  dismissFailure: () => void
  /** Sends `text`, or the current draft when called without one. */
  send: (text?: string) => void
  clear: () => void
  clearing: boolean
  provider?: ChatProvider
  loadingHistory: boolean
  /** Whether the sheet is open. Shared so the trigger and the sheet agree. */
  open: boolean
  setOpen: (value: boolean) => void
  /**
   * Whether the assistant is docked as a column, on displays wide enough to hold one.
   * Remembered across visits: it is a preference about how somebody wants to work, not
   * a transient piece of view state.
   */
  docked: boolean
  setDocked: (value: boolean) => void
}

const DOCKED_KEY = 'corebank.assistant.docked'

/**
 * Reads the docking preference, defaulting to docked.
 *
 * Open on a first visit, because an assistant nobody finds is worth nothing and this one
 * is the reason the product exists. Shut the moment somebody shuts it, and it stays shut
 * — the choice is theirs, and having to make it again on every page would not be a
 * choice.
 */
function readDocked(): boolean {
  try {
    return window.localStorage.getItem(DOCKED_KEY) !== 'false'
  } catch {
    // Private browsing, or storage disabled. The preference is not important enough to
    // fail over.
    return true
  }
}

const AssistantContext = createContext<AssistantContextValue | null>(null)

export function useAssistant(): AssistantContextValue {
  const value = useContext(AssistantContext)
  if (!value) throw new Error('useAssistant must be used inside an AssistantProvider')
  return value
}

export function AssistantProvider({ children }: { children: React.ReactNode }) {
  const history = useChatHistory()
  const clearMutation = useClearChat()
  const queryClient = useQueryClient()

  const [turns, setTurns] = useState<Turn[]>([])
  const [draft, setDraft] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [activeTool, setActiveTool] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const [docked, setDockedState] = useState(readDocked)
  /**
   * The engine reported by the last exchange, which supersedes the one the history
   * endpoint gave on load.
   *
   * The two can disagree by the time somebody has sent a message: the AI budget is
   * finite and shared, so it can run out mid-session. Keeping the label on the
   * load-time value would attribute an unavailable notice to a model.
   */
  const [liveProvider, setLiveProvider] = useState<ChatProvider | null>(null)

  const abortRef = useRef<AbortController | null>(null)
  const hydratedRef = useRef(false)

  // The stored conversation is loaded once, into the same list the live stream
  // appends to. Loading it on every render would fight the stream for the same state.
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

  // A stream left running after the provider unmounts — a sign-out — would keep
  // writing into state that no longer exists.
  useEffect(() => () => abortRef.current?.abort(), [])

  const send = useCallback(
    (text?: string) => {
      const message = (text ?? draft).trim()
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
        setTurns((previous) =>
          previous.map((turn) => (turn.id === turnId ? change(turn) : turn)),
        )

      void (async () => {
        try {
          for await (const event of streamChat(message, controller.signal)) {
            applyEvent(event, update, setActiveTool, setLiveProvider)
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
      })()
    },
    [draft, streaming, queryClient],
  )

  const clear = useCallback(() => {
    clearMutation.mutate(undefined, { onSuccess: () => setTurns([]) })
  }, [clearMutation])

  const dismissFailure = useCallback(() => setFailure(null), [])

  const setDocked = useCallback((value: boolean) => {
    setDockedState(value)
    try {
      window.localStorage.setItem(DOCKED_KEY, String(value))
    } catch {
      // Nothing to do: the column still opens and closes, it just will not be
      // remembered next time.
    }
  }, [])

  const value = useMemo<AssistantContextValue>(
    () => ({
      turns,
      draft,
      setDraft,
      streaming,
      activeTool,
      failure,
      dismissFailure,
      send,
      clear,
      clearing: clearMutation.isPending,
      provider: liveProvider ?? history.data?.provider,
      loadingHistory: history.isLoading,
      open,
      setOpen,
      docked,
      setDocked,
    }),
    [
      turns,
      draft,
      streaming,
      activeTool,
      failure,
      dismissFailure,
      send,
      clear,
      clearMutation.isPending,
      liveProvider,
      history.data?.provider,
      history.isLoading,
      open,
      docked,
      setDocked,
    ],
  )

  return <AssistantContext.Provider value={value}>{children}</AssistantContext.Provider>
}

/** applyEvent folds one stream event into the assistant's turn. */
function applyEvent(
  event: ChatEvent,
  update: (change: (turn: Turn) => Turn) => void,
  setActiveTool: (tool: string | null) => void,
  setLiveProvider: (provider: ChatProvider) => void,
) {
  switch (event.kind) {
    case 'message':
      // Turns arrive whole rather than token by token, so successive messages in one
      // exchange are joined rather than replacing each other.
      update((turn) => ({
        ...turn,
        text: turn.text ? `${turn.text}\n\n${event.text}` : event.text,
      }))
      break

    case 'tool_call':
      setActiveTool(event.tool)
      update((turn) => ({ ...turn, tools: [...(turn.tools ?? []), { name: event.tool }] }))
      break

    case 'tool_result':
      setActiveTool(null)
      update((turn) => {
        const tools = [...(turn.tools ?? [])]
        // Mark the most recent run of this tool, not the first: the same tool can be
        // called twice in one exchange.
        for (let i = tools.length - 1; i >= 0; i--) {
          if (tools[i]?.name === event.tool) {
            tools[i] = { name: event.tool, failed: event.failed }
            break
          }
        }
        return { ...turn, tools }
      })
      break

    case 'error':
      update((turn) => ({ ...turn, error: event.message }))
      break

    case 'done':
      setActiveTool(null)
      // The engine that actually answered. It can have changed during this very
      // exchange — a spend ceiling reached, a key that stopped working — so the
      // label is corrected here rather than waiting for a reload.
      if (event.provider) setLiveProvider(event.provider)
      break
  }
}
