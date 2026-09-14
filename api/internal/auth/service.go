package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// ErrEmailTaken means the address is already registered.
var ErrEmailTaken = errors.New("auth: e-mail is already registered")

var (
	// ErrDeviceNotTrusted covers every way a PIN login can fail before the PIN
	// itself is even checked: no device cookie, an unknown or expired one, or
	// one just revoked for failing too many times. All of them mean the same
	// thing to the client — fall back to e-mail and password — so none of them
	// is worth telling apart from the others.
	ErrDeviceNotTrusted = errors.New("auth: device is not trusted")

	// ErrPinNotSet means a device cannot be enabled for PIN login because the
	// account has none yet.
	ErrPinNotSet = errors.New("auth: pin is not set")
)

// maxPinFailures is how many wrong PINs a trusted device tolerates before its
// trust is revoked outright. A PIN is only six digits, so unlike a password
// there is no point warning and continuing to allow guesses forever — the
// device itself is the second factor, and a device that keeps failing is
// either not the account holder's or no longer in their hands.
const maxPinFailures = 5

// Session is what a successful authentication produces.
//
// The refresh token is returned so the transport can put it in an HttpOnly
// cookie; it is never part of a JSON body, which is what keeps it out of reach
// of any script running on the page.
type Session struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	User             store.User
}

// Service issues and ends sessions, and registers new customers.
type Service struct {
	db         *store.DB
	accounts   *accounts.Service
	categories *categories.Service
	tokens     *TokenIssuer
	cfg        config.AuthConfig
	equalise   timingEqualiser
	now        func() time.Time
}

func NewService(db *store.DB, accts *accounts.Service, cats *categories.Service, cfg config.AuthConfig) (*Service, error) {
	equalise, err := newTimingEqualiser(cfg.BcryptCost)
	if err != nil {
		return nil, err
	}
	return &Service{
		db:         db,
		accounts:   accts,
		categories: cats,
		tokens:     NewTokenIssuer(cfg.JWTSecret, cfg.AccessTTL),
		cfg:        cfg,
		equalise:   equalise,
		now:        time.Now,
	}, nil
}

// Tokens exposes the issuer for the authentication middleware.
func (s *Service) Tokens() *TokenIssuer { return s.tokens }

// RegisterInput is a validated registration request.
type RegisterInput struct {
	Email       string
	Password    string
	FullName    string
	AccountKind ledger.AccountKind
}

// Register creates a user together with their first bank account.
//
// Both happen in one PostgreSQL transaction because a customer who exists but
// has no account cannot do anything, and would have no way to get one — the
// registration endpoint is the only thing that opens the first account. The
// ledger account is created inside the transaction too; see accounts.Open for
// why that ordering is the safe one.
func (s *Service) Register(ctx context.Context, in RegisterInput) (Session, error) {
	email := NormaliseEmail(in.Email)

	hash, err := HashPassword(in.Password, s.cfg.BcryptCost)
	if err != nil {
		return Session{}, err
	}

	userID := uuid.New()
	var user store.User

	err = s.db.InTx(ctx, func(q *store.Queries) error {
		user, err = q.CreateUser(ctx, store.User{
			ID:           userID,
			Email:        email,
			PasswordHash: hash,
			FullName:     strings.TrimSpace(in.FullName),
		})
		if err != nil {
			if store.IsConstraint(err, store.UsersEmailConstraint) {
				return fmt.Errorf("%w: %s", ErrEmailTaken, email)
			}
			return err
		}

		// No alias on the account somebody registers with: they have not been asked
		// for one and inventing a name on their behalf would be putting words in
		// their mouth. The interface labels it by its type until they say otherwise.
		if _, err := s.accounts.Open(ctx, q, userID, in.AccountKind, ""); err != nil {
			return err
		}
		// Seeded in the same transaction for the same reason the first account is:
		// a customer who exists but cannot categorise a single transaction is not a
		// state worth allowing even momentarily.
		if err := s.categories.SeedDefaults(ctx, q, userID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}

	logging.FromContext(ctx).Info("user registered", "user_id", userID)
	return s.issue(ctx, user)
}

// Login exchanges credentials for a session.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	user, err := s.db.Q().UserByEmail(ctx, NormaliseEmail(email))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Spend the same bcrypt time as a real check before rejecting, so
			// the response time does not reveal whether the address exists.
			s.equalise.equalise(password)
			return Session{}, ErrInvalidCredentials
		}
		return Session{}, err
	}

	if err := VerifyPassword(user.PasswordHash, password); err != nil {
		return Session{}, err
	}
	return s.issue(ctx, user)
}

