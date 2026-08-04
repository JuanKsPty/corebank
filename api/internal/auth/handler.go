package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/identity"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// refreshCookieName is the cookie holding the refresh token.
//
// It lives in a cookie rather than in the JSON body because the body ends up in
// JavaScript, and from there in whatever storage the page chooses. An HttpOnly
// cookie is unreachable from script, so an injected script cannot steal the
// long-lived credential — the access token it *can* reach expires in minutes.
const refreshCookieName = "corebank_refresh"

// refreshCookiePath scopes the cookie to the endpoints that consume it, so it is
// not attached to every API call.
const refreshCookiePath = "/api/auth"

// Handler serves the authentication endpoints.
type Handler struct {
	svc *Service
	// secureCookies marks the refresh cookie Secure. Off in development because
	// the dev server is plain HTTP and a Secure cookie would simply never be
	// sent, which looks like a broken login rather than a misconfiguration.
	secureCookies bool
	loginLimiter  *httpx.RateLimiter
}

func NewHandler(svc *Service, production bool, loginLimiter *httpx.RateLimiter) *Handler {
	return &Handler{svc: svc, secureCookies: production, loginLimiter: loginLimiter}
}

// Routes returns the /api/auth subrouter.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()

	// Registration and login are the two unauthenticated endpoints where
	// guessing or mass-creating pays off, so both are rate limited per address.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RateLimit(h.loginLimiter,
			"Demasiados intentos. Espera un momento antes de volver a intentarlo."))
		r.Post("/register", h.register)
		r.Post("/login", h.login)
	})

	r.Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)
	return r
}

// Me handles GET /api/me: the profile together with the accounts and the
// consolidated balance, so the app renders its shell from one request instead of
// three. It must be mounted behind Require.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID := identity.MustFromContext(r.Context())

	user, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	list, err := h.svc.accounts.List(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, r, http.StatusOK, meResponse{
		User:     newUserResponse(user),
		Accounts: accounts.NewViews(list),
		Total:    h.svc.accounts.Total(r.Context(), list).Amount(),
	})
}

type meResponse struct {
	User     userResponse    `json:"user"`
	Accounts []accounts.View `json:"accounts"`
	Total    money.Amount    `json:"total_available"`
}

// --- request and response shapes -------------------------------------------

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	FullName    string `json:"full_name"`
	AccountType string `json:"account_type"`
}

