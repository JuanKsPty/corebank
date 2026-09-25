// Package movements lists and annotates the owner's movements: every line
// their statements and IBKR printed, across every account.
//
// What a movement *is* — its amount, dates and account — came from a document
// and cannot be edited here. What it *means* can: its category, a note, and
// whether it is a transfer between the owner's own accounts.
package movements

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrNotFound means no such movement belongs to the caller.
	ErrNotFound = errors.New("movements: movement not found")
	// ErrNoteTooLong means the note is longer than 500 characters.
	ErrNoteTooLong = errors.New("movements: the note is too long")
)

// Service lists and annotates movements.
type Service struct {
	db         *store.DB
	categories *categories.Service
}

func NewService(db *store.DB, cats *categories.Service) *Service {
	return &Service{db: db, categories: cats}
}

// Query selects movements. UserID comes from the caller's identity, never
// from the request.
type Query struct {
	AccountIDs    []uuid.UUID
	From, To      civil.Date
	Kinds         []string
	CategoryIDs   []uuid.UUID
	Uncategorized bool
	Search        string
	Before        *store.EntryCursor
	Limit         int
}

// Page is one page of movements, newest first.
type Page struct {
	Entries []store.Entry
	Next    *store.EntryCursor
}

// List returns one page of the user's movements.
func (s *Service) List(ctx context.Context, userID uuid.UUID, q Query) (Page, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Q().Entries(ctx, s.filter(userID, q, limit+1))
	if err != nil {
		return Page{}, err
	}
	page := Page{Entries: rows}
	if len(rows) > limit {
		page.Entries = rows[:limit]
		last := page.Entries[limit-1]
		page.Next = &store.EntryCursor{BookedOn: last.BookedOn, ID: last.ID}
	}
	return page, nil
}

// Each streams every movement matching q, oldest first, for an export.
func (s *Service) Each(ctx context.Context, userID uuid.UUID, q Query, fn func(store.Entry) error) error {
	return s.db.Q().EachEntry(ctx, s.filter(userID, q, 0), fn)
}

func (s *Service) filter(userID uuid.UUID, q Query, limit int) store.EntryFilter {
	return store.EntryFilter{
		UserID: userID, AccountIDs: q.AccountIDs, From: q.From, To: q.To, Kinds: q.Kinds,
		CategoryIDs: q.CategoryIDs, Uncategorized: q.Uncategorized, Search: q.Search,
		Before: q.Before, Limit: limit,
	}
}

// Get returns one of the user's movements.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (store.Entry, error) {
	e, err := s.db.Q().EntryByID(ctx, userID, id)
	return e, notFound(err)
}

// SetCategory files a movement under one of the user's categories, or clears
// it with nil. source records who decided: "user" or "assistant".
func (s *Service) SetCategory(ctx context.Context, userID, id uuid.UUID, categoryID *uuid.UUID, source string) (store.Entry, error) {
	if categoryID != nil {
		if _, err := s.categories.Get(ctx, userID, *categoryID); err != nil {
			return store.Entry{}, err
		}
	}
	if err := s.db.Q().SetEntryCategory(ctx, userID, id, categoryID, source); err != nil {
		return store.Entry{}, notFound(err)
	}
	return s.Get(ctx, userID, id)
}

// SetNote sets a movement's note; an empty note clears it.
func (s *Service) SetNote(ctx context.Context, userID, id uuid.UUID, note string) (store.Entry, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		return store.Entry{}, ErrNoteTooLong
	}
	if err := s.db.Q().SetEntryNote(ctx, userID, id, note); err != nil {
		return store.Entry{}, notFound(err)
	}
	return s.Get(ctx, userID, id)
}

// SetTransfer marks a movement as a transfer between the owner's own
// accounts, so it never counts as spending or income — or, with false,
// restores what the importer decided.
func (s *Service) SetTransfer(ctx context.Context, userID, id uuid.UUID, transfer bool) (store.Entry, error) {
	e, err := s.Get(ctx, userID, id)
	if err != nil {
		return store.Entry{}, err
	}
	var kind *string
	if transfer && e.Kind != store.KindTransfer {
		k := store.KindTransfer
		kind = &k
	}
	if !transfer && e.Kind == store.KindTransfer {
		// The importer said transfer; saying it is not one needs a kind of its
		// own, which the sign decides.
		k := store.KindExpense
		if e.Amount > 0 {
			k = store.KindIncome
		}
		kind = &k
	}
	if err := s.db.Q().SetEntryUserKind(ctx, userID, id, kind); err != nil {
		return store.Entry{}, notFound(err)
	}
	return s.Get(ctx, userID, id)
}

func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}

// categoryNames maps every category the user has to its display name, a child
// as "Padre › Hija".
func (s *Service) categoryNames(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]string, error) {
	tree, err := s.categories.Tree(ctx, userID)
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, n := range tree {
		names[n.ID] = n.Name
		for _, c := range n.Children {
			names[c.ID] = n.Name + " › " + c.Name
		}
	}
	return names, nil
}
