package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Category is a row of the categories table.
//
// Kind is kept as a plain string here rather than a domain type: unlike an
// account, which has a lower-level ledger.AccountKind this package already
// depends on, a category's kind has no meaning below the categories package
// itself, so validating and typing it lives there instead of here.
type Category struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// ParentID is nil for a top-level category.
	ParentID  *uuid.UUID
	Name      string
	Kind      string
	Color     string
	Icon      string
	IsSystem  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Constraint names a caller may need to distinguish.
//
// Two names cover "this name is already taken at this level": the table
// constraint catches two children of the same parent, and the partial index
// catches two top-level categories, which the constraint alone cannot (see
// 00004_categories.sql for why).
const (
	CategoriesNameConstraint         = "categories_user_id_parent_id_name_key"
	CategoriesTopLevelNameConstraint = "categories_user_id_name_top_level_idx"
)

// CreateCategory inserts a category.
func (q *Queries) CreateCategory(ctx context.Context, c Category) (Category, error) {
	const query = `
		INSERT INTO categories (id, user_id, parent_id, name, kind, color, icon, is_system)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at`

	err := q.q.QueryRow(ctx, query,
		c.ID, c.UserID, c.ParentID, c.Name, c.Kind, nullIfEmpty(c.Color), nullIfEmpty(c.Icon), c.IsSystem,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Category{}, wrap("store.CreateCategory", err)
	}
	return c, nil
}

// CategoriesByUser lists every category a user has, parents before their own
// children within the same kind, so a caller can build the tree in one pass.
func (q *Queries) CategoriesByUser(ctx context.Context, userID uuid.UUID) ([]Category, error) {
	const query = `
		SELECT id, user_id, parent_id, name, kind, COALESCE(color, ''), COALESCE(icon, ''),
		       is_system, created_at, updated_at
		FROM categories
		WHERE user_id = $1
		ORDER BY kind, (parent_id IS NOT NULL), name`

	rows, err := q.q.Query(ctx, query, userID)
	if err != nil {
		return nil, wrap("store.CategoriesByUser", err)
	}
	defer rows.Close()

	categories, err := scanCategories(rows)
	if err != nil {
		return nil, wrap("store.CategoriesByUser", err)
	}
	return categories, nil
}

// CategoryByID looks up a single category regardless of owner; the service
// layer checks ownership, the same split accounts.Resolve relies on.
func (q *Queries) CategoryByID(ctx context.Context, id uuid.UUID) (Category, error) {
	const query = `
		SELECT id, user_id, parent_id, name, kind, COALESCE(color, ''), COALESCE(icon, ''),
		       is_system, created_at, updated_at
		FROM categories WHERE id = $1`

	c, err := scanCategory(q.q.QueryRow(ctx, query, id))
	if err != nil {
		return Category{}, wrap("store.CategoryByID", err)
	}
	return c, nil
}

// RenameCategory updates a category's name and color/icon.
func (q *Queries) RenameCategory(ctx context.Context, id uuid.UUID, name, color, icon string) error {
	const query = `
		UPDATE categories
		SET name = $2, color = $3, icon = $4, updated_at = now()
		WHERE id = $1`

	tag, err := q.q.Exec(ctx, query, id, name, nullIfEmpty(color), nullIfEmpty(icon))
	if err != nil {
		return wrap("store.RenameCategory", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("store.RenameCategory", pgx.ErrNoRows)
	}
	return nil
}

// DeleteCategory removes a category. Any transaction that pointed at it, and
// any child category, falls back to uncategorised/top-level automatically —
// see the ON DELETE SET NULL clauses in 00004_categories.sql.
func (q *Queries) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	const query = `DELETE FROM categories WHERE id = $1`

	tag, err := q.q.Exec(ctx, query, id)
	if err != nil {
		return wrap("store.DeleteCategory", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("store.DeleteCategory", pgx.ErrNoRows)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func scanCategory(s scanner) (Category, error) {
	var c Category
	if err := s.Scan(&c.ID, &c.UserID, &c.ParentID, &c.Name, &c.Kind, &c.Color, &c.Icon,
		&c.IsSystem, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Category{}, err
	}
	return c, nil
}

func scanCategories(rows pgx.Rows) ([]Category, error) {
	var categories []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}