// Refresh rotates a refresh token and issues a new session.
//
// Rotation is single-use: the presented token is revoked as it is read, in one
// statement, so a token that is replayed — the shape a stolen one takes — finds
// nothing to consume and fails.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	if refreshToken == "" {
		return Session{}, ErrTokenInvalid
	}

	userID, err := s.db.Q().ConsumeRefreshToken(ctx, HashRefreshToken(refreshToken), s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Unknown, expired, revoked or already used all look the same here,
			// and all mean the same thing to the client: log in again.
			return Session{}, ErrTokenInvalid
		}
		return Session{}, err
	}

	user, err := s.db.Q().UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Session{}, ErrTokenInvalid
		}
		return Session{}, err
	}
	return s.issue(ctx, user)
}

// Logout revokes a refresh token. It is idempotent and never reports that the
// token was unknown.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.db.Q().RevokeRefreshToken(ctx, HashRefreshToken(refreshToken), s.now())
}

// Me loads the authenticated user.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (store.User, error) {
	return s.db.Q().UserByID(ctx, userID)
}

// SetPin creates or changes the PIN a trusted device can unlock a session
// with. The current password is required, the same as it would be to change
// the password itself: a PIN is a second way into the account, so opening
// that door needs the same proof of identity as the door it stands beside.
func (s *Service) SetPin(ctx context.Context, userID uuid.UUID, currentPassword, pin string) error {
	user, err := s.db.Q().UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := VerifyPassword(user.PasswordHash, currentPassword); err != nil {
		return err
	}

	hash, err := HashPin(pin, s.cfg.BcryptCost)
	if err != nil {
		return err
	}
	return s.db.Q().SetUserPin(ctx, userID, &hash)
}

// RemovePin deletes the PIN and, with it, every device trusted to use one. A
// device trusted for a PIN that no longer exists is not a state worth
// allowing even momentarily — see Register for the same reasoning applied to
// a customer's first account.
func (s *Service) RemovePin(ctx context.Context, userID uuid.UUID, currentPassword string) error {
	user, err := s.db.Q().UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := VerifyPassword(user.PasswordHash, currentPassword); err != nil {
		return err
	}

	return s.db.InTx(ctx, func(q *store.Queries) error {
		if err := q.SetUserPin(ctx, userID, nil); err != nil {
			return err
		}
		return q.RevokeUserTrustedDevices(ctx, userID)
	})
}

