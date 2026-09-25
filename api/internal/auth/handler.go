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

// deviceCookieName is the cookie holding the device token that unlocks PIN
// login. Separate from the refresh cookie because the two claims are
// different in kind: a refresh token means "this browser has a live
// session", a device token means only "this browser may try a PIN" — nothing
// that reads one should ever be able to mistake it for the other.
const deviceCookieName = "corebank_device"

// deviceCookiePath scopes the device cookie the same way the refresh cookie
// is scoped, and for the same reason: every endpoint that reads it lives
// under /api/auth (login-pin, device, and the security/pin endpoints, which
// share this router precisely so this cookie reaches them).
const deviceCookiePath = "/api/auth"

// Handler serves the authentication endpoints.
type Handler struct {
	svc *Service
	// secureCookies marks the refresh and device cookies Secure. Off in
	// development because the dev server is plain HTTP and a Secure cookie
	// would simply never be sent, which looks like a broken login rather than a
	// misconfiguration.
	secureCookies   bool
	loginLimiter    *httpx.RateLimiter
	pinLoginLimiter *httpx.RateLimiter
}

func NewHandler(svc *Service, production bool, loginLimiter, pinLoginLimiter *httpx.RateLimiter) *Handler {
	return &Handler{
		svc:             svc,
		secureCookies:   production,
		loginLimiter:    loginLimiter,
		pinLoginLimiter: pinLoginLimiter,
	}
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

	// login-pin gets its own limiter: a six-digit PIN is a much smaller keyspace
	// than a password, even though a device token is also required to try one.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RateLimit(h.pinLoginLimiter,
			"Demasiados intentos. Espera un momento antes de volver a intentarlo."))
		r.Post("/login-pin", h.loginPin)
	})

	// Public and unthrottled: it only reads a cookie, and there is nothing in
	// the request for a guesser to vary.
	r.Get("/device", h.deviceStatus)

	r.Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)

	// Managing the PIN itself requires an existing session. This group lives on
	// the same router as login-pin and device — rather than beside /me in the
	// top-level authenticated group in server.go — specifically so the device
	// cookie, scoped to /api/auth, reaches these endpoints too.
	r.Group(func(r chi.Router) {
		r.Use(Require(h.svc.Tokens()))
		r.Post("/security/pin", h.setPin)
		r.Delete("/security/pin", h.removePin)
		r.Post("/security/pin/device", h.enableDevice)
		r.Delete("/security/pin/device", h.disableDevice)
		r.Get("/security/status", h.securityStatus)
	})

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

	view := accounts.NewListView(list)
	httpx.JSON(w, r, http.StatusOK, meResponse{
		User:     newUserResponse(user),
		Accounts: view.Accounts,
		NetWorth: view.NetWorth,
	})
}

type meResponse struct {
	User     userResponse          `json:"user"`
	Accounts []accounts.View       `json:"accounts"`
	NetWorth accounts.NetWorthView `json:"net_worth"`
}

// --- request and response shapes -------------------------------------------

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
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

	if len(problems) > 0 {
		return RegisterInput{}, problems
	}
	return RegisterInput{
		Email:    email,
		Password: req.Password,
		FullName: fullName,
	}, nil
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginPinRequest struct {
	Pin string `json:"pin"`
}

// deviceStatusResponse tells the sign-in screen whether this browser may skip
// straight to a PIN, and whose name to greet before anything is typed.
type deviceStatusResponse struct {
	Trusted  bool   `json:"trusted"`
	FullName string `json:"full_name,omitempty"`
}

// securityStatusResponse is what the Seguridad screen renders itself from: a
// PIN either exists or does not, and this device either is or is not the one
// it can be unlocked from.
type securityStatusResponse struct {
	HasPin        bool `json:"has_pin"`
	DeviceEnabled bool `json:"device_enabled"`
}

type setPinRequest struct {
	CurrentPassword string `json:"current_password"`
	Pin             string `json:"pin"`
}

