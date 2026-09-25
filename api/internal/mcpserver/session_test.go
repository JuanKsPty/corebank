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
		ToolListAccounts, ToolListMovements, ToolListCategories, ToolSpending, ToolCashFlow,
		ToolExplainDrift, ToolTransferSuggest, ToolPortfolio, ToolListRules,
		ToolProposeCategory, ToolProposeNote, ToolProposeRule, ToolProposeTransfer, ToolProposeCheckpoint,
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
	if len(specs) != 14 {
		t.Errorf("the server publishes %d tools, want 14 — a new one needs a test here", len(specs))
	}
}

func TestNoToolMovesMoney(t *testing.T) {
	// Every movement comes from a statement or a brokerage sync. A tool whose
	// verb — the first word of its name — says it moves money means that rule
	// was broken, whatever the tool does. Listing transfer suggestions or
	// proposing a transfer decision reads or proposes; it moves nothing.
	specs, err := openOffline(t).Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	verbs := map[string]bool{"deposit": true, "withdraw": true, "withdrawal": true, "transfer": true,
		"pay": true, "payment": true, "send": true, "move": true}
	for _, spec := range specs {
		verb, _, _ := strings.Cut(strings.ToLower(spec.Name), "_")
		if verbs[verb] {
			t.Errorf("tool %s looks like it moves money (%q)", spec.Name, verb)
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