// validate returns per-field messages for the form to display inline.
//
// Written by hand rather than driven by struct tags: there are few enough
// endpoints that the tags would not save code, and this way each message says
// what the customer should actually do instead of a generated "field failed on
// the 'min' tag".
func (req registerRequest) validate() (RegisterInput, map[string]string) {
	problems := map[string]string{}

	email := NormaliseEmail(req.Email)
	switch {
	case email == "":
		problems["email"] = "El correo es obligatorio."
	case !looksLikeEmail(email):
		problems["email"] = "El correo no tiene un formato válido."
	case len(email) > 254:
		problems["email"] = "El correo es demasiado largo."
	}

	if err := CheckPasswordPolicy(req.Password); err != nil {
		switch {
		case errors.Is(err, ErrPasswordTooShort):
			problems["password"] = "La contraseña debe tener al menos 8 caracteres."
		case errors.Is(err, ErrPasswordTooLong):
			problems["password"] = "La contraseña es demasiado larga (máximo 72 bytes)."
		default:
			problems["password"] = "La contraseña debe incluir al menos una letra y un número."
		}
	}

	fullName := strings.TrimSpace(req.FullName)
	switch {
	case fullName == "":
		problems["full_name"] = "El nombre es obligatorio."
	case len(fullName) > 120:
		problems["full_name"] = "El nombre es demasiado largo."
	}

	// The first account defaults to a savings account, which is what most of the
	// dataset uses, so the field is optional on the form.
	kind := ledger.KindSavings
	if req.AccountType != "" {
		parsed, err := ledger.ParseAccountKind(req.AccountType)
		if err != nil {
			problems["account_type"] = "El tipo de cuenta debe ser savings, checking o investment."
		} else {
			kind = parsed
		}
	}

	if len(problems) > 0 {
		return RegisterInput{}, problems
	}
	return RegisterInput{
		Email:       email,
		Password:    req.Password,
		FullName:    fullName,
		AccountKind: kind,
	}, nil
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// sessionResponse is what an authenticated client receives.
//
// The refresh token is deliberately absent: it travels only in the HttpOnly
// cookie. expires_at lets the client schedule a refresh a little early instead
// of discovering the expiry through a failed request.
type sessionResponse struct {
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresAt   time.Time    `json:"expires_at"`
	User        userResponse `json:"user"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	CreatedAt time.Time `json:"created_at"`
}

func newUserResponse(u store.User) userResponse {
	return userResponse{
		ID:        u.ID.String(),
		Email:     u.Email,
		FullName:  u.FullName,
		CreatedAt: u.CreatedAt,
	}
}

// --- handlers ---------------------------------------------------------------

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	in, problems := req.validate()
	if problems != nil {
		httpx.Fail(w, r, httpx.Invalid(problems))
		return
	}

	session, err := h.svc.Register(r.Context(), in)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			// Reported per field so the form can point at the input. This does
			// reveal that an address is registered — unavoidable for a signup
			// form, and the alternative (accepting the registration silently) is
			// worse for the person who already owns the account.
			httpx.Fail(w, r, httpx.Invalid(map[string]string{
				"email": "Este correo ya está registrado.",
			}).WithCause(err))
			return
		}
		httpx.Fail(w, r, err)
		return
	}

	h.writeSession(w, r, session, http.StatusCreated)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	session, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.Fail(w, r, httpx.Unauthorized("El correo o la contraseña no son correctos.").WithCause(err))
			return
		}
		httpx.Fail(w, r, err)
		return
	}

	h.writeSession(w, r, session, http.StatusOK)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		httpx.Fail(w, r, httpx.Unauthorized("No hay una sesión que renovar.").WithCause(err))
		return
	}

	session, err := h.svc.Refresh(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, ErrTokenInvalid) {
			// Clear the cookie so the browser stops presenting a token that can
			// never work again.
			h.clearRefreshCookie(w)
			httpx.Fail(w, r, httpx.Unauthorized("Tu sesión expiró. Inicia sesión de nuevo.").WithCause(err))
			return
		}
		httpx.Fail(w, r, err)
		return
	}

	h.writeSession(w, r, session, http.StatusOK)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	// Always clear the cookie and always succeed: logging out must work even if
	// the session was already gone.
	h.clearRefreshCookie(w)
	httpx.NoContent(w)
}

// --- cookie handling --------------------------------------------------------

func (h *Handler) writeSession(w http.ResponseWriter, r *http.Request, s Session, status int) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    s.RefreshToken,
		Path:     refreshCookiePath,
		Expires:  s.RefreshExpiresAt,
		MaxAge:   int(time.Until(s.RefreshExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookies,
		// Lax rather than Strict: the cookie is only read by explicit POSTs from
		// the app's own pages, and Strict would drop it when the customer
		// arrives from an external link, silently logging them out.
		SameSite: http.SameSiteLaxMode,
	})

	httpx.JSON(w, r, status, sessionResponse{
		AccessToken: s.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   s.AccessExpiresAt,
		User:        newUserResponse(s.User),
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// looksLikeEmail is a shape check, not a validity check.
//
// Nothing short of sending a message proves an address works, and the strict
// grammar accepts things no mail server would. So this rejects only the obvious
// mistakes and leaves the rest to delivery.
func looksLikeEmail(s string) bool {
	at := strings.LastIndex(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	local, domain := s[:at], s[at+1:]
	if strings.ContainsAny(s, " \t\r\n,;\"'\\") {
		return false
	}
	if len(local) > 64 {
		return false
	}
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1 && !strings.Contains(domain, "..")
}
