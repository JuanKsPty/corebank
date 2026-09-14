// Package categories lets a customer group their own transactions for spend
// tracking.
//
// A category is metadata, nothing more: attaching one to a transaction never
// touches the ledger, in the same way an account's alias never does. That is
// what makes this package safe to build without going anywhere near
// TigerBeetle — there is no balance here to keep consistent with anything.
package categories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/store"
)

var (
	// ErrNotFound means no category has that id.
	ErrNotFound = errors.New("categories: category not found")

	// ErrNotOwned means the category exists but belongs to someone else, kept
	// distinct from ErrNotFound for the same reason accounts.ErrNotOwned is:
	// the HTTP layer answers "not found" for both so a category id cannot be
	// probed for existence, but the log keeps the real reason.
	ErrNotOwned = errors.New("categories: category belongs to another user")

	// ErrParentNotFound means the given parent id does not resolve to one of
	// the caller's own categories.
	ErrParentNotFound = errors.New("categories: parent category not found")

	// ErrParentIsChild means the given parent is itself a child category.
	// Capping the tree at two levels only works if a child can never become
	// a parent, so this is checked rather than left to produce a
	// three-level tree nothing in the UI expects.
	ErrParentIsChild = errors.New("categories: a child category cannot be used as a parent")

	// ErrKindMismatch means a category and the parent it was given belong to
	// different kinds (income vs. expense). Mixing them would make "expand
	// this expense category" show income entries alongside it.
	ErrKindMismatch = errors.New("categories: a category must share its parent's kind")

	// ErrNameTaken means the user already has a category with this name at
	// this level (same parent, or both top-level).
	ErrNameTaken = errors.New("categories: a category with this name already exists at this level")
)

// Category is a category as the rest of the application sees it: the
// storage row with its kind already parsed and validated.
type Category struct {
	ID        uuid.UUID
	ParentID  *uuid.UUID
	Name      string
	Kind      Kind
	Color     string
	Icon      string
	IsSystem  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Service manages a customer's categories.
type Service struct {
	db *store.DB
}

func NewService(db *store.DB) *Service {
	return &Service{db: db}
}

// SeedDefaults inserts the starting category set for a newly registered
// customer.
//
// It takes the queries rather than opening its own transaction, exactly like
// accounts.Open, so registration can create the user, their first account and
// their default categories atomically — a customer who exists but cannot
// categorise a single transaction is not a state worth allowing even
// momentarily.
func (s *Service) SeedDefaults(ctx context.Context, q *store.Queries, userID uuid.UUID) error {
	for _, seed := range defaultSeeds {
		_, err := q.CreateCategory(ctx, store.Category{
			ID:       uuid.New(),
			UserID:   userID,
			Name:     seed.name,
			Kind:     string(seed.kind),
			IsSystem: true,
		})
		if err != nil {
			return fmt.Errorf("categories: seeding defaults: %w", err)
		}
	}
	return nil
}

// List returns every category a user has, flat.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Category, error) {
	rows, err := s.db.Q().CategoriesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return toCategories(rows)
}

// FindByName returns a user's own top-level category matching name exactly,
// ignoring case, or ok=false if none does.
//
// For a caller that knows a category should already exist by name — a
// default seed, a bank's own category label being mapped onto one of
// them — rather than by id.
func (s *Service) FindByName(ctx context.Context, userID uuid.UUID, name string) (Category, bool, error) {
	list, err := s.List(ctx, userID)
	if err != nil {
		return Category{}, false, err
	}
	for _, c := range list {
		if strings.EqualFold(c.Name, name) {
			return c, true, nil
		}
	}
	return Category{}, false, nil
}

// Node is a category together with its own children, for rendering a tree
// without a second round trip.
type Node struct {
	Category
	Children []Category
}

// Tree groups a user's categories into parents with their children nested
// underneath. A category with an unset or dangling parent is a top-level
// node — the latter cannot happen once ON DELETE SET NULL is honoured, but
// treating it as top-level rather than dropping it is the safer failure mode
// if the two ever disagree.
func (s *Service) Tree(ctx context.Context, userID uuid.UUID) ([]Node, error) {
	flat, err := s.List(ctx, userID)
	if err != nil {
		return nil, err
	}

	byID := make(map[uuid.UUID]*Node, len(flat))
	var roots []*Node
	for _, c := range flat {
		byID[c.ID] = &Node{Category: c}
	}
	for _, c := range flat {
		if c.ParentID == nil {
			roots = append(roots, byID[c.ID])
			continue
		}
		parent, ok := byID[*c.ParentID]
		if !ok {
			roots = append(roots, byID[c.ID])
			continue
		}
		parent.Children = append(parent.Children, c)
	}

	out := make([]Node, 0, len(roots))
	for _, r := range roots {
		out = append(out, *r)
	}
	return out, nil
}

