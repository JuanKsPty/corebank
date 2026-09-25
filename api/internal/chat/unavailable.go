package chat

import (
	"context"

	"github.com/JuanKsPty/corebank/api/internal/llm"
)

// Unavailable answers when no language model can: no API key is configured, a
// spend ceiling was reached, or the model cannot be reached.
//
// It says so and nothing else. The interface labels why from the provider's
// state (see Budgeted), so this message only has to be true in every one of those
// cases. A rule-based imitation of the assistant was tried and removed: pattern
// matching over a handful of phrasings is not an analyst, and presenting it as
// one was worse than saying the assistant is off.
type Unavailable struct{}

func NewUnavailable() Unavailable { return Unavailable{} }

func (Unavailable) Name() string { return "asistente no disponible" }
func (Unavailable) IsAI() bool   { return false }

// unavailableReply is shown in place of an answer.
const unavailableReply = "El asistente no está disponible en este momento, así que no puedo responder. " +
	"Tus cuentas, movimientos e importaciones siguen funcionando con normalidad."

func (Unavailable) Complete(context.Context, llm.Request) (llm.Reply, error) {
	return llm.Reply{Text: unavailableReply}, nil
}
