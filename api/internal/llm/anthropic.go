package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic answers with a Claude model.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// maxReplyTokens bounds one reply.
//
// Sized to a banking answer — a few sentences and a short table at most. It was
// 1500, which was generous for no reason: output is billed at five times the rate
// of input, so this is the single highest-leverage number in the file. Lowering it
// does not truncate real answers; it lowers the worst case of a model that starts
// enumerating.
const maxReplyTokens = 700

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
		// The first cache breakpoint. Anthropic's cacheable prefix runs tools →
		// system → messages, so a breakpoint here covers the tool schemas as well:
		// the whole fixed preamble is written once and read at a tenth of the price
		// on every call afterwards. That preamble goes out on every single request,
		// including each round of the tool loop, which is what makes it the most
		// expensive thing in the conversation and the most worth caching.
		params.System = []anthropic.TextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}
	// The second breakpoint, at the end of the conversation so far. Each call reads
	// the previous call's prefix and extends it, which is what keeps the tool loop
	// from paying full price to re-send everything it just sent.
	markCacheBreakpoint(messages)

	message, err := a.client.Messages.New(ctx, params)
	if err != nil {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		return Reply{}, classify(err)
	}

	reply := Reply{
		InputTokens:      int(message.Usage.InputTokens),
		OutputTokens:     int(message.Usage.OutputTokens),
		CacheWriteTokens: int(message.Usage.CacheCreationInputTokens),
		CacheReadTokens:  int(message.Usage.CacheReadInputTokens),
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

// CountTokens reports how many input tokens a request would consume, without
// generating anything.
//
// The endpoint is free, which is the point. Every number about what this assistant
// costs — the spend ceilings, the figures in the README — should be measured rather
// than estimated from character counts, and this is how that happens without
// spending any of the budget it is measuring.
//
// It builds the request the same way Complete does, so what is counted is what would
// actually be sent rather than an approximation of it.
func (a *Anthropic) CountTokens(ctx context.Context, req Request) (int, error) {
	messages, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return 0, err
	}

	params := anthropic.MessageCountTokensParams{
		Model:    anthropic.Model(a.model),
		Messages: messages,
		Tools:    toCountTokensTools(req.Tools),
	}
	if req.System != "" {
		params.System = anthropic.MessageCountTokensParamsSystemUnion{
			OfTextBlockArray: []anthropic.TextBlockParam{{Text: req.System}},
		}
	}

	count, err := a.client.Messages.CountTokens(ctx, params)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, classify(err)
	}
	return int(count.InputTokens), nil
}

// toCountTokensTools mirrors toAnthropicTools into the shape the counting endpoint
// takes. The two unions are distinct types in the SDK for the same underlying JSON,
// so the conversion is mechanical rather than meaningful.
func toCountTokensTools(tools []Tool) []anthropic.MessageCountTokensToolUnionParam {
	out := make([]anthropic.MessageCountTokensToolUnionParam, 0, len(tools))
	for _, param := range toAnthropicTools(tools) {
		if param.OfTool == nil {
			continue
		}
		out = append(out, anthropic.MessageCountTokensToolUnionParam{OfTool: param.OfTool})
	}
	return out
}

// markCacheBreakpoint puts a cache breakpoint on the last block of the last
// message, so the prefix cached by one call is the prefix read by the next.
//
// Only the three block kinds this package produces are handled. Anything else is
// left alone rather than guessed at: a missing breakpoint costs money, a wrong one
// costs a rejected request.
func markCacheBreakpoint(messages []anthropic.MessageParam) {
	if len(messages) == 0 {
		return
	}
	blocks := messages[len(messages)-1].Content
	if len(blocks) == 0 {
		return
	}

	// The union holds pointers, so writing through them reaches the block the
	// request will marshal — the copy here is only of the pointer.
	switch block := blocks[len(blocks)-1]; {
	case block.OfText != nil:
		block.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case block.OfToolUse != nil:
		block.OfToolUse.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case block.OfToolResult != nil:
		block.OfToolResult.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
}

// creditExhausted matches the wording the API uses when the account has run out of
// balance. It arrives as a 400, the same status as a malformed request, so the
// status alone cannot tell "you owe money" from "you sent nonsense".
var creditExhausted = regexp.MustCompile(`(?i)credit balance|billing|insufficient (funds|credit)|purchase credits`)

// classify decides whether a failure is worth retrying.
//
// The distinction is the whole point: a rate limit or an overloaded API clears on
// its own, while a rejected key or an empty balance does not clear until somebody
// acts. Treating the second like the first is how an application ends up telling
// its users to "try again in a moment" indefinitely.
//
// What arrives here has already been retried: the client is built with two retries,
// which it applies to 408, 409, 429 and 5xx.
func classify(err error) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		// Not an API response at all — a dial failure, a TLS problem, a timeout.
		// Those clear on their own.
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// The key is wrong, revoked, or lacks permission. Waiting changes nothing.
		return fmt.Errorf("%w: the API key was rejected (%d)", ErrPermanent, apiErr.StatusCode)

	case http.StatusBadRequest:
		// RawJSON rather than Error(): the SDK's Error() dereferences Request and
		// Response without checking them for nil.
		if creditExhausted.MatchString(apiErr.RawJSON()) {
			return fmt.Errorf("%w: the account is out of credit", ErrPermanent)
		}
		// Anything else at 400 is this code sending something the API will not
		// accept — our bug, not a spend problem. Transient is the wrong label but
		// the right behaviour: it does not latch the provider off over a bug that a
		// deploy will fix.
		return fmt.Errorf("%w: the request was rejected: %v", ErrUnavailable, apiErr.RawJSON())

	default:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
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