// EnableDeviceForPin trusts the device the caller is currently using: from now
// on, presenting the returned token together with the right PIN is enough to
// sign in from it. It refuses to run before a PIN exists — trusting a device
// for a credential that is not there yet would just be a device nothing can
// ever unlock with.
func (s *Service) EnableDeviceForPin(ctx context.Context, userID uuid.UUID) (deviceToken string, expiresAt time.Time, err error) {
	user, err := s.db.Q().UserByID(ctx, userID)
	if err != nil {
		return "", time.Time{}, err
	}
	if user.PinHash == nil {
		return "", time.Time{}, ErrPinNotSet
	}

	token, hash, err := NewDeviceToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = s.now().Add(s.cfg.DeviceTTL)

	if err := s.db.Q().CreateTrustedDevice(ctx, userID, hash, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// DisableDeviceForPin revokes the trust of the device the caller is currently
// using. Idempotent, like Logout: a customer disabling quick access twice, or
// from a device that was never enabled, still ends up in the state they
// asked for.
func (s *Service) DisableDeviceForPin(ctx context.Context, userID uuid.UUID, deviceToken string) error {
	if deviceToken == "" {
		return nil
	}
	return s.db.Q().RevokeTrustedDevice(ctx, HashDeviceToken(deviceToken), userID)
}

// DeviceHasPin reports whether the device the caller is currently using is
// trusted for PIN login, for the Seguridad screen to render its own state
// without keeping a separate flag anywhere.
func (s *Service) DeviceHasPin(ctx context.Context, userID uuid.UUID, deviceToken string) (bool, error) {
	if deviceToken == "" {
		return false, nil
	}
	device, err := s.db.Q().TrustedDeviceByHash(ctx, HashDeviceToken(deviceToken), s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return device.UserID == userID, nil
}

// DeviceStatus reports whether a device is trusted, and whose it is, for the
// sign-in screen to greet the right customer by name before they have typed
// anything. It never establishes a session by itself.
func (s *Service) DeviceStatus(ctx context.Context, deviceToken string) (fullName string, ok bool, err error) {
	if deviceToken == "" {
		return "", false, nil
	}

	device, err := s.db.Q().TrustedDeviceByHash(ctx, HashDeviceToken(deviceToken), s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}

	user, err := s.db.Q().UserByID(ctx, device.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return user.FullName, true, nil
}

// LoginWithPin exchanges a trusted device's token and PIN for a session, the
// PIN equivalent of Login.
//
// The device token is checked first and the PIN only against the one user it
// names: unlike Login, there is no e-mail to look up and therefore no
// enumeration question to equalise the timing of. A device with nothing
// trusted for it never reaches a PIN comparison at all.
func (s *Service) LoginWithPin(ctx context.Context, deviceToken, pin string) (Session, error) {
	if deviceToken == "" {
		return Session{}, ErrDeviceNotTrusted
	}

	device, err := s.db.Q().TrustedDeviceByHash(ctx, HashDeviceToken(deviceToken), s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Session{}, ErrDeviceNotTrusted
		}
		return Session{}, err
	}

	user, err := s.db.Q().UserByID(ctx, device.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Session{}, ErrDeviceNotTrusted
		}
		return Session{}, err
	}

	if user.PinHash == nil {
		// The PIN was removed after this device was trusted (RemovePin should
		// have revoked it already; this is the belt to that braces). Either way
		// the device has nothing left to prove with.
		_ = s.db.Q().RevokeTrustedDeviceByID(ctx, device.ID)
		return Session{}, ErrDeviceNotTrusted
	}

	if err := VerifyPin(*user.PinHash, pin); err != nil {
		attempts, incErr := s.db.Q().IncrementTrustedDeviceFailures(ctx, device.ID)
		if incErr != nil {
			return Session{}, incErr
		}
		if attempts >= maxPinFailures {
			if err := s.db.Q().RevokeTrustedDeviceByID(ctx, device.ID); err != nil {
				return Session{}, err
			}
			return Session{}, ErrDeviceNotTrusted
		}
		return Session{}, err
	}

	if err := s.db.Q().TouchTrustedDevice(ctx, device.ID, s.now()); err != nil {
		return Session{}, err
	}
	return s.issue(ctx, user)
}

// issue mints an access token and a fresh refresh token for a user.
func (s *Service) issue(ctx context.Context, user store.User) (Session, error) {
	now := s.now()

	access, err := s.tokens.IssueAccessToken(user.ID, now)
	if err != nil {
		return Session{}, err
	}

	refresh, refreshHash, err := NewRefreshToken()
	if err != nil {
		return Session{}, err
	}
	refreshExpiry := now.Add(s.cfg.RefreshTTL)

	if err := s.db.Q().StoreRefreshToken(ctx, refreshHash, user.ID, refreshExpiry); err != nil {
		return Session{}, err
	}

	return Session{
		AccessToken:      access,
		AccessExpiresAt:  now.Add(s.cfg.AccessTTL),
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExpiry,
		User:             user,
	}, nil
}

// PruneSessions deletes refresh tokens and trusted devices that can no longer
// authenticate anything. Called on a timer by the API so neither table grows
// forever.
func (s *Service) PruneSessions(ctx context.Context) (int64, error) {
	removed, err := s.db.Q().DeleteExpiredRefreshTokens(ctx, s.now())
	if err != nil {
		return removed, err
	}
	devicesRemoved, err := s.db.Q().DeleteExpiredTrustedDevices(ctx, s.now())
	return removed + devicesRemoved, err
}

// NormaliseEmail trims and lower-cases an address.
//
// The column is CITEXT, so comparison is already case-insensitive; normalising
// on the way in just means the address is stored the way the customer will see
// it echoed back, rather than however they happened to type it.
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
