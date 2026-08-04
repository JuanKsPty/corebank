package seeder

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func quietSeeder() *Seeder {
	return &Seeder{
		bcryptCost: bcrypt.MinCost,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestDuplicateEmailsBecomeDistinctUsers(t *testing.T) {
	// Twenty addresses appear twice in the dataset, each time for a different
	// person. Merging them would leave the second person's accounts owned by the
	// first, so both users must survive with distinct addresses.
	raw := []datasetUser{
		{ID: uuid.New(), Email: "ana@example.com", Password: "Secreta2026", FullName: "Ana Uno"},
		{ID: uuid.New(), Email: "Ana@Example.com", Password: "Secreta2026", FullName: "Ana Dos"},
		{ID: uuid.New(), Email: "ana@example.com", Password: "Secreta2026", FullName: "Ana Tres"},
		{ID: uuid.New(), Email: "bruno@example.com", Password: "Secreta2026", FullName: "Bruno"},
	}

	users, rewritten, _, err := quietSeeder().prepareUsers(raw)
	if err != nil {
		t.Fatalf("prepareUsers: %v", err)
	}

	if len(users) != len(raw) {
		t.Fatalf("got %d users from %d rows; none may be dropped", len(users), len(raw))
	}

	seen := map[string]bool{}
	for _, u := range users {
		if seen[u.Email] {
			t.Errorf("%q appears twice, so the unique index would reject the import", u.Email)
		}
		seen[u.Email] = true
	}

	// The comparison is case-insensitive, so the second "Ana@Example.com" counts
	// as a collision even though the text differs.
	want := []string{"ana@example.com", "ana+dup2@example.com", "ana+dup3@example.com", "bruno@example.com"}
	for i, w := range want {
		if users[i].Email != w {
			t.Errorf("user %d has e-mail %q, want %q", i, users[i].Email, w)
		}
	}
	if len(rewritten) != 2 {
		t.Errorf("reported %d rewritten addresses, want 2: %v", len(rewritten), rewritten)
	}
}

func TestIdenticalPasswordsAreHashedOnce(t *testing.T) {
	// The optimisation that takes the import from a minute to seconds. It is
	// confined to fixture data on purpose, and this test states what it does:
	// one hash per distinct password, reused across users.
	raw := []datasetUser{
		{ID: uuid.New(), Email: "a@example.com", Password: "Isabel2024!"},
		{ID: uuid.New(), Email: "b@example.com", Password: "Isabel2024!"},
		{ID: uuid.New(), Email: "c@example.com", Password: "Miguel2024!"},
	}

	users, _, distinct, err := quietSeeder().prepareUsers(raw)
	if err != nil {
		t.Fatalf("prepareUsers: %v", err)
	}
	if distinct != 2 {
		t.Errorf("computed %d hashes for 2 distinct passwords", distinct)
	}
	if users[0].PasswordHash != users[1].PasswordHash {
		t.Error("the shared password produced two different hashes, so it was not reused")
	}
	if users[0].PasswordHash == users[2].PasswordHash {
		t.Error("two different passwords produced the same hash")
	}
	// And the reused hash must still verify, or the fixture credentials in the
	// README would not work.
	if err := bcrypt.CompareHashAndPassword([]byte(users[1].PasswordHash), []byte("Isabel2024!")); err != nil {
		t.Errorf("the reused hash does not verify: %v", err)
	}
}

func TestOpeningBalanceIsParsedFromTheFilesDigits(t *testing.T) {
	// 8.87 is the value that exposes the naive conversion: float64(8.87)*100
	// truncates to 886 cents. The dataset is full of such values, so the opening
	// transfers have to come from the text.
	owner := uuid.New()
	users := []store.User{{ID: owner}}

	accounts, openings, err := prepareAccounts([]datasetAccount{
		{Number: "4001-6588-5247-0001", UserID: owner, InitialBalance: json.Number("32354.53"),
			Currency: "USD", AccountType: "savings"},
		{Number: "4001-0000-0000-0002", UserID: owner, InitialBalance: json.Number("8.87"),
			Currency: "USD", AccountType: "checking"},
	}, users)
	if err != nil {
		t.Fatalf("prepareAccounts: %v", err)
	}

	for i, want := range []int64{3235453, 887} {
		if got := int64(openings[i].Amount); got != want {
			t.Errorf("opening %d funded with %d cents, want %d", i, got, want)
		}
		if openings[i].From != ledger.WorldAccountID {
			t.Errorf("opening %d is not debited from the equity account", i)
		}
		if openings[i].Kind != ledger.MovementOpening {
			t.Errorf("opening %d has kind %s, want opening", i, openings[i].Kind)
		}
	}
	if accounts[0].LedgerID != 4001658852470001 {
		t.Errorf("ledger id = %d, want 4001658852470001", accounts[0].LedgerID)
	}
}

func TestSeedIDsAreDeterministic(t *testing.T) {
	// Re-running the seeder has to produce the same ids, because that is what
	// makes both PostgreSQL's primary keys and the ledger's transfer ids reject a
	// second import instead of duplicating it.
	tx := datasetTransaction{
		From: "4001-0000-0000-0001", To: "EXTERNAL", Amount: json.Number("10.00"),
		Type: "withdrawal", Timestamp: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),
	}

	if accountRowID("4001-0000-0000-0001") != accountRowID("4001-0000-0000-0001") {
		t.Error("accountRowID is not deterministic")
	}
	if openingTransferID("4001-0000-0000-0001") == accountRowID("4001-0000-0000-0001") {
		t.Error("an account's row id and its opening transfer id collide")
	}
	if movementID(7, tx) != movementID(7, tx) {
		t.Error("movementID is not deterministic")
	}
	if movementID(7, tx) == movementID(8, tx) {
		t.Error("two identical movements at different positions share an id")
	}
}

func TestPreparationRejectsInconsistentData(t *testing.T) {
	owner := uuid.New()
	users := []store.User{{ID: owner}}
	goodAccount := datasetAccount{Number: "4001-0000-0000-0001", UserID: owner,
		InitialBalance: json.Number("100.00"), Currency: "USD", AccountType: "savings"}

	t.Run("account owned by an unknown user", func(t *testing.T) {
		bad := goodAccount
		bad.UserID = uuid.New()
		if _, _, err := prepareAccounts([]datasetAccount{bad}, users); err == nil {
			t.Error("an orphan account was accepted")
		}
	})

	t.Run("unknown account type", func(t *testing.T) {
		bad := goodAccount
		bad.AccountType = "crypto"
		if _, _, err := prepareAccounts([]datasetAccount{bad}, users); err == nil {
			t.Error("an unknown account type was accepted")
		}
	})

	t.Run("unsupported currency", func(t *testing.T) {
		bad := goodAccount
		bad.Currency = "EUR"
		if _, _, err := prepareAccounts([]datasetAccount{bad}, users); err == nil {
			t.Error("a non-USD account was accepted")
		}
	})

	accounts, _, err := prepareAccounts([]datasetAccount{goodAccount}, users)
	if err != nil {
		t.Fatalf("prepareAccounts: %v", err)
	}
	good := datasetTransaction{From: goodAccount.Number, To: "EXTERNAL",
		Amount: json.Number("10.00"), Type: "withdrawal", Status: "completed"}

	for name, mutate := range map[string]func(datasetTransaction) datasetTransaction{
		"unknown account": func(t datasetTransaction) datasetTransaction {
			t.From = "4001-9999-9999-9999"
			return t
		},
		"same account on both sides": func(t datasetTransaction) datasetTransaction {
			t.To = t.From
			return t
		},
		"zero amount": func(t datasetTransaction) datasetTransaction {
			t.Amount = json.Number("0")
			return t
		},
		"unknown kind": func(t datasetTransaction) datasetTransaction {
			t.Type = "chargeback"
			return t
		},
		"unexpected status": func(t datasetTransaction) datasetTransaction {
			t.Status = "pending"
			return t
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepareMovements([]datasetTransaction{mutate(good)}, accounts); err == nil {
				t.Error("the inconsistent movement was accepted")
			}
		})
	}
}

func TestLoadDatasetRejectsUnknownFields(t *testing.T) {
	// Importing a dataset that gained a column while silently ignoring it would
	// produce a bank that quietly disagrees with its own source data.
	path := t.TempDir() + "/extra.json"
	writeFile(t, path, `{"users":[{"id":"`+uuid.NewString()+`","email":"a@b.co",
		"password":"Secreta2026","full_name":"A","created_at":"2024-01-01T00:00:00Z",
		"loyalty_tier":"gold"}],"accounts":[],"transactions":[]}`)

	_, err := loadDataset(path)
	if err == nil {
		t.Fatal("an unknown field was accepted")
	}
	if !strings.Contains(err.Error(), "loyalty_tier") {
		t.Errorf("the error does not name the offending field: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
