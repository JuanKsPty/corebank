package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Session is a live MCP client and server pair bound to one customer.
//
// A session is built per chat request rather than shared, and that is the
// mechanism behind this package's central claim. The server's tool handlers close
// over the authenticated user at construction; there is no field, argument or
// header on the wire that could change it afterwards. Sharing one server across
// users would mean passing identity *through* the protocol, which is exactly the
// thing a language model must not be able to influence.
//
// The cost is a handshake over a pair of in-memory pipes — microseconds, no
// sockets, no processes.
type Session struct {
	client       *mcp.ClientSession
	serverToStop *mcp.ServerSession
}

// Open starts a session acting as userID.
func Open(ctx context.Context, deps Deps, userID uuid.UUID) (*Session, error) {
	if userID == uuid.Nil {
		// Refusing the zero user is not defensive noise: it would otherwise mean
		// "some account belonging to nobody", and the tools would happily operate
		// on it.
		return nil, errors.New("mcpserver: a session requires an authenticated user")
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := newServer(deps, userID).Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcpserver: connecting the server: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "corebank-chat", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, fmt.Errorf("mcpserver: connecting the client: %w", err)
	}
	return &Session{client: clientSession, serverToStop: serverSession}, nil
}

// Close shuts the session down.
func (s *Session) Close() error {
	// Closing the client ends the server side too; both are closed so a failure
	// on one still releases the other.
	return errors.Join(s.client.Close(), s.serverToStop.Close())
}

// ToolSpec is a tool as the model needs to see it.
type ToolSpec struct {
	Name        string
	Description string
	// InputSchema is the JSON Schema the MCP server published, forwarded to the
	// model unchanged. Deriving the model's tool definitions from the server's own
	// schemas is what keeps the two from drifting: a field renamed in the Go
	// struct changes what the model is told, automatically.
	InputSchema json.RawMessage
}

// Tools lists what the session offers.
func (s *Session) Tools(ctx context.Context) ([]ToolSpec, error) {
	list, err := s.client.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mcpserver: listing tools: %w", err)
	}

	specs := make([]ToolSpec, 0, len(list.Tools))
	for _, tool := range list.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcpserver: encoding the schema of %s: %w", tool.Name, err)
		}
		specs = append(specs, ToolSpec{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	return specs, nil
}

// Result is a tool call's outcome.
type Result struct {
	// Text is what goes back to the model: the structured result as JSON, or the
	// error message when the call failed.
	Text string
	// IsError marks a failed call. It is reported to the model rather than raised,
	// because a tool failure is something the model should explain or recover
	// from — "you do not have enough funds" is a conversational answer, not a
	// crash.
	IsError bool
	// Structured is the tool's typed result, used by the chat layer to detect a
	// confirmation without re-parsing prose.
	Structured json.RawMessage
}

// Call invokes a tool with the model's arguments.
func (s *Session) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	// Arguments arrive as whatever the model produced. They are handed to the MCP
	// server as-is and validated there against the published schema — one
	// validation, in the place that declared the shape.
	var args map[string]any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return Result{
				Text:    fmt.Sprintf("los argumentos no son un objeto JSON válido: %v", err),
				IsError: true,
			}, nil
		}
	}

	result, err := s.client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		// A transport or protocol failure — an unknown tool name, a schema
		// violation. Still reported to the model, which can correct itself.
		return Result{Text: err.Error(), IsError: true}, nil
	}

	out := Result{IsError: result.IsError}
	if result.StructuredContent != nil {
		if encoded, err := json.Marshal(result.StructuredContent); err == nil {
			out.Structured = encoded
			out.Text = string(encoded)
		}
	}
	if out.Text == "" {
		out.Text = textOf(result)
	}
	return out, nil
}

// textOf flattens a result's content blocks.
func textOf(result *mcp.CallToolResult) string {
	var text string
	for _, content := range result.Content {
		if block, ok := content.(*mcp.TextContent); ok {
			if text != "" {
				text += "\n"
			}
			text += block.Text
		}
	}
	if text == "" {
		text = "(sin contenido)"
	}
	return text
}

// Confirmation is a reservation a tool created, extracted from its result.
type Confirmation struct {
	ID                 uuid.UUID
	Kind               string
	Amount             string
	FromAccount        string
	ToAccount          string
	BalanceIfConfirmed string
	ExpiresAt          string
}

// ConfirmationFrom reads a confirmation out of a tool result.
//
// The chat layer uses this to emit the confirmation card, and it reads the
// *structured* result rather than the model's prose. That matters: if the card came
// from what the model said, a model that hallucinated a confirmation id would
// produce a button, and one that forgot to mention the reservation would leave the
// customer with funds held and nothing to click.
func ConfirmationFrom(tool string, result Result) (Confirmation, bool) {
	if result.IsError || !RequiresConfirmation(tool) || len(result.Structured) == 0 {
		return Confirmation{}, false
	}

	var raw confirmationResult
	if err := json.Unmarshal(result.Structured, &raw); err != nil {
		return Confirmation{}, false
	}
	id, err := uuid.Parse(raw.ConfirmationID)
	if err != nil {
		return Confirmation{}, false
	}
	return Confirmation{
		ID:                 id,
		Kind:               raw.Kind,
		Amount:             raw.Amount,
		FromAccount:        raw.FromAccount,
		ToAccount:          raw.ToAccount,
		BalanceIfConfirmed: raw.BalanceIfConfirmed,
		ExpiresAt:          raw.ExpiresAt,
	}, true
}
