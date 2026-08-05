package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// Prompt caching fails silently. A breakpoint in the wrong place, or missing, is not
// an error — the request succeeds and every call quietly pays full price for tokens
// it could have read back at a tenth. The only symptom is the bill.
//
// So the request is marshalled and inspected here. It cannot prove the cache is
// being *hit* — that needs a live call and a real key, and shows up as
// cache_read_input_tokens in the log — but it does prove the two breakpoints are
// where they are meant to be, which is the half that lives in this repository.

// marshalRequest builds the params exactly as Complete does and returns the JSON
// that would go over the wire.
func marshalRequest(t *testing.T, req Request) map[string]any {
	t.Helper()

	messages, err := toAnthropicMessages(req.Messages)
	if err != nil {
		t.Fatalf("toAnthropicMessages: %v", err)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model("claude-sonnet-5"),
		MaxTokens: maxReplyTokens,
		Messages:  messages,
		Tools:     toAnthropicTools(req.Tools),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}
	markCacheBreakpoint(messages)

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshalling the request: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshalling the request: %v", err)
	}
	return out
}

// The system block carries the first breakpoint. Anthropic's cacheable prefix runs
// tools → system → messages, so one breakpoint here covers the tool schemas too —
// which is the whole fixed preamble, and the most expensive thing in a conversation
// because it is re-sent on every call including each round of the tool loop.
func TestSystemBlockCarriesACacheBreakpoint(t *testing.T) {
	body := marshalRequest(t, Request{
		System:   "You are a banking assistant.",
		Messages: []Message{{Role: RoleUser, Text: "¿Cuál es mi saldo?"}},
		Tools: []Tool{
			{Name: "get_balance", Description: "Returns a balance.", InputSchema: []byte(`{"type":"object"}`)},
		},
	})

	system, ok := body["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatalf("the request carries no system block: %v", body["system"])
	}
	block, _ := system[0].(map[string]any)
	control, ok := block["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("the system block has no cache_control: %v", block)
	}
	if control["type"] != "ephemeral" {
		t.Errorf("cache_control type = %v, want ephemeral", control["type"])
	}
}

// The second breakpoint sits on the last block of the last message, so each call
// reads the prefix the previous one cached and extends it. Without it, every round of
// the tool loop re-sends the whole conversation at full price.
func TestTheLastMessageCarriesACacheBreakpoint(t *testing.T) {
	tests := []struct {
		name string
		last Message
	}{
		{
			name: "a customer's message",
			last: Message{Role: RoleUser, Text: "¿Cuál es mi saldo?"},
		},
		{
			name: "an assistant turn asking for a tool",
			last: Message{Role: RoleAssistant, ToolCalls: []ToolCall{
				{ID: "toolu_1", Name: "get_balance", Arguments: []byte(`{}`)},
			}},
		},
		{
			name: "a tool result",
			last: Message{Role: RoleTool, Results: []ToolResult{
				{CallID: "toolu_1", Content: `{"available":"1000.00"}`},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			messages := []Message{{Role: RoleUser, Text: "Hola"}}
			if tc.last.Role != RoleUser {
				// A tool result has to follow the call it answers, or the API rejects
				// the conversation as malformed.
				messages = append(messages, Message{Role: RoleAssistant, ToolCalls: []ToolCall{
					{ID: "toolu_1", Name: "get_balance", Arguments: []byte(`{}`)},
				}})
			}
			messages = append(messages, tc.last)

			body := marshalRequest(t, Request{System: "You are an assistant.", Messages: messages})

			raw, _ := json.Marshal(body["messages"])
			list, ok := body["messages"].([]any)
			if !ok || len(list) == 0 {
				t.Fatalf("the request carries no messages: %s", raw)
			}

			final, _ := list[len(list)-1].(map[string]any)
			blocks, ok := final["content"].([]any)
			if !ok || len(blocks) == 0 {
				t.Fatalf("the last message has no content blocks: %v", final)
			}

			block, _ := blocks[len(blocks)-1].(map[string]any)
			if _, ok := block["cache_control"]; !ok {
				t.Errorf("the last block of the last message has no cache_control: %v", block)
			}

			// Exactly one breakpoint among the messages. Anthropic allows four in
			// total and one is already spent on the system block; scattering them
			// would waste the allowance and pay to write prefixes nothing reads back.
			if got := strings.Count(string(raw), `"cache_control"`); got != 1 {
				t.Errorf("found %d cache breakpoints among the messages, want 1", got)
			}
		})
	}
}

// A request with no conversation yet must not panic on the way to finding a block to
// mark. There is nothing to cache and nothing to crash over.
func TestCacheBreakpointOnAnEmptyConversation(t *testing.T) {
	markCacheBreakpoint(nil)
	markCacheBreakpoint([]anthropic.MessageParam{{}})
}
