// Package httpx is the HTTP transport: routing helpers, middleware, request
// decoding and the single place where a domain error becomes a status code.
//
// Centralising that translation is deliberate. Handlers return domain errors
// and never choose a status themselves, so a new endpoint cannot report
// insufficient funds as a 500 by omission, and the wording the customer reads
// lives in exactly one file.
package httpx

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// Error is a failure already shaped for the client.
//
// Message is user-facing and in Spanish, matching the interface. Code is a
// stable machine-readable identifier the frontend can branch on without
// matching prose. cause is logged but never serialised: it is where the
// database driver's or the ledger's own wording stays.
type Error struct {
	Status  int
	Code    string
	Message string
	// Fields carries per-field validation messages, keyed by the JSON field
	// name so a form can highlight the offending input.
	Fields map[string]string
	cause  error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches the underlying error for logging.
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

// Constructors for the failures that come up across handlers. Each returns a
// fresh value so a caller can attach a cause without mutating a shared one.

func BadRequest(code, message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Message: message}
}

func Unauthorized(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: message}
}

func Forbidden(message string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: message}
}

func NotFound(code, message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: code, Message: message}
}

func Conflict(code, message string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: message}
}

func Unprocessable(code, message string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: code, Message: message}
}

func TooManyRequests(message string) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: message}
}

func Internal(err error) *Error {
	return &Error{
		Status:  http.StatusInternalServerError,
		Code:    "internal_error",
		Message: "Ocurrió un error inesperado. Inténtalo de nuevo.",
		cause:   err,
	}
}

// Invalid reports a validation failure, with per-field detail.
func Invalid(fields map[string]string) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "validation_failed",
		Message: "Revisa los datos enviados.",
		Fields:  fields,
	}
}

// asAPIError maps any error to the response the client will see.
//
// Domain errors are translated explicitly; anything unrecognised becomes a 500
// with its detail withheld from the response and kept for the log. That
// direction of defaulting matters: an unmapped error must never leak a database
// message to a browser, and it must never be quietly downgraded to a 4xx that
// makes a bug look like the customer's mistake.
func asAPIError(err error) *Error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}

	switch {
	// --- store ---
	// A repository reporting "no rows" that no handler chose to interpret is
	// still a missing resource, not a server fault.
	case errors.Is(err, store.ErrNotFound):
		return NotFound("not_found", "El recurso solicitado no existe.").WithCause(err)

	case errors.Is(err, store.ErrConflict):
		return Conflict("conflict", "El recurso ya existe o entra en conflicto con otro.").WithCause(err)

	// --- ledger: money ---
	case errors.Is(err, ledger.ErrInsufficientFunds):
		return Unprocessable("insufficient_funds",
			"La cuenta no tiene fondos suficientes para esta operación.").WithCause(err)

	case errors.Is(err, ledger.ErrSourceNotFound):
		return NotFound("source_account_not_found", "La cuenta de origen no existe.").WithCause(err)

	case errors.Is(err, ledger.ErrDestinationNotFound):
		return NotFound("destination_account_not_found", "La cuenta de destino no existe.").WithCause(err)

	case errors.Is(err, ledger.ErrSameAccount):
		return BadRequest("same_account",
			"La cuenta de origen y la de destino deben ser distintas.").WithCause(err)

	case errors.Is(err, ledger.ErrAmountNotPositive), errors.Is(err, money.ErrNotPositive):
		return BadRequest("amount_not_positive", "El monto debe ser mayor que cero.").WithCause(err)

	case errors.Is(err, ledger.ErrInvalidAccountNumber):
		return BadRequest("invalid_account_number", "El número de cuenta no es válido.").WithCause(err)

	// --- ledger: two-phase confirmation ---
	case errors.Is(err, ledger.ErrHoldNotFound):
		return NotFound("confirmation_not_found",
			"Esta confirmación ya no existe.").WithCause(err)

	case errors.Is(err, ledger.ErrHoldExpired):
		return Conflict("confirmation_expired",
			"La confirmación expiró y los fondos se liberaron. Vuelve a intentarlo.").WithCause(err)

	case errors.Is(err, ledger.ErrHoldAlreadySettled):
		return Conflict("already_confirmed", "Esta operación ya fue confirmada.").WithCause(err)

	case errors.Is(err, ledger.ErrHoldAlreadyVoided):
		return Conflict("already_cancelled", "Esta operación ya fue cancelada.").WithCause(err)

	// --- ledger: idempotency ---
	// A replayed movement is not a failure; the caller's earlier attempt
	// succeeded. Handlers that can distinguish it return the original result
	// instead, so reaching here means a path that cannot, and reporting the
	// operation as done is still the truthful answer.
	case errors.Is(err, ledger.ErrPreviouslyFailed):
		return Conflict("movement_previously_failed",
			"Esta operación ya había fallado antes y no puede reintentarse con el mismo identificador.").WithCause(err)

	// --- money parsing ---
	case errors.Is(err, money.ErrTooPrecise):
		return BadRequest("amount_too_precise", "El monto admite como máximo dos decimales.").WithCause(err)

	case errors.Is(err, money.ErrMalformed), errors.Is(err, money.ErrEmpty):
		return BadRequest("amount_malformed", "El monto no tiene un formato válido.").WithCause(err)

	case errors.Is(err, money.ErrOutOfRange):
		return BadRequest("amount_out_of_range", "El monto está fuera del rango admitido.").WithCause(err)

	case errors.Is(err, money.ErrUnsupportedCurrency):
		return BadRequest("unsupported_currency", "Solo se admiten montos en dólares (USD).").WithCause(err)

	default:
		return Internal(err)
	}
}
