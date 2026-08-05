package chat

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/mcpserver"
)

// The size of the fixed preamble decides two things, so it is measured rather than
// assumed.
//
// It is sent on every single call, including each round of the tool loop, which makes
// it the most expensive thing in a conversation and the one most worth caching. And
// caching only engages above a per-model minimum — 1024 tokens on Sonnet, 2048 on
// Haiku — so a preamble that shrinks below the line silently stops being cached and
// starts costing full price on every call, with nothing failing to announce it.
//
// That is the regression this file exists to catch. `go test` reports the character
// counts with no credentials at all; with ANTHROPIC_API_KEY set it asks the API for
// the exact token count, which is free — count_tokens bills nothing.

// minimumCacheableTokens is Sonnet's threshold. Below this, cache_control is accepted
// and quietly does nothing.
const minimumCacheableTokens = 1024

// fixedPreamble builds what goes out ahead of the conversation on every call: the
// system prompt and the six tool definitions.
//
// The tools come from the real MCP server rather than a copy of them, because their
// descriptions and schemas are what is actually sent. Deps is left zero-valued on
// purpose: the services behind it are only dereferenced when a tool is *called*, and
// listing them touches nothing — which is what lets this run without a database or a
// ledger.
func fixedPreamble(t *testing.T) llm.Request {
	t.Helper()

	ctx := context.Background()
	session, err := mcpserver.Open(ctx, mcpserver.Deps{}, uuid.New())
	if err != nil {
		t.Fatalf("opening an MCP session: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Logf("closing the MCP session: %v", err)
		}
	})

	specs, err := session.Tools(ctx)
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("the MCP server published no tools")
	}

	return llm.Request{
		System: systemPrompt("Ana Gómez", time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)),
		Tools:  toLLMTools(specs),
	}
}

func TestFixedPreambleSize(t *testing.T) {
	req := fixedPreamble(t)

	systemChars := len(req.System)
	toolChars := 0
	for _, tool := range req.Tools {
		toolChars += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
	}

	t.Logf("system prompt: %d characters", systemChars)
	t.Logf("%d tools: %d characters", len(req.Tools), toolChars)
	t.Logf("preamble total: %d characters", systemChars+toolChars)

	// Without a key this is all that can be said, and it is still worth saying: the
	// figure is what the estimate in EstimateCost is derived from.
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("set ANTHROPIC_API_KEY for an exact count (count_tokens is free)")
	}

	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-5"
	}

	provider, err := llm.NewAnthropic(apiKey, model)
	if err != nil {
		t.Fatalf("building the provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tokens, err := provider.CountTokens(ctx, req)
	if err != nil {
		t.Fatalf("counting tokens: %v", err)
	}

	t.Logf("preamble: %d tokens, measured against %s", tokens, model)
	t.Logf("characters per token: %.2f", float64(systemChars+toolChars)/float64(tokens))

	if tokens < minimumCacheableTokens {
		t.Errorf("the preamble is %d tokens, below the %d needed for prompt caching to "+
			"engage on Sonnet — cache_control is being sent and doing nothing, and every "+
			"call is paying full price for these tokens",
			tokens, minimumCacheableTokens)
	}

	// What one exchange costs, from measured input rather than a guess. Two calls:
	// the model asks for a tool, then answers with its result.
	perMessage := estimateExchange(model, tokens)
	t.Logf("a two-call exchange at this size costs about %d micro-dollars ($%.4f)",
		perMessage, float64(perMessage)/1_000_000)
}

// estimateExchange prices one customer message: two calls, the preamble on both, and
// a short reply each time.
func estimateExchange(model string, preambleTokens int) int64 {
	const (
		conversationTokens = 800 // a few turns of history
		toolResultTokens   = 400 // a balance or a handful of movements
		replyTokens        = 150
	)
	at := time.Now()

	// The first call pays to write the preamble into the cache; the second reads it.
	first, _ := llm.Cost(model, llm.Usage{
		CacheWriteTokens: preambleTokens,
		InputTokens:      conversationTokens,
		OutputTokens:     replyTokens,
	}, at)
	second, _ := llm.Cost(model, llm.Usage{
		CacheReadTokens: preambleTokens,
		InputTokens:     conversationTokens + toolResultTokens + replyTokens,
		OutputTokens:    replyTokens,
	}, at)

	return first + second
}
