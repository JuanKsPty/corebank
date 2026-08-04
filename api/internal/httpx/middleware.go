package httpx

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/logging"
)

type requestIDKey struct{}

// RequestIDHeader is the header the id is read from and echoed back on.
const RequestIDHeader = "X-Request-Id"

// RequestIDFromContext returns the current request's id, or "" outside a request.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RequestID assigns every request a correlation id and echoes it back.
//
// An id supplied by the client is honoured so a trace can span the browser and
// the API, but it is length-capped and stripped of anything unprintable before
// being copied into logs and response headers — an unbounded attacker-supplied
// string in a log line is a log-injection primitive, not a convenience.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitiseRequestID(r.Header.Get(RequestIDHeader))
		if id == "" {
			id = uuid.NewString()
		}

		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sanitiseRequestID(raw string) string {
	const maxLen = 64
	if len(raw) > maxLen {
		raw = raw[:maxLen]
	}
	var b strings.Builder
	for _, r := range raw {
		// Deliberately narrow: enough for a UUID or a trace id, and nothing
		// that could forge a new log line or a header.
		if r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RequestLogger logs one line per request and puts a request-scoped logger in
// the context, so everything logged downstream carries the same id without any
// layer having to thread it through by hand.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqLogger := logger.With("request_id", RequestIDFromContext(r.Context()))
			ctx := logging.WithLogger(r.Context(), reqLogger)

			// chi's wrapper records the status and byte count while still
			// forwarding Flush, which the chat's SSE stream depends on.
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r.WithContext(ctx))

			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_ip", ClientIP(r),
			}
			// A 5xx is already logged with its cause by Fail; this line is the
			// access record, so it stays at info and does not duplicate detail.
			switch {
			case ww.Status() >= http.StatusInternalServerError:
				reqLogger.Error("request", attrs...)
			case ww.Status() >= http.StatusBadRequest:
				reqLogger.Warn("request", attrs...)
			default:
				reqLogger.Info("request", attrs...)
			}
		})
	}
}

// Recoverer turns a panic into a 500 instead of a dropped connection, and logs
// the stack so the bug is diagnosable.
//
// It re-panics on http.ErrAbortHandler, which is the documented way to abandon
// a response on purpose and must keep working.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			logging.FromContext(r.Context()).Error("panic recovered",
				"panic", rec, "stack", string(debug.Stack()))
			Fail(w, r, Internal(errPanic))
		}()
		next.ServeHTTP(w, r)
	})
}

// errPanic keeps the recovered value out of the response while still giving the
// mapper something to wrap.
var errPanic = &Error{
	Status: http.StatusInternalServerError, Code: "internal_error",
	Message: "Ocurrió un error inesperado. Inténtalo de nuevo.",
}

// SecurityHeaders sets the headers that are cheap and unconditionally correct
// for a JSON API. The frontend is served by nginx, which sets its own
// content-security policy for the HTML; these apply to API responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// Nothing the API returns should ever be cached: it is all
		// account-specific and some of it is a session token.
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// CORS allows the listed origins, with credentials, for the browser dev server.
//
// In the shipped stack nginx serves the SPA and proxies /api on the same
// origin, so no CORS is involved at all; this exists for `npm run dev`. Origins
// are matched exactly against an allowlist and reflected — never "*" — because
// "*" is incompatible with credentialed requests and would defeat the point.
func CORS(origins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && slices.Contains(origins, origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers",
					"Authorization, Content-Type, Idempotency-Key, "+RequestIDHeader)
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Expose-Headers", RequestIDHeader)
				h.Set("Access-Control-Max-Age", "600")
				// Responses vary by origin, so a shared cache must not serve
				// one origin's response to another.
				h.Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP is the address used for rate limiting.
//
// It trusts X-Forwarded-For only because the one proxy in front of this API is
// the nginx container in the same compose stack. Exposed directly to the
// internet, this header is client-controlled and would make per-IP limits
// trivially bypassable — which is why the trust boundary is stated here rather
// than assumed.
func ClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, found := strings.Cut(fwd, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(fwd)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
