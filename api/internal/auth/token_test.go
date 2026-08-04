package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func newTestIssuer(t *testing.T) *TokenIssuer {
	t.Helper()
	return NewTokenIssuer([]byte(strings.Repeat("s", 32)), 15*time.Minute)
}

func TestAccessTokenRoundTrip(t *testing.T) {
	issuer := newTestIssuer(t)
	userID := uuid.New()
	now := time.Now()

	token, err := issuer.IssueAccessToken(userID, now)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	got, err := issuer.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if got != userID {
		t.Errorf("subject = %s, want %s", got, userID)
	}
}

func TestExpiredTokenIsReportedAsExpired(t *testing.T) {
	// The distinction matters to the client: an expired token means "refresh",
	// while an invalid one means "log in again". Collapsing them would log people
	// out every fifteen minutes.
	issuer := newTestIssuer(t)

	token, err := issuer.IssueAccessToken(uuid.New(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	_, err = issuer.ParseAccessToken(token)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("ParseAccessToken on an expired token = %v, want ErrTokenExpired", err)
	}
}

func TestTokenSignedWithAnotherSecretIsRejected(t *testing.T) {
	issuer := newTestIssuer(t)
	other := NewTokenIssuer([]byte(strings.Repeat("x", 32)), 15*time.Minute)

	token, err := other.IssueAccessToken(uuid.New(), time.Now())
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	if _, err := issuer.ParseAccessToken(token); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a foreign signature gave %v, want ErrTokenInvalid", err)
	}
}

func TestUnsignedTokenIsRejected(t *testing.T) {
	// The classic JWT attack: strip the signature and set alg to "none". The
	// parser is pinned to HS256, so the token's own header cannot choose how it
	// is verified.
	issuer := newTestIssuer(t)

	claims := jwt.RegisteredClaims{
		Subject:   uuid.NewString(),
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{tokenAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building the unsigned token: %v", err)
	}

	if _, err := issuer.ParseAccessToken(unsigned); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("an alg=none token gave %v, want ErrTokenInvalid", err)
	}
}

func TestTokenForAnotherAudienceOrIssuerIsRejected(t *testing.T) {
	issuer := newTestIssuer(t)
	secret := []byte(strings.Repeat("s", 32))

	for name, claims := range map[string]jwt.RegisteredClaims{
		"foreign issuer": {
			Subject: uuid.NewString(), Issuer: "somebody-else",
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		"foreign audience": {
			Subject: uuid.NewString(), Issuer: tokenIssuer,
			Audience:  jwt.ClaimStrings{"some-other-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		"no expiry": {
			Subject: uuid.NewString(), Issuer: tokenIssuer,
			Audience: jwt.ClaimStrings{tokenAudience},
		},
	} {
		t.Run(name, func(t *testing.T) {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
			if err != nil {
				t.Fatalf("signing: %v", err)
			}
			if _, err := issuer.ParseAccessToken(token); err == nil {
				t.Error("the token was accepted")
			}
		})
	}
}

func TestSubjectMustBeAUUID(t *testing.T) {
	issuer := newTestIssuer(t)

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "administrator",
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{tokenAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	if _, err := issuer.ParseAccessToken(token); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a non-uuid subject gave %v, want ErrTokenInvalid", err)
	}
}

func TestRefreshTokensAreUniqueAndStoredHashed(t *testing.T) {
	first, firstHash, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	second, secondHash, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}

	if first == second {
		t.Fatal("two refresh tokens came out identical")
	}
	if firstHash == secondHash {
		t.Error("two distinct tokens hashed to the same value")
	}
	if strings.Contains(firstHash, first) || firstHash == first {
		t.Error("the stored value contains the usable token")
	}
	if HashRefreshToken(first) != firstHash {
		t.Error("hashing the same token twice gave different results")
	}
	// 32 random bytes, base64url without padding.
	if len(first) != 43 {
		t.Errorf("token is %d characters, want 43 (256 bits)", len(first))
	}
}
