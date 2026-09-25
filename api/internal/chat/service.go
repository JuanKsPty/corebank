// Package chat is the assistant: the agentic loop that runs between a language
// model and the bank's MCP tools.
//
// The loop is the interesting part, and it lives here rather than in the provider
// so that its rules hold whoever is answering — how many rounds of tools are
// allowed and what happens when one fails. See mcpserver for why the model cannot act as anybody but the
// authenticated customer.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/mcpserver"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// historyTurns is how much of the conversation is replayed to the model.
//
// Bounded because the whole history is sent on every message: unbounded, it would
// grow until it cost more than it helped and then be rejected outright. It is also
// the term that grows fastest — every message pays for every turn before it — so it
// was cut from twenty to ten. Ten turns is still more banking context than any
// single request needs, and a conversation that genuinely needs more has usually
// finished one errand and started another.
const historyTurns = 10

// Service runs the assistant.
type Service struct {
	db       *store.DB
	mcp      mcpserver.Deps
	provider llm.Provider
	maxTurns int
	now      func() time.Time
}

func NewService(db *store.DB, deps mcpserver.Deps, provider llm.Provider, maxTurns int) *Service {
	return &Service{db: db, mcp: deps, provider: provider, maxTurns: maxTurns, now: time.Now}
}

// Provider reports which engine is answering, so the interface can label it.
func (s *Service) Provider() llm.Provider { return s.provider }

// ProviderState reports which engine is answering and why.
//
// Asked after every exchange, not only on load: the engine can change mid-session
// when a spend ceiling is reached or the model stops answering, and a label that
// only updates on a page refresh would be telling the customer their reply came
// from a model that did not write it.
//
// A provider that cannot explain itself — a bare Unavailable — is described from
// what it does expose, which is the honest reading: no key was configured.
func (s *Service) ProviderState() ProviderState {
	if p, ok := s.provider.(stateful); ok {
		return p.State()
	}
	if s.provider.IsAI() {
		return ProviderState{Name: s.provider.Name(), IsAI: true, Engine: EngineAI}
	}
	return ProviderState{Name: s.provider.Name(), IsAI: false, Engine: EngineUnconfigured}
}

// Event is something that happened while producing a reply. The handler forwards
// these to the browser as they occur, so a slow tool call is visible progress
// rather than a stalled request.
type Event struct {
	Kind EventKind
	// Text is the assistant's prose, for EventMessage.
	Text string
	// Tool is the tool's name, for EventToolCall and EventToolResult.
	Tool string
	// Failed marks a tool that returned an error.
	Failed bool
	// Code and Message describe a failure, for EventError.
	Code    string
	Message string
	// Provider is set on EventDone: which engine actually answered, which can have
	// changed during this very exchange.
	Provider *ProviderState
	// Proposal is set on EventProposal: a change the assistant proposed, for the
	// owner to apply or discard.
	Proposal *Proposal
}

