// Package auth owns identity: password hashing, access and refresh tokens, the
// endpoints that issue them and the middleware that consumes them.
//
// The whole package is written so that no code path outside it ever sees a
// plaintext password or a raw refresh token beyond the moment it is used.
package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt hashes at most the first 72 bytes of its input and silently ignores the
// rest, so "correct horse battery staple …" past that length would be
// interchangeable with any other string sharing the prefix. Rejecting longer
// passwords outright is the honest behaviour: the alternative is a password
// field where the last characters do not matter and nobody is told.
const (
	MinPasswordLength = 8
	MaxPasswordBytes  = 72
)

var (
	// ErrInvalidCredentials is returned for both a wrong password and an unknown
	// e-mail. Distinguishing them would turn the login form into an account
	// enumeration oracle.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")

	ErrPasswordTooShort = fmt.Errorf("auth: password must be at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("auth: password must be at most %d bytes", MaxPasswordBytes)
	ErrPasswordTooWeak  = errors.New("auth: password must contain a letter and a digit")
)

// HashPassword validates a plaintext password against the policy and hashes it.
func HashPassword(plain string, cost int) (string, error) {
	if err := CheckPasswordPolicy(plain); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", fmt.Errorf("auth: hashing password: %w", err)
	}
	return string(hash), nil
}

// CheckPasswordPolicy reports whether a password is acceptable.
//
// The rules are deliberately modest — length plus one letter and one digit.
// Elaborate composition rules push people towards "Password1!" and are worse in
// practice than a length floor; the length floor is the part that matters.
func CheckPasswordPolicy(plain string) error {
	if utf8.RuneCountInString(plain) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(plain) > MaxPasswordBytes {
		return ErrPasswordTooLong
	}

	var hasLetter, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return ErrPasswordTooWeak
	}
	return nil
}

// VerifyPassword checks a plaintext password against a stored hash, returning
// ErrInvalidCredentials on any mismatch.
func VerifyPassword(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return ErrInvalidCredentials
		}
		// A malformed hash in the database is an operational fault, not a wrong
		// password, and must not be reported as one — it would look like a user
		// error while an account is quietly unusable.
		return fmt.Errorf("auth: comparing password hash: %w", err)
	}
	return nil
}

// timingEqualiser makes a login attempt for an unknown e-mail cost the same as
// one for a real account.
//
// Without it the difference between a ~60 ms bcrypt comparison and an immediate
// "no such user" rejection turns the login form into an account-enumeration
// oracle, measurable over the network. The decoy hash therefore has to be built
// at the *same cost* as the real ones — a cheap decoy would leak the same
// information it is meant to hide.
type timingEqualiser struct{ decoy []byte }

func newTimingEqualiser(cost int) (timingEqualiser, error) {
	decoy, err := bcrypt.GenerateFromPassword([]byte("corebank-timing-decoy"), cost)
	if err != nil {
		return timingEqualiser{}, fmt.Errorf("auth: generating the timing decoy: %w", err)
	}
	return timingEqualiser{decoy: decoy}, nil
}

// equalise performs a full password comparison whose outcome is discarded.
func (t timingEqualiser) equalise(plain string) {
	err := bcrypt.CompareHashAndPassword(t.decoy, []byte(plain))
	// The decoy never matches. Feeding the outcome through a constant-time
	// comparison keeps a compiler from deciding the call is dead code.
	_ = subtle.ConstantTimeCompare([]byte(strconv.FormatBool(err == nil)), []byte("false"))
}