// CreateInput is a validated request to create a category.
type CreateInput struct {
	Name     string
	Kind     Kind
	ParentID *uuid.UUID
	Color    string
	Icon     string
}

// Create adds a new category for a user.
//
// A parent, when given, must already belong to the same user, be a
// top-level category itself, and share this category's kind — the three
// invariants that keep the tree exactly two levels deep and each level
// internally consistent.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in CreateInput) (Category, error) {
	name, err := normaliseName(in.Name)
	if err != nil {
		return Category{}, err
	}

	if in.ParentID != nil {
		if err := s.validateParent(ctx, userID, *in.ParentID, in.Kind); err != nil {
			return Category{}, err
		}
	}

	row, err := s.db.Q().CreateCategory(ctx, store.Category{
		ID:       uuid.New(),
		UserID:   userID,
		ParentID: in.ParentID,
		Name:     name,
		Kind:     string(in.Kind),
		Color:    in.Color,
		Icon:     in.Icon,
	})
	if err != nil {
		if store.IsConstraint(err, store.CategoriesNameConstraint) ||
			store.IsConstraint(err, store.CategoriesTopLevelNameConstraint) {
			return Category{}, fmt.Errorf("%w: %q", ErrNameTaken, name)
		}
		return Category{}, err
	}
	return toCategory(row)
}

func (s *Service) validateParent(ctx context.Context, userID uuid.UUID, parentID uuid.UUID, kind Kind) error {
	parent, err := s.resolve(ctx, userID, parentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotOwned) {
			return fmt.Errorf("%w: %s", ErrParentNotFound, parentID)
		}
		return err
	}
	if parent.ParentID != nil {
		return fmt.Errorf("%w: %s", ErrParentIsChild, parentID)
	}
	if parent.Kind != string(kind) {
		return fmt.Errorf("%w: parent is %s, category is %s", ErrKindMismatch, parent.Kind, kind)
	}
	return nil
}

// Rename changes a category's name, color and icon. Its kind and parent
// never change after creation: moving a category between kinds or levels
// would leave every transaction already filed under it describing something
// it no longer means.
func (s *Service) Rename(ctx context.Context, userID, id uuid.UUID, name, color, icon string) (Category, error) {
	cleanName, err := normaliseName(name)
	if err != nil {
		return Category{}, err
	}

	if _, err := s.resolve(ctx, userID, id); err != nil {
		return Category{}, err
	}
	if err := s.db.Q().RenameCategory(ctx, id, cleanName, color, icon); err != nil {
		if store.IsConstraint(err, store.CategoriesNameConstraint) ||
			store.IsConstraint(err, store.CategoriesTopLevelNameConstraint) {
			return Category{}, fmt.Errorf("%w: %q", ErrNameTaken, cleanName)
		}
		return Category{}, err
	}

	row, err := s.db.Q().CategoryByID(ctx, id)
	if err != nil {
		return Category{}, err
	}
	return toCategory(row)
}

// Delete removes a category. Transactions filed under it fall back to
// uncategorised and any child is promoted to top-level — both enforced by
// the ON DELETE SET NULL clauses in the schema, not by application code, so
// there is nothing here to keep in sync with the constraint.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	if _, err := s.resolve(ctx, userID, id); err != nil {
		return err
	}
	return s.db.Q().DeleteCategory(ctx, id)
}

// Get returns one category belonging to userID.
//
// Exported so a collaborator like the transactions package — which knows
// nothing about categories beyond an id — can confirm one exists and is the
// caller's own before attaching it to something.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Category, error) {
	row, err := s.resolve(ctx, userID, id)
	if err != nil {
		return Category{}, err
	}
	return toCategory(row)
}

// resolve finds a category by id and checks that userID owns it — the same
// split accounts.Resolve makes between "does not exist" and "not yours",
// with the same reason: the caller decides what to tell the customer.
func (s *Service) resolve(ctx context.Context, userID, id uuid.UUID) (store.Category, error) {
	row, err := s.db.Q().CategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Category{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return store.Category{}, err
	}
	if row.UserID != userID {
		return store.Category{}, fmt.Errorf("%w: %s", ErrNotOwned, id)
	}
	return row, nil
}

func toCategory(row store.Category) (Category, error) {
	kind, err := ParseKind(row.Kind)
	if err != nil {
		// The column has a CHECK constraint restricting it to the enum, so
		// reaching here means the schema and the code have drifted apart.
		return Category{}, err
	}
	return Category{
		ID:        row.ID,
		ParentID:  row.ParentID,
		Name:      row.Name,
		Kind:      kind,
		Color:     row.Color,
		Icon:      row.Icon,
		IsSystem:  row.IsSystem,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func toCategories(rows []store.Category) ([]Category, error) {
	out := make([]Category, 0, len(rows))
	for _, row := range rows {
		c, err := toCategory(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
