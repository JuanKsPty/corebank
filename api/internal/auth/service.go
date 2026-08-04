package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

// ErrEmailTaken means the address is already registered.
var ErrEmailTaken = errors.New("auth: e-mail is already registered")

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
	db       *store.DB
	accounts *accounts.Service
	tokens   *TokenIssuer
	cfg      config.AuthConfig
	equalise timingEqualiser
	now      func() time.Time
}

func NewService(db *store.DB, accts *accounts.Service, cfg config.AuthConfig) (*Service, error) {
	equalise, err := newTimingEqualiser(cfg.BcryptCost)
	if err != nil {
		return nil, err
	}
	return &Service{
		db:       db,
		accounts: accts,
		tokens:   NewTokenIssuer(cfg.JWTSecret, cfg.AccessTTL),
		cfg:      cfg,
		equalise: equalise,
		now:      time.Now,
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

		if _, err := s.accounts.Open(ctx, q, userID, in.AccountKind); err != nil {
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

// PruneSessions deletes refresh tokens that can no longer authenticate
// anything. Called on a timer by the API so the table does not grow forever.
func (s *Service) PruneSessions(ctx context.Context) (int64, error) {
	return s.db.Q().DeleteExpiredRefreshTokens(ctx, s.now())
}

// NormaliseEmail trims and lower-cases an address.
//
// The column is CITEXT, so comparison is already case-insensitive; normalising
// on the way in just means the address is stored the way the customer will see
// it echoed back, rather than however they happened to type it.
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
