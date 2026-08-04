package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic answers with a Claude model.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// maxReplyTokens bounds one reply. Generous for a banking answer — which is a few
// sentences and a table at most — and low enough that a runaway generation cannot
// stall the request.
const maxReplyTokens = 1500

// NewAnthropic builds a provider for the given API key and model.
func NewAnthropic(apiKey, model string) (*Anthropic, error) {
	if apiKey == "" {
		return nil, errors.New("llm: an Anthropic API key is required")
	}
	if model == "" {
		return nil, errors.New("llm: a model name is required")
	}
	return &Anthropic{
		client: anthropic.NewClient(
			option.WithAPIKey(apiKey),
			// A chat request is interactive: the customer is watching. Better to
			// fail and say so than to hold the connection open indefinitely.
			option.WithRequestTimeout(60*time.Second),
			option.WithMaxRetries(2),
		),
		model: model,
	}, nil
}

func (a *Anthropic) Name() string { return a.model }
func (a *Anthropic) IsAI() bool   { return true }

// Complete sends the conversation and returns the model's next turn.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Reply, error) {
	messages, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return Reply{}, err
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: maxReplyTokens,
		Messages:  messages,
		Tools:     toAnthropicTools(req.Tools),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	message, err := a.client.Messages.New(ctx, params)
	if err != nil {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		// Wrapped as unavailable so the chat reports "the assistant is
		// unreachable" rather than surfacing an HTTP status to the customer.
		return Reply{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	reply := Reply{
		InputTokens:  int(message.Usage.InputTokens),
		OutputTokens: int(message.Usage.OutputTokens),
	}
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			reply.Text += block.Text
		case "tool_use":
			reply.ToolCalls = append(reply.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
		// Other block types — thinking, server tool use — are not requested and
		// are ignored rather than mishandled.
	}
	return reply, nil
}

// toAnthropicMessages converts the conversation to the SDK's shape.
//
// The mapping is not one-to-one. Anthropic expects tool results as user-role
// blocks referencing the call, and an assistant turn's text and tool calls in one
// message; this package models them as separate roles because that is easier for
// the loop to build. The translation lives here so a second provider can map the
// same neutral history onto its own conventions.
func toAnthropicMessages(history []Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(history))

	for _, m := range history {
		switch m.Role {
		case RoleUser:
			if m.Text == "" {
				// An empty user turn would be rejected by the API, and it carries
				// nothing anyway.
				continue
			}
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Text)))

		case RoleAssistant:
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.ToolCalls)+1)
			if m.Text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, call := range m.ToolCalls {
				// The arguments go back exactly as the model produced them.
				// Re-encoding them from a decoded map could reorder keys or change
				// number formatting, and the API compares the echoed block with
				// what it sent.
				var input any
				if len(call.Arguments) > 0 {
					if err := json.Unmarshal(call.Arguments, &input); err != nil {
						return nil, fmt.Errorf("llm: tool call %s has invalid arguments: %w", call.ID, err)
					}
				} else {
					input = map[string]any{}
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, input, call.Name))
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))

		case RoleTool:
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Results))
			for _, result := range m.Results {
				blocks = append(blocks,
					anthropic.NewToolResultBlock(result.CallID, result.Content, result.IsError))
			}
			if len(blocks) == 0 {
				continue
			}
			// Tool results are a user-role turn in Anthropic's protocol: they are
			// input to the model, not something it produced.
			out = append(out, anthropic.NewUserMessage(blocks...))

		default:
			return nil, fmt.Errorf("llm: unknown role %q", m.Role)
		}
	}
	return out, nil
}

// toAnthropicTools forwards the MCP schemas to the model.
func toAnthropicTools(tools []Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))

	for _, tool := range tools {
		schema := anthropic.ToolInputSchemaParam{}
		// The schema arrives as JSON from the MCP server. Unmarshalling it into
		// the SDK's own type keeps the properties and the required list intact
		// without this package having to understand JSON Schema.
		if len(tool.InputSchema) > 0 {
			if err := schema.UnmarshalJSON(tool.InputSchema); err != nil {
				// A tool whose schema will not load is skipped rather than sent
				// malformed: the model simply does not see it, and the others keep
				// working.
				continue
			}
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        tool.Name,
			Description: anthropic.String(tool.Description),
			InputSchema: schema,
		}})
	}
	return out
}
