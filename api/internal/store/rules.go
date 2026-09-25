package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CategoryRule files a matching movement under a category, marks it as a
// transfer between the owner's own accounts, or both.
type CategoryRule struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	MatchText  string
	CategoryID *uuid.UUID
	// SetKind, when "transfer", marks matching movements as transfers.
	SetKind   *string
	Priority  int
	CreatedAt time.Time
}

// CreateCategoryRuleIfNew adds a rule unless the user already has one for the
// same text, in which case nothing changes.
func (q *Queries) CreateCategoryRuleIfNew(ctx context.Context, r CategoryRule) (bool, error) {
	tag, err := q.q.Exec(ctx, `
		INSERT INTO category_rules (id, user_id, match_text, category_id, set_kind, priority)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, lower(btrim(match_text))) DO NOTHING`,
		r.ID, r.UserID, r.MatchText, r.CategoryID, r.SetKind, r.Priority)
	if err != nil {
		return false, wrap("store.CreateCategoryRuleIfNew", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CategoryRulesByUser lists a user's rules in the order they are tried:
// highest priority first, then the longest (most specific) text, then oldest.
func (q *Queries) CategoryRulesByUser(ctx context.Context, userID uuid.UUID) ([]CategoryRule, error) {
	rows, err := q.q.Query(ctx, `
		SELECT id, user_id, match_text, category_id, set_kind, priority, created_at
		FROM category_rules WHERE user_id = $1
		ORDER BY priority DESC, char_length(btrim(match_text)) DESC, created_at, id`, userID)
	if err != nil {
		return nil, wrap("store.CategoryRulesByUser", err)
	}
	defer rows.Close()
	var out []CategoryRule
	for rows.Next() {
		var r CategoryRule
		if err := rows.Scan(&r.ID, &r.UserID, &r.MatchText, &r.CategoryID, &r.SetKind, &r.Priority, &r.CreatedAt); err != nil {
			return nil, wrap("store.CategoryRulesByUser", err)
		}
		out = append(out, r)
	}
	return out, wrap("store.CategoryRulesByUser", rows.Err())
}

// UpdateCategoryRule changes one of the user's rules.
func (q *Queries) UpdateCategoryRule(ctx context.Context, r CategoryRule) error {
	return q.execOne(ctx, "store.UpdateCategoryRule", `
		UPDATE category_rules SET match_text = $3, category_id = $4, set_kind = $5, priority = $6
		WHERE id = $1 AND user_id = $2`, r.ID, r.UserID, r.MatchText, r.CategoryID, r.SetKind, r.Priority)
}

// DeleteCategoryRule removes one of the user's rules.
func (q *Queries) DeleteCategoryRule(ctx context.Context, userID, id uuid.UUID) error {
	return q.execOne(ctx, "store.DeleteCategoryRule",
		`DELETE FROM category_rules WHERE id = $1 AND user_id = $2`, id, userID)
}

// ApplyCategoryRule files the user's already-imported movements that match a
// rule. A category the owner or the assistant chose is never overwritten, and
// a kind the owner set is never changed.
func (q *Queries) ApplyCategoryRule(ctx context.Context, r CategoryRule) (int64, error) {
	pattern := "%" + escapeLike(strings.ToLower(strings.TrimSpace(r.MatchText))) + "%"
	var total int64
	if r.CategoryID != nil {
		tag, err := q.q.Exec(ctx, `
			UPDATE entries SET category_id = $3, category_source = 'rule'
			WHERE user_id = $1 AND lower(bank_category || ' ' || description) LIKE $2
			  AND (category_source IS NULL OR category_source = 'rule')`, r.UserID, pattern, *r.CategoryID)
		if err != nil {
			return 0, wrap("store.ApplyCategoryRule", err)
		}
		total += tag.RowsAffected()
	}
	if r.SetKind != nil {
		tag, err := q.q.Exec(ctx, `
			UPDATE entries SET user_kind = $3::entry_kind
			WHERE user_id = $1 AND lower(bank_category || ' ' || description) LIKE $2
			  AND user_kind IS NULL AND kind <> $3::entry_kind AND kind <> 'trade'`, r.UserID, pattern, *r.SetKind)
		if err != nil {
			return 0, wrap("store.ApplyCategoryRule", err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
