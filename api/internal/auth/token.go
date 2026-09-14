package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// tokenIssuer identifies this API in the `iss` claim, so a token minted by a
	// different service with the same secret is still rejected.
	tokenIssuer = "corebank"
	// tokenAudience narrows a token to this API's own endpoints.
	tokenAudience = "corebank-api"
)

var (
	ErrTokenInvalid = errors.New("auth: token is invalid")
	ErrTokenExpired = errors.New("auth: token has expired")
)

// TokenIssuer mints and verifies access tokens.
//
// Access tokens are short-lived JWTs so that authorising a request needs no
// database round trip. The price of that is that they cannot be revoked before
// they expire, which is exactly why they last minutes and the long-lived
// credential is the refresh token — that one *is* stored, and revoking it ends
// the session.
type TokenIssuer struct {
	secret    []byte
	accessTTL time.Duration
}

func NewTokenIssuer(secret []byte, accessTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: secret, accessTTL: accessTTL}
}

// AccessTTL is how long a freshly issued access token remains valid; the client
// needs it to schedule a refresh before expiry rather than after a failure.
func (t *TokenIssuer) AccessTTL() time.Duration { return t.accessTTL }

// IssueAccessToken mints a signed access token for a user.
func (t *TokenIssuer) IssueAccessToken(userID uuid.UUID, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{tokenAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(t.accessTTL)),
		ID:        uuid.NewString(),
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("auth: signing access token: %w", err)
	}
	return signed, nil
}

// ParseAccessToken verifies a token and returns the user it belongs to.
func (t *TokenIssuer) ParseAccessToken(raw string) (uuid.UUID, error) {
	claims := &jwt.RegisteredClaims{}

	_, err := jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		// Pinning the algorithm is what closes the "alg: none" and
		// HS256-verified-as-RS256 confusion classes: the parser is told what to
		// expect instead of trusting the token's own header.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			// Told apart from a malformed token on purpose: the client's correct
			// response to an expired token is to refresh, not to log in again.
			return uuid.Nil, ErrTokenExpired
		}
		return uuid.Nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: subject is not a uuid", ErrTokenInvalid)
	}
	return userID, nil
}

// opaqueTokenBytes is 32 bytes — 256 bits — of entropy, which puts guessing one
// out of reach regardless of how fast the check is. Both the refresh token and
// the device token are this shape: a random string with nothing about it to
// verify except "does the database have this hash".
const opaqueTokenBytes = 32

// newOpaqueToken returns a fresh random token and its stored hash. Refresh
// tokens and device tokens are both built from this: they need to be
// revocable, which means a server-side record either way, and once there is a
// record, a self-describing token (a JWT, say) buys nothing and only adds a
// second format to get wrong.
func newOpaqueToken() (token string, hash string, err error) {
	raw := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: generating token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashOpaqueToken(token), nil
}

// hashOpaqueToken is the value stored in the database for a refresh or device
// token.
//
// SHA-256 rather than bcrypt, deliberately. bcrypt's work factor exists to slow
// down guessing of low-entropy, human-chosen secrets; a 256-bit random value has
// nothing to guess, so the cost would buy no security and would be paid on every
// single refresh or PIN unlock. What matters here is that the database never
// holds the usable credential — a leaked dump cannot be replayed as a set of
// live sessions or trusted devices.
func hashOpaqueToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewRefreshToken returns a fresh opaque token and the hash to store for it.
func NewRefreshToken() (token string, hash string, err error) { return newOpaqueToken() }

// HashRefreshToken is the value stored in the database for a refresh token.
func HashRefreshToken(token string) string { return hashOpaqueToken(token) }

// NewDeviceToken returns a fresh opaque token and the hash to store for it.
//
// A separate name from NewRefreshToken even though the shape is identical: a
// device token proves "this browser may unlock a session with a PIN", which is
// a weaker claim than a refresh token's "this browser has a live session", and
// the two must never be interchangeable in code that reads either name.
func NewDeviceToken() (token string, hash string, err error) { return newOpaqueToken() }

// HashDeviceToken is the value stored in the database for a device token.
func HashDeviceToken(token string) string { return hashOpaqueToken(token) }
