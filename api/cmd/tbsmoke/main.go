// Command tbsmoke validates the accounting model against a live TigerBeetle
// replica before the rest of the system is built on top of it.
//
// It is a scaffolding tool, not part of the shipped application: it proves that
// the double-entry layout, the overdraft invariant, two-phase transfers and
// idempotent retries all behave as the design assumes. Run it with TigerBeetle
// up (`docker compose up -d`), then:
//
//	go run ./cmd/tbsmoke
package main

import (
	"fmt"
	"math/big"
	"os"
	"strings"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

const (
	ledgerUSD = 840 // ISO 4217 numeric code for USD

	codeSavings  = 1
	codeChecking = 2
	codeWorld    = 999

	codeDeposit  = 1
	codeWithdraw = 2
	codeTransfer = 3
	codeOpening  = 10

	// worldAccountID is the bank's equity counterparty. Double-entry has no
	// single-sided postings: a deposit has to be debited from somewhere, so
	// customer funds enter and leave the ledger through this account.
	worldAccountID = 1
)

var failures []string

func main() {
	addr := os.Getenv("TB_ADDRESS")
	if addr == "" {
		addr = "127.0.0.1:3001"
	}

	client, err := tb.NewClient(tb.ToUint128(0), []string{addr})
	if err != nil {
		fatal("connect to TigerBeetle at %s: %v", addr, err)
	}
	defer client.Close()

	section("0. account id derivation from account_number")
	checkAccountNumberDerivation()

	section("1. create accounts")
	// Fresh customer accounts per run so the script is safely re-runnable.
	alice, bob := tb.ID(), tb.ID()
	createAccounts(client, alice, bob)

	section("2. opening balances (world -> customer)")
	// Alice starts with the real fixture balance for 4001-6588-5247-0001.
	mustTransfer(client, "opening alice $32,354.53", tb.Transfer{
		ID: tb.ID(), DebitAccountID: worldID(), CreditAccountID: alice,
		Amount: cents(3_235_453), Ledger: ledgerUSD, Code: codeOpening,
	})
	mustTransfer(client, "opening bob $1,000.00", tb.Transfer{
		ID: tb.ID(), DebitAccountID: worldID(), CreditAccountID: bob,
		Amount: cents(100_000), Ledger: ledgerUSD, Code: codeOpening,
	})
	expectBalance(client, "alice", alice, 3_235_453)
	expectBalance(client, "bob", bob, 100_000)

	section("3. deposit and withdrawal")
	mustTransfer(client, "deposit alice $100.00", tb.Transfer{
		ID: tb.ID(), DebitAccountID: worldID(), CreditAccountID: alice,
		Amount: cents(10_000), Ledger: ledgerUSD, Code: codeDeposit,
	})
	expectBalance(client, "alice after deposit", alice, 3_245_453)

	mustTransfer(client, "withdraw alice $50.00", tb.Transfer{
		ID: tb.ID(), DebitAccountID: alice, CreditAccountID: worldID(),
		Amount: cents(5_000), Ledger: ledgerUSD, Code: codeWithdraw,
	})
	expectBalance(client, "alice after withdrawal", alice, 3_240_453)

	section("4. the overdraft invariant (this is the headline claim)")
	// No `if balance < amount` in application code: the ledger itself refuses.
	expectStatus(client, "withdraw $1,000,000 with $32k on hand",
		tb.Transfer{
			ID: tb.ID(), DebitAccountID: alice, CreditAccountID: worldID(),
			Amount: cents(100_000_000), Ledger: ledgerUSD, Code: codeWithdraw,
		}, tb.TransferExceedsCredits)
	expectBalance(client, "alice unchanged after rejected withdrawal", alice, 3_240_453)

	section("5. transfer between customers")
	mustTransfer(client, "alice -> bob $100.00", tb.Transfer{
		ID: tb.ID(), DebitAccountID: alice, CreditAccountID: bob,
		Amount: cents(10_000), Ledger: ledgerUSD, Code: codeTransfer,
	})
	expectBalance(client, "alice", alice, 3_230_453)
	expectBalance(client, "bob", bob, 110_000)

	section("6. two-phase transfer, POSTED (the AI confirmation flow)")
	pendingPost := tb.ID()
	mustTransfer(client, "pending alice -> bob $200.00", tb.Transfer{
		ID: pendingPost, DebitAccountID: alice, CreditAccountID: bob,
		Amount: cents(20_000), Ledger: ledgerUSD, Code: codeTransfer,
		Timeout: 120,
		Flags:   tb.TransferFlags{Pending: true}.ToUint16(),
	})
	// The funds are reserved: available balance already reflects the hold, so a
	// second spend of the same money cannot slip through while the user decides.
	expectBalance(client, "alice with $200 reserved", alice, 3_210_453)
	expectPending(client, "alice debits_pending", alice, 20_000)
	expectBalance(client, "bob not yet credited", bob, 110_000)

	mustTransfer(client, "post the pending transfer", tb.Transfer{
		ID: tb.ID(), PendingID: pendingPost,
		Amount: cents(20_000), Ledger: ledgerUSD, Code: codeTransfer,
		Flags: tb.TransferFlags{PostPendingTransfer: true}.ToUint16(),
	})
	expectBalance(client, "alice after post", alice, 3_210_453)
	expectPending(client, "alice has no hold left", alice, 0)
	expectBalance(client, "bob after post", bob, 130_000)

	section("7. two-phase transfer, VOIDED (user taps Cancel)")
	pendingVoid := tb.ID()
	mustTransfer(client, "pending alice -> bob $500.00", tb.Transfer{
		ID: pendingVoid, DebitAccountID: alice, CreditAccountID: bob,
		Amount: cents(50_000), Ledger: ledgerUSD, Code: codeTransfer,
		Timeout: 120,
		Flags:   tb.TransferFlags{Pending: true}.ToUint16(),
	})
	expectBalance(client, "alice with $500 reserved", alice, 3_160_453)
	mustTransfer(client, "void the pending transfer", tb.Transfer{
		ID: tb.ID(), PendingID: pendingVoid,
		Amount: cents(50_000), Ledger: ledgerUSD, Code: codeTransfer,
		Flags: tb.TransferFlags{VoidPendingTransfer: true}.ToUint16(),
	})
	expectBalance(client, "alice fully restored after void", alice, 3_210_453)
	expectPending(client, "no hold remains", alice, 0)
	expectBalance(client, "bob untouched by the void", bob, 130_000)

	section("8. idempotency: replaying a transfer id")
	replay := tb.Transfer{
		ID: tb.ID(), DebitAccountID: alice, CreditAccountID: bob,
		Amount: cents(1_500), Ledger: ledgerUSD, Code: codeTransfer,
	}
	mustTransfer(client, "first submission", replay)
	expectBalance(client, "bob credited once", bob, 131_500)
	// A deterministic transfer id makes a crash-retry safe: the ledger reports
	// TransferExists instead of moving the money twice.
	expectStatus(client, "same id submitted again", replay, tb.TransferExists)
	expectBalance(client, "bob still credited only once", bob, 131_500)

	section("9. rejected edge cases")
	expectStatus(client, "transfer to self", tb.Transfer{
		ID: tb.ID(), DebitAccountID: alice, CreditAccountID: alice,
		Amount: cents(100), Ledger: ledgerUSD, Code: codeTransfer,
	}, tb.TransferAccountsMustBeDifferent)

	expectStatus(client, "destination account does not exist", tb.Transfer{
		ID: tb.ID(), DebitAccountID: alice, CreditAccountID: tb.ToUint128(999_999_999),
		Amount: cents(100), Ledger: ledgerUSD, Code: codeTransfer,
	}, tb.TransferCreditAccountNotFound)

	section("10. account history query")
	transfers, err := client.GetAccountTransfers(tb.AccountFilter{
		AccountID: alice,
		Limit:     100,
		Flags:     tb.AccountFilterFlags{Debits: true, Credits: true}.ToUint32(),
	})
	if err != nil {
		fail("GetAccountTransfers: %v", err)
	} else {
		fmt.Printf("  alice has %d transfers on record\n", len(transfers))
		if len(transfers) == 0 {
			fail("expected alice to have transfers")
		}
	}

	report()
}

// --- helpers ---------------------------------------------------------------

func worldID() tb.Uint128 { return tb.ToUint128(worldAccountID) }

func cents(n uint64) tb.Uint128 { return tb.ToUint128(n) }

// checkAccountNumberDerivation proves a real account_number maps to a u128 id
// deterministically, which is what makes re-seeding idempotent.
func checkAccountNumberDerivation() {
	const number = "4001-6588-5247-0001"
	digits := strings.ReplaceAll(number, "-", "")
	var n uint64
	if _, err := fmt.Sscanf(digits, "%d", &n); err != nil {
		fail("parse %q: %v", digits, err)
		return
	}
	fmt.Printf("  %s -> %d (fits in uint64: %t, collides with world id: %t)\n",
		number, n, n <= 1<<63, n == worldAccountID)
	if n != 4001658852470001 {
		fail("unexpected derivation: got %d", n)
	}
}

func createAccounts(client tb.Client, alice, bob tb.Uint128) {
	customer := tb.AccountFlags{DebitsMustNotExceedCredits: true, History: true}.ToUint16()

	accounts := []tb.Account{
		// The world account carries no balance-limiting flag: it must be free
		// to run an unbounded debit balance as money flows into the bank.
		{ID: worldID(), Ledger: ledgerUSD, Code: codeWorld,
			Flags: tb.AccountFlags{History: true}.ToUint16()},
		{ID: alice, Ledger: ledgerUSD, Code: codeSavings, Flags: customer},
		{ID: bob, Ledger: ledgerUSD, Code: codeChecking, Flags: customer},
	}

	results, err := client.CreateAccounts(accounts)
	if err != nil {
		fatal("CreateAccounts: %v", err)
	}
	if len(results) != len(accounts) {
		fail("expected %d results, got %d", len(accounts), len(results))
	}
	for i, r := range results {
		switch r.Status {
		case tb.AccountCreated:
			fmt.Printf("  account %d created\n", i)
		case tb.AccountExists:
			// Expected for the world account on a second run.
			fmt.Printf("  account %d already existed (idempotent)\n", i)
		default:
			fail("account %d: %s", i, r.Status)
		}
	}
}

func mustTransfer(client tb.Client, label string, t tb.Transfer) {
	expectStatus(client, label, t, tb.TransferCreated)
}

func expectStatus(client tb.Client, label string, t tb.Transfer, want tb.CreateTransferStatus) {
	results, err := client.CreateTransfers([]tb.Transfer{t})
	if err != nil {
		fail("%s: %v", label, err)
		return
	}
	if len(results) != 1 {
		fail("%s: expected 1 result, got %d", label, len(results))
		return
	}
	got := results[0].Status
	if got != want {
		fail("%s: expected %s, got %s", label, want, got)
		return
	}
	fmt.Printf("  OK  %-46s -> %s\n", label, got)
}

// available mirrors the balance the application shows a customer: posted
// credits minus posted debits minus anything currently on hold.
func available(a tb.Account) *big.Int {
	v := new(big.Int).Sub(a.CreditsPosted.BigInt(), a.DebitsPosted.BigInt())
	return v.Sub(v, a.DebitsPending.BigInt())
}

func lookup(client tb.Client, id tb.Uint128) (tb.Account, bool) {
	accounts, err := client.LookupAccounts([]tb.Uint128{id})
	if err != nil {
		fail("LookupAccounts: %v", err)
		return tb.Account{}, false
	}
	if len(accounts) != 1 {
		fail("LookupAccounts: expected 1 account, got %d", len(accounts))
		return tb.Account{}, false
	}
	return accounts[0], true
}

func expectBalance(client tb.Client, label string, id tb.Uint128, wantCents int64) {
	a, ok := lookup(client, id)
	if !ok {
		return
	}
	got := available(a)
	want := big.NewInt(wantCents)
	if got.Cmp(want) != 0 {
		fail("%s: expected %s, got %s", label, money(want), money(got))
		return
	}
	fmt.Printf("  OK  %-46s = %s\n", label, money(got))
}

func expectPending(client tb.Client, label string, id tb.Uint128, wantCents int64) {
	a, ok := lookup(client, id)
	if !ok {
		return
	}
	got := a.DebitsPending.BigInt()
	want := big.NewInt(wantCents)
	if got.Cmp(want) != 0 {
		fail("%s: expected %s on hold, got %s", label, money(want), money(got))
		return
	}
	fmt.Printf("  OK  %-46s = %s on hold\n", label, money(got))
}

func money(cents *big.Int) string {
	q, r := new(big.Int).QuoRem(cents, big.NewInt(100), new(big.Int))
	if r.Sign() < 0 {
		r.Neg(r)
	}
	return fmt.Sprintf("$%s.%02d", q.String(), r.Int64())
}

func section(title string) { fmt.Printf("\n== %s ==\n", title) }

func fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	failures = append(failures, msg)
	fmt.Printf("  FAIL %s\n", msg)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}

func report() {
	fmt.Println()
	if len(failures) == 0 {
		fmt.Println("ALL CHECKS PASSED — the accounting model behaves as designed.")
		return
	}
	fmt.Printf("%d CHECK(S) FAILED:\n", len(failures))
	for _, f := range failures {
		fmt.Printf("  - %s\n", f)
	}
	os.Exit(1)
}