// Proposal is what the interface needs to render a proposal's card. It comes
// from the tool's own structured result, which the proposals package wrote,
// never from the model's prose.
type Proposal struct {
	ID      string `json:"proposal_id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

type EventKind string

const (
	EventMessage    EventKind = "message"
	EventToolCall   EventKind = "tool_call"
	EventToolResult EventKind = "tool_result"
	EventProposal   EventKind = "proposal"
	EventError      EventKind = "error"
	EventDone       EventKind = "done"
)

// Emit receives events as the reply is produced.
type Emit func(Event)

// Reply runs one exchange: the customer's message in, events out.
//
// Persistence happens as the exchange proceeds rather than at the end, so a
// connection that drops mid-reply still leaves the conversation — and any
// reservation the assistant made — recoverable on reload.
func (s *Service) Reply(ctx context.Context, userID uuid.UUID, customerName, message string, emit Emit) error {
	session, err := mcpserver.Open(ctx, s.mcp, userID)
	if err != nil {
		return err
	}
	defer func() {
		if err := session.Close(); err != nil {
			logging.FromContext(ctx).Warn("closing the MCP session", "error", err)
		}
	}()

	tools, err := session.Tools(ctx)
	if err != nil {
		return err
	}

	history, err := s.loadHistory(ctx, userID)
	if err != nil {
		return err
	}

	if err := s.persist(ctx, userID, llm.Message{Role: llm.RoleUser, Text: message}); err != nil {
		return err
	}
	history = append(history, llm.Message{Role: llm.RoleUser, Text: message})

	request := llm.Request{
		System:   systemPrompt(customerName, s.now()),
		Messages: history,
		Tools:    toLLMTools(tools),
	}

	logger := logging.FromContext(ctx)

	// The agentic loop. Each pass asks the provider what to do next; if it asks
	// for tools, they run and the results go back. The bound is not paranoia: a
	// model that misreads a tool result can ask for the same thing indefinitely,
	// and without a ceiling that is an unbounded spend on a single message.
	for turn := 1; ; turn++ {
		reply, err := s.provider.Complete(ctx, request)
		if err != nil {
			return err
		}
		logger.Info("assistant turn",
			"turn", turn, "provider", s.provider.Name(),
			"tool_calls", len(reply.ToolCalls),
			"input_tokens", reply.InputTokens, "output_tokens", reply.OutputTokens)

		assistantTurn := llm.Message{
			Role:      llm.RoleAssistant,
			Text:      reply.Text,
			ToolCalls: reply.ToolCalls,
		}
		if err := s.persist(ctx, userID, assistantTurn); err != nil {
			return err
		}
		request.Messages = append(request.Messages, assistantTurn)

		if reply.Text != "" {
			emit(Event{Kind: EventMessage, Text: reply.Text})
		}
		if !reply.WantsTools() {
			return nil
		}

		if turn >= s.maxTurns {
			// Stop and say so rather than silently truncating: the customer needs
			// to know the request was not completed, and the reservations made
			// along the way will expire on their own.
			logger.Warn("assistant stopped at the tool-round limit",
				"limit", s.maxTurns, "pending_calls", len(reply.ToolCalls))
			emit(Event{
				Kind: EventError, Code: "too_many_steps",
				Message: "No pude completar la consulta en los pasos disponibles. " +
					"Prueba a pedírmelo de forma más concreta.",
			})
			return nil
		}

		toolTurn, err := s.runTools(ctx, session, reply.ToolCalls, emit)
		if err != nil {
			return err
		}
		if err := s.persist(ctx, userID, toolTurn); err != nil {
			return err
		}
		request.Messages = append(request.Messages, toolTurn)
	}
}

// runTools executes the calls the model asked for and emits what happened.
func (s *Service) runTools(ctx context.Context, session *mcpserver.Session, calls []llm.ToolCall, emit Emit) (llm.Message, error) {
	turn := llm.Message{Role: llm.RoleTool, Results: make([]llm.ToolResult, 0, len(calls))}
	logger := logging.FromContext(ctx)

	// Sequentially, not concurrently, so the events arrive in the order the
	// model asked for the calls and the log reads as one story.
	for _, call := range calls {
		emit(Event{Kind: EventToolCall, Tool: call.Name})

		result, err := session.Call(ctx, call.Name, call.Arguments)
		if err != nil {
			return llm.Message{}, err
		}

		logger.Info("tool call",
			"tool", call.Name, "failed", result.IsError,
			// The arguments are logged because this is the audit trail of what the
			// assistant did with somebody's money.
			"arguments", string(call.Arguments))

		emit(Event{Kind: EventToolResult, Tool: call.Name, Failed: result.IsError})
		if p := proposalOf(call.Name, result); p != nil {
			emit(Event{Kind: EventProposal, Tool: call.Name, Proposal: p})
		}

		turn.Results = append(turn.Results, llm.ToolResult{
			CallID:  call.ID,
			Content: result.Text,
			IsError: result.IsError,
		})
	}
	return turn, nil
}

// proposalOf reads the proposal a propose_* tool stored, if it stored one.
func proposalOf(tool string, result mcpserver.Result) *Proposal {
	if result.IsError || !strings.HasPrefix(tool, "propose_") || len(result.Structured) == 0 {
		return nil
	}
	var p Proposal
	if err := json.Unmarshal(result.Structured, &p); err != nil || p.ID == "" {
		return nil
	}
	return &p
}

// History returns the conversation for the interface to render on load.
func (s *Service) History(ctx context.Context, userID uuid.UUID) ([]store.ChatMessage, error) {
	return s.db.Q().ChatHistory(ctx, userID, historyTurns*2)
}

// Clear discards the conversation.
func (s *Service) Clear(ctx context.Context, userID uuid.UUID) error {
	return s.db.Q().ClearChatHistory(ctx, userID)
}

// loadHistory reads the stored conversation back into the provider's shape.
func (s *Service) loadHistory(ctx context.Context, userID uuid.UUID) ([]llm.Message, error) {
	stored, err := s.db.Q().ChatHistory(ctx, userID, historyTurns*2)
	if err != nil {
		return nil, err
	}

	messages := make([]llm.Message, 0, len(stored))
	for _, row := range stored {
		var m llm.Message
		if err := json.Unmarshal(row.Content, &m); err != nil {
			// A row this code can no longer read would otherwise poison every
			// later message. Dropping it loses context; failing loses the chat.
			logging.FromContext(ctx).Warn("skipping an unreadable chat message",
				"message_id", row.ID, "error", err)
			continue
		}
		m.Role = llm.Role(row.Role)
		messages = append(messages, m)
	}
	return trimToValidStart(messages), nil
}

// trimToValidStart drops leading turns that cannot begin a conversation.
//
// The window can open in the middle of an exchange — on a tool result whose call
// was cut off, for instance — and a provider will reject that as malformed. Dropping
// from the front until the history starts with a customer message costs a little
// context and keeps the conversation working.
func trimToValidStart(messages []llm.Message) []llm.Message {
	for i, m := range messages {
		if m.Role == llm.RoleUser {
			return messages[i:]
		}
	}
	return nil
}

// persist stores one turn.
func (s *Service) persist(ctx context.Context, userID uuid.UUID, m llm.Message) error {
	// An assistant turn with neither prose nor tool calls carries nothing.
	if m.Text == "" && len(m.ToolCalls) == 0 && len(m.Results) == 0 {
		return nil
	}

	content, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("chat: encoding a message: %w", err)
	}
	_, err = s.db.Q().AppendChatMessage(ctx, userID, string(m.Role), content)
	return err
}

func toLLMTools(specs []mcpserver.ToolSpec) []llm.Tool {
	tools := make([]llm.Tool, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, llm.Tool{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
		})
	}
	return tools
}

// ErrNoProvider means the assistant has no engine at all.
var ErrNoProvider = errors.New("chat: no assistant is configured")
