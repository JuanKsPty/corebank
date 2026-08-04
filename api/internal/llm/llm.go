// Package llm is the boundary between the assistant and whatever produces its
// replies.
//
// One interface with two implementations. Anthropic drives it when an API key is
// configured; a small deterministic interpreter drives it when none is, so the
// MCP tools and the confirmation flow stay demonstrable on a machine with no
// credentials. Which one is in use is reported to the interface and shown to the
// customer, because a rule-based fallback presented as an AI assistant would be a
// lie about the product.
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
	// Usage is reported when the provider knows it, for the log.
	InputTokens  int
	OutputTokens int
}

// WantsTools reports whether the loop should run tools and come back.
func (r Reply) WantsTools() bool { return len(r.ToolCalls) > 0 }

// Provider answers one turn of a conversation.
type Provider interface {
	// Name identifies the provider in logs and in the interface.
	Name() string
	// IsAI reports whether replies come from a language model. False for the
	// deterministic fallback, so the interface can say so plainly.
	IsAI() bool
	// Complete produces the next turn.
	Complete(ctx context.Context, req Request) (Reply, error)
}

// ErrUnavailable means the provider cannot answer right now — an unreachable API,
// a rejected key, a rate limit. The chat reports it as a temporary failure rather
// than a bug.
var ErrUnavailable = errors.New("llm: provider unavailable")
