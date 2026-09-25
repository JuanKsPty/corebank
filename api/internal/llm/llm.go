// Package llm is the boundary between the assistant and whatever produces its
// replies.
//
// Anthropic implements it when an API key is configured; when none is, the chat
// package's Unavailable stand-in says the assistant is off. Which one is in use
// is reported to the interface and shown to the person, so an answer is never
// passed off as coming from a model that did not write it.
//
// The interface is deliberately narrow — one round of "here is the conversation
// and the tools, what next?" — with the agentic loop living in the chat package.
// Keeping the loop out of the provider means the loop's rules (how many tool
// rounds, what to do with a failure, when to stop) are written once and hold
// regardless of who is answering.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Role identifies who produced a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool carries tool results back to the model. Providers map it onto
	// whatever their own API calls that.
	RoleTool Role = "tool"
)

// Message is one turn of the conversation.
type Message struct {
	Role Role
	Text string
	// ToolCalls are what an assistant turn asked for.
	ToolCalls []ToolCall
	// Results are what a tool turn is answering with, one per call.
	Results []ToolResult
}

// ToolCall is the model's request to run a tool.
type ToolCall struct {
	// ID pairs the call with its result. Providers that do not supply one get a
	// generated value, since the loop needs the pairing either way.
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult is what a tool returned.
type ToolResult struct {
	CallID  string
	Content string
	// IsError marks a failed call. It is sent to the model rather than aborting
	// the turn: "you do not have enough funds" is something to explain to the
	// customer, not a crash.
	IsError bool
}

// Tool is a tool as the model is told about it.
//
// The schema comes from the MCP server verbatim, so the model's view of a tool and
// the server's validation of it can never disagree.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Request is one turn's input.
type Request struct {
	System   string
	Messages []Message
	Tools    []Tool
}

// Reply is what the provider produced.
type Reply struct {
	Text      string
	ToolCalls []ToolCall
	// Usage is reported when the provider knows it. It is not only for the log:
	// what the budget is charged is computed from these, so the four counts have to
	// be kept apart. Cached input is billed at a different rate from fresh input —
	// writing the cache costs more than sending the tokens plainly, reading it costs
	// a fraction — so folding them into InputTokens would misprice every call after
	// the first.
	InputTokens  int
	OutputTokens int
	// CacheWriteTokens were stored in the cache by this call.
	CacheWriteTokens int
	// CacheReadTokens were served from the cache instead of being processed again.
	// Non-zero here is the only proof that caching is actually working.
	CacheReadTokens int
}

// Usage is what one call consumed, for pricing and for the record.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheWriteTokens int
	CacheReadTokens  int
}

// Usage returns what the call that produced this reply consumed.
func (r Reply) Usage() Usage {
	return Usage{
		InputTokens:      r.InputTokens,
		OutputTokens:     r.OutputTokens,
		CacheWriteTokens: r.CacheWriteTokens,
		CacheReadTokens:  r.CacheReadTokens,
	}
}

// WantsTools reports whether the loop should run tools and come back.
func (r Reply) WantsTools() bool { return len(r.ToolCalls) > 0 }

// Provider answers one turn of a conversation.
type Provider interface {
	// Name identifies the provider in logs and in the interface.
	Name() string
	// IsAI reports whether replies come from a language model. False for the
	// stand-in that answers when no model can, so the interface can say so plainly.
	IsAI() bool
	// Complete produces the next turn.
	Complete(ctx context.Context, req Request) (Reply, error)
}

// ErrUnavailable means the provider cannot answer right now — an unreachable API,
// an overloaded one, a rate limit. The chat reports it as a temporary failure and
// the next message tries again.
var ErrUnavailable = errors.New("llm: provider unavailable")

// ErrPermanent means the provider will not work again until somebody changes
// something: the key is rejected, or the account has no credit left.
//
// It is separated from ErrUnavailable because the two deserve opposite handling.
// Retrying a rate limit is right; retrying an exhausted balance burns a round trip
// on every message forever and tells the customer to "try again in a moment" when
// no amount of waiting will help. A caller that sees this should stop asking.
var ErrPermanent = errors.New("llm: provider permanently unavailable")