type removePinRequest struct {
	CurrentPassword string `json:"current_password"`
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

func (h *Handler) loginPin(w http.ResponseWriter, r *http.Request) {
	var req loginPinRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	session, err := h.svc.LoginWithPin(r.Context(), h.deviceCookieValue(r), req.Pin)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceNotTrusted):
			// Its own code, distinct from "pin_incorrect" below: the two mean
			// different things to the client. A wrong PIN is worth retrying: the
			// device is still trusted, only the digits were wrong. This one is
			// not — the device itself is dead (unknown, expired, or just revoked
			// for failing too many times) — so the cookie is cleared and the
			// client should fall back to e-mail and password rather than show
			// the PIN box again.
			h.clearDeviceCookie(w)
			httpx.Fail(w, r, (&httpx.Error{
				Status:  http.StatusUnauthorized,
				Code:    "device_not_trusted",
				Message: "No hay un acceso rápido activo en este dispositivo. Inicia sesión con tu correo y contraseña.",
			}).WithCause(err))
		case errors.Is(err, ErrPinIncorrect):
			httpx.Fail(w, r, (&httpx.Error{
				Status:  http.StatusUnauthorized,
				Code:    "pin_incorrect",
				Message: "El PIN no es correcto.",
			}).WithCause(err))
		default:
			httpx.Fail(w, r, err)
		}
		return
	}

	h.writeSession(w, r, session, http.StatusOK)
}

// deviceStatus lets the sign-in screen ask, before anything is typed, whether
// this browser can skip straight to a PIN. Public: it only reads a cookie
// nothing outside this device could have, so there is no identity to protect
// by requiring one.
func (h *Handler) deviceStatus(w http.ResponseWriter, r *http.Request) {
	fullName, trusted, err := h.svc.DeviceStatus(r.Context(), h.deviceCookieValue(r))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, deviceStatusResponse{Trusted: trusted, FullName: fullName})
}

func (h *Handler) setPin(w http.ResponseWriter, r *http.Request) {
	userID := identity.MustFromContext(r.Context())

	var req setPinRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.svc.SetPin(r.Context(), userID, req.CurrentPassword, req.Pin); err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			httpx.Fail(w, r, httpx.Invalid(map[string]string{
				"current_password": "La contraseña actual no es correcta.",
			}).WithCause(err))
		case errors.Is(err, ErrPinInvalid):
			httpx.Fail(w, r, httpx.Invalid(map[string]string{
				"pin": "El PIN debe tener exactamente 6 dígitos.",
			}).WithCause(err))
		default:
			httpx.Fail(w, r, err)
		}
		return
	}

	httpx.NoContent(w)
}

func (h *Handler) removePin(w http.ResponseWriter, r *http.Request) {
	userID := identity.MustFromContext(r.Context())

	var req removePinRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.svc.RemovePin(r.Context(), userID, req.CurrentPassword); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.Fail(w, r, httpx.Invalid(map[string]string{
				"current_password": "La contraseña actual no es correcta.",
			}).WithCause(err))
			return
		}
		httpx.Fail(w, r, err)
		return
	}

	// Removing the PIN revokes every trusted device server-side; clearing this
	// one's cookie too means the browser that just did it stops offering a PIN
	// screen on its own next visit, rather than failing once against a device
	// the server already forgot.
	h.clearDeviceCookie(w)
	httpx.NoContent(w)
}

func (h *Handler) enableDevice(w http.ResponseWriter, r *http.Request) {
	userID := identity.MustFromContext(r.Context())

	token, expiresAt, err := h.svc.EnableDeviceForPin(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrPinNotSet) {
			httpx.Fail(w, r, httpx.Conflict("pin_not_set", "Primero configura un PIN.").WithCause(err))
			return
		}
		httpx.Fail(w, r, err)
		return
	}

	h.writeDeviceCookie(w, token, expiresAt)
	httpx.JSON(w, r, http.StatusCreated, securityStatusResponse{HasPin: true, DeviceEnabled: true})
}

func (h *Handler) disableDevice(w http.ResponseWriter, r *http.Request) {
	userID := identity.MustFromContext(r.Context())

	if err := h.svc.DisableDeviceForPin(r.Context(), userID, h.deviceCookieValue(r)); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.clearDeviceCookie(w)
	httpx.NoContent(w)
}

func (h *Handler) securityStatus(w http.ResponseWriter, r *http.Request) {
	userID := identity.MustFromContext(r.Context())

	user, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	deviceEnabled, err := h.svc.DeviceHasPin(r.Context(), userID, h.deviceCookieValue(r))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, r, http.StatusOK, securityStatusResponse{
		HasPin:        user.PinHash != nil,
		DeviceEnabled: deviceEnabled,
	})
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

// deviceCookieValue reads the raw device token, or "" if none is present —
// callers treat an absent cookie exactly like an invalid one, so there is no
// error to return here.
func (h *Handler) deviceCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(deviceCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (h *Handler) writeDeviceCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookieName,
		Value:    token,
		Path:     deviceCookiePath,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookies,
		// Lax for the same reason the refresh cookie is: it is only ever read by
		// this app's own POSTs, and Strict would drop it when the customer
		// arrives from an external link.
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearDeviceCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookieName,
		Value:    "",
		Path:     deviceCookiePath,
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
