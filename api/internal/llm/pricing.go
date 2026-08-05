package llm

import "time"

// What a call costs, so the budget can be charged what was actually used.
//
// Everything here is micro-dollars (1/1,000,000 of a dollar) as int64. A single
// call can cost a fraction of a cent, so cents cannot represent it; and the rule
// against floats that governs every balance in this system governs this too — a
// spend ceiling enforced with accumulated rounding error is not a ceiling.

// microsPerMillionUSD converts a price written in dollars per million tokens into
// the integer this file computes with: $3.00 per million becomes 3_000_000.
const microsPerMillionUSD = 1_000_000

// Cache multipliers, as hundredths so the arithmetic stays integral. Writing an
// entry costs a quarter more than sending the tokens plainly; reading one costs a
// tenth. That asymmetry is the whole economic case for caching, and it is why the
// four token counts are tracked separately rather than summed.
const (
	cacheWritePercent = 125
	cacheReadPercent  = 10
)

// price is one model's rate, in micro-dollars per million tokens.
type price struct {
	input  int64
	output int64
}

// pricing is a model's rate over time. A promotional rate is modelled explicitly
// rather than hardcoded, so the cost recorded in ai_usage is what was actually
// billed and not what the list said on the day this file was written.
type pricing struct {
	// introUntil is the last instant the intro rate applies. Zero means there is
	// no intro rate and standard is used throughout.
	introUntil time.Time
	intro      price
	standard   price
}

func usd(dollars, cents int64) int64 {
	return (dollars*100 + cents) * microsPerMillionUSD / 100
}

// sonnet5IntroEnds is when Sonnet 5's launch pricing lapses. Until then it is
// $2/$10 per million rather than $3/$15 — which is the whole evaluation window for
// this project, and the reason the numbers in the README are what they are.
var sonnet5IntroEnds = time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)

// rates is every model this application might be pointed at.
//
// Keyed by the exact model id, not a prefix: a prefix match would silently price a
// model nobody has checked. An id that is not here is charged the most expensive
// rate known — see Cost.
var rates = map[string]pricing{
	"claude-sonnet-5": {
		introUntil: sonnet5IntroEnds,
		intro:      price{input: usd(2, 0), output: usd(10, 0)},
		standard:   price{input: usd(3, 0), output: usd(15, 0)},
	},
	"claude-sonnet-4-6": {standard: price{input: usd(3, 0), output: usd(15, 0)}},
	"claude-haiku-4-5":  {standard: price{input: usd(1, 0), output: usd(5, 0)}},
	"claude-opus-5":     {standard: price{input: usd(5, 0), output: usd(25, 0)}},
	"claude-opus-4-8":   {standard: price{input: usd(5, 0), output: usd(25, 0)}},
	"claude-fable-5":    {standard: price{input: usd(10, 0), output: usd(50, 0)}},
}

// rateAt picks the price in force for a model at a given time.
func rateAt(model string, at time.Time) (price, bool) {
	p, ok := rates[model]
	if !ok {
		return price{}, false
	}
	if !p.introUntil.IsZero() && at.Before(p.introUntil) {
		return p.intro, true
	}
	return p.standard, true
}

// fallbackRate prices a model this file has never heard of.
//
// The most expensive rate known, deliberately. An unknown model has to be charged
// *something*, and the two directions are not symmetric: guessing low lets an
// unrecognised model spend past the ceiling without the guard noticing, while
// guessing high only makes the assistant fall back to its rule-based engine
// earlier than it strictly had to. Overcharging is recoverable; overspending is
// the thing this whole mechanism exists to prevent.
var fallbackRate = price{input: usd(10, 0), output: usd(50, 0)}

// Cost prices one call's usage in micro-dollars, and reports whether the model's
// rate was actually known.
//
// The caller is expected to log a false: it means the recorded cost is a
// deliberate overestimate and the pricing table needs a line adding.
func Cost(model string, u Usage, at time.Time) (int64, bool) {
	rate, known := rateAt(model, at)
	if !known {
		rate = fallbackRate
	}

	// Multiply before dividing, so a per-token rate is never rounded down to zero
	// on the way to being multiplied back up. The largest intermediate here is
	// roughly 1e15, well inside int64.
	total := perMillion(u.InputTokens, rate.input) +
		perMillion(u.OutputTokens, rate.output) +
		perMillion(u.CacheWriteTokens, rate.input*cacheWritePercent/100) +
		perMillion(u.CacheReadTokens, rate.input*cacheReadPercent/100)

	return total, known
}

func perMillion(tokens int, ratePerMillion int64) int64 {
	if tokens <= 0 {
		return 0
	}
	return int64(tokens) * ratePerMillion / 1_000_000
}

// charsPerToken is the ratio used to size a call before it is made.
//
// Two, measured, and not the three this started as. That first value came from the
// usual rule of thumb that text runs 3.5 to 4 characters per token, which would have
// made the estimate comfortably conservative. It is not what this prompt does: a
// production call reported 2722 cached tokens for a preamble of 5919 characters,
// which is 2.2 characters per token. Spanish prose and JSON schemas both tokenise
// denser than the English prose the rule of thumb is drawn from.
//
// At three, the input side of the estimate came out 27% *under* the real figure. The
// reservation still exceeded what the call cost, but only because the output
// allowance — priced at five times input and assumed to be spent in full — was
// carrying it. That is the ceiling's central guarantee resting on an unrelated
// constant: lower maxReplyTokens for a good reason and the estimate quietly stops
// being conservative, with nothing failing to say so. At two the input side
// over-estimates on its own, which is what the argument for the ceiling assumes.
const charsPerToken = 2

// EstimateCost sizes a call before making it, for the reservation.
//
// It prices every input token at the full rate, ignoring the cache entirely. That
// is another deliberate overestimate: most of these tokens will be read from cache
// at a tenth of the price, so the reservation is comfortably above what the call
// will actually cost.
func EstimateCost(model string, req Request, at time.Time) int64 {
	chars := len(req.System)
	for _, m := range req.Messages {
		chars += len(m.Text)
		for _, call := range m.ToolCalls {
			chars += len(call.Name) + len(call.Arguments)
		}
		for _, result := range m.Results {
			chars += len(result.Content)
		}
	}
	for _, tool := range req.Tools {
		chars += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
	}

	cost, _ := Cost(model, Usage{
		InputTokens: chars / charsPerToken,
		// The worst case, since what the model will actually write is unknowable
		// until it has written it.
		OutputTokens: maxReplyTokens,
	}, at)
	return cost
}
