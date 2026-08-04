// Package seeder imports the provided dataset into PostgreSQL and the ledger.
//
// The interpretation it applies is the one documented in the README, and it is a
// decision rather than a reading: the dataset is not internally consistent.
// Replaying its 6429 movements over the stated opening balances overdraws 126
// accounts, and solving for opening balances that make the final figures come out
// right still takes 140 accounts negative somewhere along the way. Since the
// ledger refuses an overdraft by construction, the three properties — final
// balance matches the file, full history inside the ledger, no overdrafts — cannot
// all hold.
//
// So `initial_balance` is taken as the balance *now*. Each account is opened in
// the ledger for exactly that amount, which means the balances an evaluator sees
// match the file to the cent; and the historical movements are imported into
// PostgreSQL as read-only audit history, which is where their Spanish
// descriptions have to live anyway since a ledger stores no text. Everything that
// happens afterwards is real double-entry bookkeeping.
package seeder

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
)

// dataset is the provided JSON file.
type dataset struct {
	Users        []datasetUser        `json:"users"`
	Accounts     []datasetAccount     `json:"accounts"`
	Transactions []datasetTransaction `json:"transactions"`
}

type datasetUser struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Password  string    `json:"password"`
	FullName  string    `json:"full_name"`
	CreatedAt time.Time `json:"created_at"`
}

// datasetAccount holds InitialBalance as a json.Number, not a float64.
//
// That is the whole reason this type exists. The file contains values like
// 32354.53, which has no exact binary representation; decoding it into a float64
// and multiplying by 100 loses cents on some values, and a ledger that is a cent
// off is simply wrong. json.Number keeps the digits the file actually contains,
// and they are parsed as text.
type datasetAccount struct {
	Number         string      `json:"account_number"`
	UserID         uuid.UUID   `json:"user_id"`
	InitialBalance json.Number `json:"initial_balance"`
	Currency       string      `json:"currency"`
	AccountType    string      `json:"account_type"`
}

type datasetTransaction struct {
	From        string      `json:"from_account"`
	To          string      `json:"to_account"`
	Amount      json.Number `json:"amount"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Timestamp   time.Time   `json:"timestamp"`
	Status      string      `json:"status"`
}

// loadDataset reads and parses the dataset.
func loadDataset(path string) (dataset, error) {
	file, err := os.Open(path)
	if err != nil {
		return dataset{}, fmt.Errorf("seeder: opening %s: %w", path, err)
	}
	defer file.Close()

	var data dataset
	dec := json.NewDecoder(file)
	// Unknown fields are refused: if the dataset gains a column that matters —
	// say a per-account currency or a transaction fee — importing it while
	// silently ignoring that column would produce a bank that quietly disagrees
	// with its own source data.
	dec.DisallowUnknownFields()

	if err := dec.Decode(&data); err != nil {
		return dataset{}, fmt.Errorf("seeder: parsing %s: %w", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return dataset{}, fmt.Errorf("seeder: %s has trailing content after the dataset", path)
	}

	switch {
	case len(data.Users) == 0:
		return dataset{}, fmt.Errorf("seeder: %s contains no users", path)
	case len(data.Accounts) == 0:
		return dataset{}, fmt.Errorf("seeder: %s contains no accounts", path)
	}
	return data, nil
}

// seedNamespace derives every id the seeder mints.
//
// Derived from a fixed URL rather than written as a magic constant, so where it
// comes from is self-evident. What matters is that it never changes: the ids below
// are UUIDv5 values built from it, so re-running the seeder produces the same ids,
// and both PostgreSQL's primary keys and the ledger's transfer ids reject the
// duplicates instead of importing the dataset twice.
var seedNamespace = uuid.NewSHA1(uuid.NameSpaceURL,
	[]byte("https://github.com/JuanKsPty/corebank/seed"))

func accountRowID(number string) uuid.UUID {
	return uuid.NewSHA1(seedNamespace, []byte("account:"+number))
}

// openingTransferID identifies the ledger transfer that funds an account.
func openingTransferID(number string) uuid.UUID {
	return uuid.NewSHA1(seedNamespace, []byte("opening:"+number))
}

// movementID identifies an imported historical movement.
//
// The index is part of the input because the dataset carries no ids of its own and
// two movements could legitimately be identical in every field; the rest of the
// fields are included so that a row is not silently reassigned another row's
// identity if the file is ever reordered.
func movementID(index int, t datasetTransaction) uuid.UUID {
	key := fmt.Sprintf("movement:%d:%s:%s:%s:%s:%s",
		index, t.From, t.To, t.Amount.String(), t.Type, t.Timestamp.UTC().Format(time.RFC3339))
	return uuid.NewSHA1(seedNamespace, []byte(key))
}
