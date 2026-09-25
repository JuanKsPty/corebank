package store_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/storetest"
)

func TestMigrationsApplyToAnEmptyDatabaseAndAreIdempotent(t *testing.T) {
	db := storetest.New(t) // has already run every migration once

	// The API migrates on every boot, so a second run over an up-to-date
	// schema must be a no-op, not an error.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := store.Migrate(context.Background(), db, logger); err != nil {
		t.Fatalf("second Migrate() = %v, want nil", err)
	}
}

func newUser(t *testing.T, db *store.DB, email string) store.User {
	t.Helper()
	u, err := db.Q().CreateUser(context.Background(), store.User{
		ID: uuid.New(), Email: email, PasswordHash: "x", FullName: email,
	})
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	return u
}

func TestCategoriesAreIsolatedBetweenUsers(t *testing.T) {
	db := storetest.New(t)
	ctx := context.Background()
	alice := newUser(t, db, "alice@example.com")
	bob := newUser(t, db, "bob@example.com")

	// The same name is allowed for two users: each user's category tree is
	// their own.
	for _, owner := range []store.User{alice, bob} {
		if _, err := db.Q().CreateCategory(ctx, store.Category{
			ID: uuid.New(), UserID: owner.ID, Name: "Comida", Kind: "expense",
		}); err != nil {
			t.Fatalf("CreateCategory for %s: %v", owner.Email, err)
		}
	}

	aliceCats, err := db.Q().CategoriesByUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range aliceCats {
		if c.UserID != alice.ID {
			t.Errorf("CategoriesByUser(alice) returned %q owned by %s", c.Name, c.UserID)
		}
	}
	if len(aliceCats) != 1 {
		t.Errorf("alice has %d categories, want 1", len(aliceCats))
	}
}
