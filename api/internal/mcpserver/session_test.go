package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The tools are exercised end to end against a live PostgreSQL and ledger in
// mcpserver_integration_test.go. What is checked here needs neither: the shape of
// the contract the model sees, and the fact that identity is not part of it.

func TestNoToolAcceptsAUserIdentity(t *testing.T) {
	// This is the package's central security claim, so it is asserted rather than
	// merely documented: if a field named anything like a user, owner or customer
	// identity ever appears in a published schema, the model gains a way to name
	// somebody else and this test fails.
	session := openOffline(t)

	specs, err := session.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("the server published no tools")
	}

	forbidden := []string{"user_id", "userid", "user", "owner", "customer_id", "subject", "actor", "token"}
	for _, spec := range specs {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Fatalf("%s: unmarshalling its schema: %v", spec.Name, err)
		}
		for field := range schema.Properties {
			for _, bad := range forbidden {
				if strings.EqualFold(field, bad) {
					t.Errorf("tool %s exposes %q, which would let the model act as another user",
						spec.Name, field)
				}
			}
		}
	}
}

func TestEveryToolIsPublishedWithASchemaAndDescription(t *testing.T) {
	session := openOffline(t)

	specs, err := session.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}

	byName := make(map[string]ToolSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
	}

	for _, name := range []string{
		ToolListAccounts, ToolGetBalance, ToolListTransactions,
		ToolDeposit, ToolPrepareWithdrawal, ToolPrepareTransfer,
	} {
		spec, ok := byName[name]
		if !ok {
			t.Errorf("tool %s is missing", name)
			continue
		}
		// A tool the model cannot understand is a tool it will misuse, so an empty
		// description is a defect rather than a style issue.
		if spec.Description == "" {
			t.Errorf("tool %s has no description", name)
		}
		if len(spec.InputSchema) == 0 {
			t.Errorf("tool %s has no input schema", name)
		}
	}
	if len(specs) != 6 {
		t.Errorf("the server publishes %d tools, want 6 — a new one needs its confirmation "+
			"requirement decided here", len(specs))
	}
}

func TestOnlyMoneyLeavingToolsRequireConfirmation(t *testing.T) {
	// A deposit can only increase the customer's balance, so asking them to
	// confirm it would be friction with nothing behind it. Everything that can
	// take money out must be a proposal.
	for tool, want := range map[string]bool{
		ToolListAccounts:      false,
		ToolGetBalance:        false,
		ToolListTransactions:  false,
		ToolDeposit:           false,
		ToolPrepareWithdrawal: true,
		ToolPrepareTransfer:   true,
	} {
		if got := RequiresConfirmation(tool); got != want {
			t.Errorf("RequiresConfirmation(%s) = %v, want %v", tool, got, want)
		}
	}
}

func TestSessionRefusesAnUnauthenticatedUser(t *testing.T) {
	// The zero UUID would mean "the accounts belonging to nobody", and the tools
	// would operate on it without complaint.
	if _, err := Open(context.Background(), Deps{}, uuid.Nil); err == nil {
		t.Error("a session was opened with no user")
	}
}

func TestConfirmationIsReadFromTheStructuredResult(t *testing.T) {
	// The confirmation card must come from the tool's own output, never from the
	// model's prose: a hallucinated id would otherwise render a working button,
	// and a forgotten mention would leave funds held with nothing to click.
	holdID := uuid.New()
	structured, err := json.Marshal(confirmationResult{
		Status:             "awaiting_confirmation",
		ConfirmationID:     holdID.String(),
		Kind:               "transfer",
		Amount:             "100.00",
		FromAccount:        "4001-0000-0000-0001",
		ToAccount:          "4001-0000-0000-0002",
		BalanceIfConfirmed: "23.45",
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	got, ok := ConfirmationFrom(ToolPrepareTransfer, Result{Structured: structured})
	if !ok {
		t.Fatal("a valid confirmation result was not recognised")
	}
	if got.ID != holdID || got.Amount != "100.00" || got.BalanceIfConfirmed != "23.45" {
		t.Errorf("extracted %+v", got)
	}

	for name, arg := range map[string]struct {
		tool   string
		result Result
	}{
		"a read-only tool": {ToolListAccounts, Result{Structured: structured}},
		"a failed call":    {ToolPrepareTransfer, Result{Structured: structured, IsError: true}},
		"no structured output": {ToolPrepareTransfer,
			Result{Text: "he preparado la transferencia, id abc-123"}},
		"a malformed id": {ToolPrepareTransfer,
			Result{Structured: json.RawMessage(`{"confirmation_id":"not-a-uuid"}`)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ConfirmationFrom(arg.tool, arg.result); ok {
				t.Error("a confirmation card would have been rendered")
			}
		})
	}
}

// openOffline starts a session with no services behind it.
//
// Listing tools and reading their schemas is pure protocol, so the handlers are
// never invoked; calling one would panic on the nil services, which is exactly the
// distinction these tests want to keep.
func openOffline(t *testing.T) *Session {
	t.Helper()

	session, err := Open(context.Background(), Deps{}, uuid.New())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("closing the session: %v", err)
		}
	})
	return session
}
