package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/JuanKsPty/corebank/api/internal/logging"
)

// errorEnvelope is the single error shape the API ever returns, so the frontend
// has one branch to write instead of one per endpoint.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

// JSON writes v with the given status.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// The response is already half-decided at this point, so the only
		// honest move is to log it and send a generic failure.
		logging.FromContext(r.Context()).Error("encoding response failed", "error", err)
		writeEnvelope(w, r, Internal(err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// NoContent replies 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Fail maps err to a status and writes the error envelope, logging the cause.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := asAPIError(err)

	logger := logging.FromContext(r.Context())
	// 5xx is our bug and gets full detail at error level; 4xx is the client's
	// and stays at debug so a wall of failed logins does not bury a real fault.
	if apiErr.Status >= http.StatusInternalServerError {
		logger.Error("request failed", "code", apiErr.Code, "status", apiErr.Status, "error", err)
	} else {
		logger.Debug("request rejected", "code", apiErr.Code, "status", apiErr.Status, "error", err)
	}

	writeEnvelope(w, r, apiErr)
}

func writeEnvelope(w http.ResponseWriter, r *http.Request, apiErr *Error) {
	body, err := json.Marshal(errorEnvelope{Error: errorBody{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Fields:  apiErr.Fields,
		// Echoing the request id lets a user quote it and lets us find the
		// matching log line without asking them what they clicked.
		RequestID: RequestIDFromContext(r.Context()),
	}})
	if err != nil {
		http.Error(w, `{"error":{"code":"internal_error","message":"Ocurrió un error inesperado."}}`,
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(apiErr.Status)
	_, _ = w.Write(body)
}

// maxRequestBody caps a decoded request body. Every endpoint here takes a small
// JSON object except the chat, whose message is still text; a megabyte is far
// more than any of them needs and stops an unbounded body from becoming a
// memory problem.
const maxRequestBody = 1 << 20

// Decode reads a JSON request body into dst.
//
// Unknown fields are rejected rather than ignored: a client that misspells
// "amount" should be told so, not silently charged zero. w is needed only so
// an oversized body also closes the connection instead of being drained.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mediaType := strings.TrimSpace(strings.Split(ct, ";")[0]); mediaType != "application/json" {
			return BadRequest("unsupported_media_type", "El cuerpo de la petición debe ser JSON.")
		}
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	// A second value in the same body is a client bug worth surfacing, not
	// something to silently drop.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("malformed_json", "El cuerpo debe contener un único objeto JSON.")
	}
	return nil
}

func decodeError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxErr    *http.MaxBytesError
	)
	switch {
	case errors.As(err, &syntaxErr):
		return BadRequest("malformed_json",
			fmt.Sprintf("El JSON tiene un error de sintaxis en la posición %d.", syntaxErr.Offset))

	case errors.As(err, &typeErr):
		e := Invalid(map[string]string{
			typeErr.Field: fmt.Sprintf("Se esperaba un valor de tipo %s.", typeErr.Type.String()),
		})
		return e.WithCause(err)

	case errors.As(err, &maxErr):
		return &Error{
			Status: http.StatusRequestEntityTooLarge, Code: "body_too_large",
			Message: "El cuerpo de la petición es demasiado grande.", cause: err,
		}

	case errors.Is(err, io.EOF):
		return BadRequest("empty_body", "El cuerpo de la petición no puede estar vacío.")

	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return BadRequest("unknown_field", fmt.Sprintf("El campo %q no es válido.", field))

	default:
		return BadRequest("malformed_json", "El cuerpo de la petición no es JSON válido.")
	}
}
