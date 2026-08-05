package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// maxMessageRunes caps one customer message. Long enough for any banking request,
// short enough that a pasted document cannot become an expensive prompt.
const maxMessageRunes = 2000

// replyTimeout bounds one exchange. A model that stalls must not hold a connection
// and a database transaction open indefinitely.
const replyTimeout = 2 * time.Minute

// Handler serves the chat endpoints. All of them require authentication.
type Handler struct {
	svc *Service
	// profiles supplies the customer's name for the system prompt.
	profiles ProfileReader
	limiter  *httpx.RateLimiter
}

// ProfileReader loads the authenticated customer. Declared here so this package
// does not depend on the authentication service to render a name.
type ProfileReader interface {
	Me(ctx context.Context, userID uuid.UUID) (store.User, error)
}

func NewHandler(svc *Service, profiles ProfileReader, limiter *httpx.RateLimiter) *Handler {
	return &Handler{svc: svc, profiles: profiles, limiter: limiter}
}

// Routes returns the /api/chat subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.history)
	r.Delete("/", h.clear)

	// Rate limited on its own budget: every message costs an upstream API call and
	// several database round trips, which is a different order of expense from
	// reading a balance.
	r.With(httpx.RateLimit(h.limiter,
		"Estás enviando mensajes muy rápido. Espera un momento.")).
		Post("/", h.send)
	return r
}

type sendRequest struct {
	Message string `json:"message"`
}

// send handles POST /api/chat, streaming the reply as server-sent events.
//
// SSE rather than a single JSON response because an exchange can take several
// seconds: the model thinks, a tool runs, then it thinks again. Streaming makes
// that visible progress instead of a spinner, and it is what lets the confirmation
// card appear the instant the funds are reserved rather than after the assistant
// finishes writing about it.
//
// SSE rather than WebSockets because the traffic is one-directional — the customer
// posts, the server narrates — and SSE needs no protocol upgrade, so it survives
// the nginx proxy in front of it without configuration.
func (h *Handler) send(w http.ResponseWriter, r *http.Request) {
	var body sendRequest
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	message := strings.TrimSpace(body.Message)
	switch {
	case message == "":
		httpx.Fail(w, r, httpx.Invalid(map[string]string{"message": "El mensaje no puede estar vacío."}))
		return
	case len([]rune(message)) > maxMessageRunes:
		httpx.Fail(w, r, httpx.Invalid(map[string]string{
			"message": fmt.Sprintf("El mensaje admite como máximo %d caracteres.", maxMessageRunes),
		}))
		return
	}

	// The stream has to be flushable, or every event would sit in a buffer until
	// the handler returned — which is precisely what streaming is for.
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.Fail(w, r, httpx.Internal(errors.New("chat: the response writer cannot stream")))
		return
	}

	userID := identity.MustFromContext(r.Context())
	customer, err := h.profiles.Me(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// Headers before the first event. Once these are written the status is 200 and
	// a later failure can only be reported as an event in the stream — which is why
	// everything that can fail cheaply is checked above.
	h.writeStreamHeaders(w)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(r.Context(), replyTimeout)
	defer cancel()

	logger := logging.FromContext(ctx)
	emit := func(event Event) {
		if err := writeEvent(w, event); err != nil {
			logger.Warn("could not write a chat event", "error", err)
			return
		}
		flusher.Flush()
	}

	if err := h.svc.Reply(ctx, userID, customer.FullName, message, emit); err != nil {
		emit(errorEvent(err))
	}

	// The engine that answered rides out on the closing event. It can differ from the
	// one the interface was told about on load — a spend ceiling reached, a key that
	// stopped working — and a label that only refreshes with the page would be
	// attributing this reply to a model that did not write it.
	state := h.svc.ProviderState()
	emit(Event{Kind: EventDone, Provider: &state})
}

func (h *Handler) writeStreamHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Connection", "keep-alive")
	// nginx buffers proxied responses by default, which would hold every event
	// until the stream closed and defeat the whole design.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// errorEvent maps a failure to something the customer can read.
func errorEvent(err error) Event {
	switch {
	case errors.Is(err, llm.ErrUnavailable):
		return Event{Kind: EventError, Code: "assistant_unavailable",
			Message: "El asistente no está disponible ahora mismo. Vuelve a intentarlo en un momento."}

	case errors.Is(err, context.DeadlineExceeded):
		return Event{Kind: EventError, Code: "assistant_timeout",
			Message: "El asistente tardó demasiado en responder. Inténtalo de nuevo."}

	case errors.Is(err, context.Canceled):
		// The customer navigated away. Nothing to report to a browser that is gone,
		// but the event keeps the stream well-formed for anything still reading.
		return Event{Kind: EventError, Code: "cancelled", Message: "Consulta cancelada."}

	default:
		return Event{Kind: EventError, Code: "assistant_error",
			Message: "No he podido completar la consulta. Inténtalo de nuevo."}
	}
}

