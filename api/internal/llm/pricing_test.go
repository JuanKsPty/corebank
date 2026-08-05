package llm

import (
	"testing"
	"time"
)

// duringIntro and afterIntro straddle the end of Sonnet 5's launch pricing.
var (
	duringIntro = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	afterIntro  = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

func TestCost(t *testing.T) {
	tests := []struct {
		name  string
		model string
		at    time.Time
		usage Usage
		want  int64 // micro-dollars
		known bool
	}{
		{
			// A typical message with one round of tools, priced at the launch rate:
			// $2 per million in, $10 per million out.
			//   5800 * 2  = 11600
			//    370 * 10 =  3700
			name:  "sonnet 5 at the intro rate",
			model: "claude-sonnet-5",
			at:    duringIntro,
			usage: Usage{InputTokens: 5800, OutputTokens: 370},
			want:  15_300,
			known: true,
		},
		{
			// The same call once the promotion lapses: $3 / $15.
			//   5800 * 3  = 17400
			//    370 * 15 =  5550
			name:  "sonnet 5 at the standard rate",
			model: "claude-sonnet-5",
			at:    afterIntro,
			usage: Usage{InputTokens: 5800, OutputTokens: 370},
			want:  22_950,
			known: true,
		},
		{
			// The same conversation with the fixed preamble served from cache. The
			// point of the test is that cached input is *not* charged as input:
			//   4100 fresh input  * 2         = 8200
			//    370 output       * 10        = 3700
			//   1700 cache read   * 2 * 0.1   =  340
			// which is a fifth cheaper than the uncached call above.
			name:  "cached input is charged at a tenth",
			model: "claude-sonnet-5",
			at:    duringIntro,
			usage: Usage{InputTokens: 4100, OutputTokens: 370, CacheReadTokens: 1700},
			want:  12_240,
			known: true,
		},
		{
			// Writing the cache costs a quarter more than sending the tokens plainly:
			//   1700 * 2 * 1.25 = 4250
			name:  "cache writes cost a quarter more",
			model: "claude-sonnet-5",
			at:    duringIntro,
			usage: Usage{CacheWriteTokens: 1700},
			want:  4_250,
			known: true,
		},
		{
			// Haiku has no promotional rate, so the date does not matter.
			//   5800 * 1 = 5800
			//    370 * 5 = 1850
			name:  "haiku has one rate",
			model: "claude-haiku-4-5",
			at:    afterIntro,
			usage: Usage{InputTokens: 5800, OutputTokens: 370},
			want:  7_650,
			known: true,
		},
		{
			// An unpriced model is charged the most expensive rate known — $10/$50 —
			// so that a model nobody has checked cannot spend past the ceiling
			// unnoticed.
			//   5800 * 10 = 58000
			//    370 * 50 = 18500
			name:  "an unknown model is charged the dearest rate",
			model: "claude-something-nobody-priced",
			at:    duringIntro,
			usage: Usage{InputTokens: 5800, OutputTokens: 370},
			want:  76_500,
			known: false,
		},
		{
			name:  "no usage costs nothing",
			model: "claude-sonnet-5",
			at:    duringIntro,
			usage: Usage{},
			want:  0,
			known: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, known := Cost(tc.model, tc.usage, tc.at)
			if got != tc.want {
				t.Errorf("Cost() = %d micros, want %d", got, tc.want)
			}
			if known != tc.known {
				t.Errorf("Cost() known = %v, want %v", known, tc.known)
			}
		})
	}
}

// The intro rate has to end on the stated day, not the day after: an off-by-one
// here undercharges every call for 24 hours.
func TestIntroRateBoundary(t *testing.T) {
	justBefore := sonnet5IntroEnds.Add(-time.Second)
	justAfter := sonnet5IntroEnds.Add(time.Second)

	before, _ := rateAt("claude-sonnet-5", justBefore)
	if before.input != usd(2, 0) {
		t.Errorf("a second before the deadline: input = %d, want %d", before.input, usd(2, 0))
	}

	after, _ := rateAt("claude-sonnet-5", justAfter)
	if after.input != usd(3, 0) {
		t.Errorf("a second after the deadline: input = %d, want %d", after.input, usd(3, 0))
	}
}

// The estimate exists to be too high. If it ever comes out below what the call
// actually cost, the reservation does not bound anything.
func TestEstimateCostOverestimates(t *testing.T) {
	req := Request{
		System: "You are a helpful banking assistant. " + repeat("word ", 200),
		Messages: []Message{
			{Role: RoleUser, Text: "¿Cuál es mi saldo?"},
		},
		Tools: []Tool{
			{Name: "get_balance", Description: "Returns a balance.", InputSchema: []byte(`{"type":"object"}`)},
		},
	}

	estimate := EstimateCost("claude-sonnet-5", req, duringIntro)

	// The real call: the same input, and an output far shorter than the ceiling the
	// estimate assumes.
	chars := len(req.System) + len(req.Messages[0].Text) +
		len(req.Tools[0].Name) + len(req.Tools[0].Description) + len(req.Tools[0].InputSchema)
	actual, _ := Cost("claude-sonnet-5", Usage{
		// Real text runs nearer four characters per token than three.
		InputTokens:  chars / 4,
		OutputTokens: 80,
	}, duringIntro)

	if estimate <= actual {
		t.Errorf("estimate %d micros must exceed the actual %d micros", estimate, actual)
	}
}

// The input half of the estimate has to be conservative on its own.
//
// It did not used to be. charsPerToken was 3, taken from the usual 3.5–4 rule of
// thumb, while this prompt measures 2.2 characters per token in production — so the
// input estimate came out 27% under, and the reservation only stayed above the real
// cost because the output allowance was carrying it. That made the ceiling's
// guarantee depend on maxReplyTokens, which exists for a different reason and could
// reasonably be lowered.
//
// This pins the property directly: for text at the density this assistant actually
// sends, the estimated input tokens must exceed the real ones with the output
// allowance taken out of the picture entirely.
func TestEstimateCostOverestimatesInputAlone(t *testing.T) {
	// The measured preamble: 5919 characters, 2722 tokens.
	const (
		preambleChars  = 5919
		preambleTokens = 2722
	)

	req := Request{System: repeat("a", preambleChars)}

	estimate := EstimateCost("claude-sonnet-5", req, duringIntro)
	// Take the output allowance back out, leaving only what the estimate said the
	// input was worth.
	outputPart, _ := Cost("claude-sonnet-5", Usage{OutputTokens: maxReplyTokens}, duringIntro)
	inputPart := estimate - outputPart

	realInput, _ := Cost("claude-sonnet-5", Usage{InputTokens: preambleTokens}, duringIntro)

	if inputPart < realInput {
		t.Errorf("the estimate prices this input at %d micros but it really costs %d; "+
			"charsPerToken=%d is too high for the text this assistant sends, so the "+
			"reservation is only conservative while maxReplyTokens props it up",
			inputPart, realInput, charsPerToken)
	}
}

// An empty request still has to reserve something, or a caller could make
// unbounded calls that each reserve nothing.
func TestEstimateCostIsNeverZero(t *testing.T) {
	if got := EstimateCost("claude-sonnet-5", Request{}, duringIntro); got <= 0 {
		t.Errorf("EstimateCost() on an empty request = %d, want > 0", got)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}
