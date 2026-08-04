package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
)

// Require rejects any request without a valid access token, and puts the verified
// user in the request context for everything downstream.
//
// This is the only place in the application that decides who a request belongs
// to. Nothing else writes an identity into a context from untrusted input, which
// is what makes "the user cannot act as someone else" a property of the wiring
// rather than a rule each handler has to remember.
func Require(tokens *TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := bearerToken(r)
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}

			userID, err := tokens.ParseAccessToken(raw)
			if err != nil {
				// An expired token gets its own code so the client refreshes
				// instead of dropping the customer back at the login form.
				if errors.Is(err, ErrTokenExpired) {
					httpx.Fail(w, r, (&httpx.Error{
						Status:  http.StatusUnauthorized,
						Code:    "token_expired",
						Message: "Tu sesión expiró. Renovando…",
					}).WithCause(err))
					return
				}
				httpx.Fail(w, r, httpx.Unauthorized(
					"Tu sesión no es válida. Inicia sesión de nuevo.").WithCause(err))
				return
			}

			next.ServeHTTP(w, r.WithContext(identity.WithUser(r.Context(), userID)))
		})
	}
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", httpx.Unauthorized("Falta el token de autenticación.")
	}

	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", httpx.Unauthorized("El encabezado Authorization debe ser «Bearer <token>».")
	}
	return strings.TrimSpace(token), nil
}
