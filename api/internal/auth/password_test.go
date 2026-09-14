package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// testCost keeps the suite fast. Production cost is validated by config, not here.
const testCost = bcrypt.MinCost

func TestHashAndVerify(t *testing.T) {
	const password = "Secreta2026"

	hash, err := HashPassword(password, testCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("the hash contains the plaintext password")
	}
	if err := VerifyPassword(hash, password); err != nil {
		t.Errorf("VerifyPassword with the right password: %v", err)
	}
	if err := VerifyPassword(hash, password+"x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("VerifyPassword with the wrong password returned %v, want ErrInvalidCredentials", err)
	}
}

func TestHashIsSaltedPerPassword(t *testing.T) {
	// Two users with the same password must not share a hash, or a single
	// cracked hash would open every account using that password.
	a, err := HashPassword("Secreta2026", testCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := HashPassword("Secreta2026", testCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Error("identical passwords produced identical hashes, so they are not salted")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
		want     error
	}{
		{"ok", "Secreta2026", nil},
		{"exactly eight", "abcdefg1", nil},
		{"accents count as letters", "contraseña2026", nil},
		{"seven characters", "abcdef1", ErrPasswordTooShort},
		{"letters only", "abcdefghij", ErrPasswordTooWeak},
		{"digits only", "1234567890", ErrPasswordTooWeak},
		{"73 bytes", strings.Repeat("a", 72) + "1", ErrPasswordTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPasswordPolicy(tc.password)
			if !errors.Is(err, tc.want) {
				t.Errorf("CheckPasswordPolicy(%q) = %v, want %v", tc.password, err, tc.want)
			}
		})
	}
}

func TestPasswordLengthIsCountedInRunesButCappedInBytes(t *testing.T) {
	// The two limits measure different things on purpose. The minimum is about
	// how much the person typed, so it counts characters. The maximum is about
	// what bcrypt will actually read — 72 *bytes* — so a password of 30 accented
	// characters is long enough to pass the floor and short enough to be hashed
	// in full.
	const eightAccented = "ñññññññ1" // 8 runes, 15 bytes
	if err := CheckPasswordPolicy(eightAccented); err != nil {
		t.Errorf("an 8-character accented password was rejected: %v", err)
	}

	// 36 two-byte runes plus a digit is 73 bytes: over what bcrypt reads, so it
	// must be refused rather than silently truncated.
	tooLong := strings.Repeat("ñ", 36) + "1"
	if len(tooLong) <= MaxPasswordBytes {
		t.Fatalf("test fixture is %d bytes, expected more than %d", len(tooLong), MaxPasswordBytes)
	}
	if err := CheckPasswordPolicy(tooLong); !errors.Is(err, ErrPasswordTooLong) {
		t.Errorf("a %d-byte password gave %v, want ErrPasswordTooLong", len(tooLong), err)
	}
}

func TestVerifyDistinguishesACorruptHashFromAWrongPassword(t *testing.T) {
	// A garbled hash in the database means an account is unusable; reporting it
	// as "wrong password" would send the customer to reset a password that was
	// never the problem, and would hide the fault.
	err := VerifyPassword("not-a-bcrypt-hash", "Secreta2026")
	if err == nil {
		t.Fatal("a corrupt hash was accepted")
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Error("a corrupt hash was reported as invalid credentials")
	}
}

func TestHashAndVerifyPin(t *testing.T) {
	const pin = "204719"

	hash, err := HashPin(pin, testCost)
	if err != nil {
		t.Fatalf("HashPin: %v", err)
	}
	if strings.Contains(hash, pin) {
		t.Fatal("the hash contains the plaintext pin")
	}
	if err := VerifyPin(hash, pin); err != nil {
		t.Errorf("VerifyPin with the right pin: %v", err)
	}
	if err := VerifyPin(hash, "000000"); !errors.Is(err, ErrPinIncorrect) {
		t.Errorf("VerifyPin with the wrong pin returned %v, want ErrPinIncorrect", err)
	}
}

func TestPinPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		pin  string
		want error
	}{
		{"ok", "204719", nil},
		{"all zeros is still six digits", "000000", nil},
		{"five digits", "20471", ErrPinInvalid},
		{"seven digits", "2047199", ErrPinInvalid},
		{"contains a letter", "20471a", ErrPinInvalid},
		{"contains a space", "2047 9", ErrPinInvalid},
		{"empty", "", ErrPinInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPinPolicy(tc.pin)
			if !errors.Is(err, tc.want) {
				t.Errorf("CheckPinPolicy(%q) = %v, want %v", tc.pin, err, tc.want)
			}
		})
	}
}

func TestVerifyPinDistinguishesACorruptHashFromAWrongPin(t *testing.T) {
	err := VerifyPin("not-a-bcrypt-hash", "204719")
	if err == nil {
		t.Fatal("a corrupt hash was accepted")
	}
	if errors.Is(err, ErrPinIncorrect) {
		t.Error("a corrupt hash was reported as an incorrect pin")
	}
}

func TestTimingEqualiserCostsTheSameAsARealCheck(t *testing.T) {
	// The property that matters is that the decoy is hashed at the same cost as
	// a real password, since that is what makes the two paths take the same
	// time. Comparing wall-clock durations in a unit test would be flaky, so the
	// cost recorded inside the decoy hash is checked instead.
	const cost = 6

	eq, err := newTimingEqualiser(cost)
	if err != nil {
		t.Fatalf("newTimingEqualiser: %v", err)
	}

	got, err := bcrypt.Cost(eq.decoy)
	if err != nil {
		t.Fatalf("reading the decoy cost: %v", err)
	}
	if got != cost {
		t.Errorf("decoy hashed at cost %d, want %d — the unknown-user path would be faster", got, cost)
	}

	// And it must not panic or match anything.
	eq.equalise("whatever the attacker sent")
}