// --- the wire format --------------------------------------------------------

// eventPayload is the JSON in an SSE `data:` line.
//
// One shape per event kind would be tidier to read but harder to consume: the
// browser would need a discriminated union per event type. A single optional-field
// shape keeps the client's handling to one switch.
type eventPayload struct {
	Text         string            `json:"text,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Failed       bool              `json:"failed,omitempty"`
	Confirmation *confirmationCard `json:"confirmation,omitempty"`
	Code         string            `json:"code,omitempty"`
	Message      string            `json:"message,omitempty"`
	Provider     *providerView     `json:"provider,omitempty"`
}

// confirmationCard is everything the interface needs to render the card and act on
// it — with no second request, so the card cannot appear before its own details.
type confirmationCard struct {
	// HoldID is what the client posts to /api/transactions/{hold_id}/confirm.
	// Ownership is verified there on every use, so this is a handle rather than a
	// capability.
	HoldID             string `json:"hold_id"`
	Kind               string `json:"kind"`
	Amount             string `json:"amount"`
	FromAccount        string `json:"from_account"`
	ToAccount          string `json:"to_account"`
	BalanceIfConfirmed string `json:"balance_if_confirmed,omitempty"`
	ExpiresAt          string `json:"expires_at"`
}

func writeEvent(w http.ResponseWriter, event Event) error {
	payload := eventPayload{
		Text:    event.Text,
		Tool:    event.Tool,
		Failed:  event.Failed,
		Code:    event.Code,
		Message: event.Message,
	}
	if event.Provider != nil {
		view := toProviderView(*event.Provider)
		payload.Provider = &view
	}
	if event.Confirmation != nil {
		payload.Confirmation = &confirmationCard{
			HoldID:             event.Confirmation.ID.String(),
			Kind:               event.Confirmation.Kind,
			Amount:             event.Confirmation.Amount,
			FromAccount:        event.Confirmation.FromAccount,
			ToAccount:          event.Confirmation.ToAccount,
			BalanceIfConfirmed: event.Confirmation.BalanceIfConfirmed,
			ExpiresAt:          event.Confirmation.ExpiresAt,
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// SSE frames are newline-delimited, so a newline inside the data would end the
	// frame early. JSON encoding already escapes them, which is part of why the
	// payload is JSON rather than raw text.
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Kind, data)
	return err
}

// --- history ----------------------------------------------------------------

type historyResponse struct {
	Messages []messageView `json:"messages"`
	// Provider tells the interface what is answering, so it can label a
	// rule-based fallback as such instead of passing it off as an assistant.
	Provider providerView `json:"provider"`
}

type providerView struct {
	Name string `json:"name"`
	IsAI bool   `json:"is_ai"`
	// Engine says *why* a rule-based reply is rule-based, which the interface needs
	// in order to be truthful. "No AI configured" and "the demo's AI budget ran out"
	// look identical from is_ai alone, and they are not the same message: the first
	// is how the project runs without credentials, the second means the money is
	// gone. One of them is worth telling somebody about.
	Engine string `json:"engine"`
}

func toProviderView(state ProviderState) providerView {
	return providerView{
		Name:   state.Name,
		IsAI:   state.IsAI,
		Engine: string(state.Engine),
	}
}

// messageView is a stored turn, rendered for display.
//
// Only the parts a person reads are exposed: prose, and the names of the tools that
// ran. The arguments and raw results stay server-side — they are the model's
// working, not the conversation.
type messageView struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Text      string    `json:"text,omitempty"`
	Tools     []string  `json:"tools,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	stored, err := h.svc.History(r.Context(), identity.MustFromContext(r.Context()))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := historyResponse{
		Messages: make([]messageView, 0, len(stored)),
		Provider: toProviderView(h.svc.ProviderState()),
	}

	for _, row := range stored {
		var turn llm.Message
		if err := json.Unmarshal(row.Content, &turn); err != nil {
			continue
		}
		// Tool turns carry results, not conversation; they are skipped so the
		// transcript reads the way the exchange sounded.
		if row.Role == string(llm.RoleTool) {
			continue
		}

		view := messageView{ID: row.ID, Role: row.Role, Text: turn.Text, CreatedAt: row.CreatedAt}
		for _, call := range turn.ToolCalls {
			view.Tools = append(view.Tools, call.Name)
		}
		if view.Text == "" && len(view.Tools) == 0 {
			continue
		}
		out.Messages = append(out.Messages, view)
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (h *Handler) clear(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Clear(r.Context(), identity.MustFromContext(r.Context())); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}
