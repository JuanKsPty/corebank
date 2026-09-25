package store

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
)

// CategoryTotal is one category's total over a period. CategoryID is nil for
// movements with no category.
type CategoryTotal struct {
	CategoryID *uuid.UUID
	Total      money.Cents
	Count      int
}

// TotalsByCategory sums the user's movements of the given kinds per category,
// between from and to inclusive (zero dates are unbounded). The sum is signed
// as stored: spending comes back negative, a refund reduces it.
func (q *Queries) TotalsByCategory(ctx context.Context, f EntryFilter) ([]CategoryTotal, error) {
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	rows, err := q.q.Query(ctx, `
		SELECT e.category_id, COALESCE(SUM(e.amount_cents), 0), count(*)
		FROM entries e WHERE `+where+`
		GROUP BY e.category_id ORDER BY SUM(e.amount_cents)`, args...)
	if err != nil {
		return nil, wrap("store.TotalsByCategory", err)
	}
	defer rows.Close()
	var out []CategoryTotal
	for rows.Next() {
		var t CategoryTotal
		var total int64
		if err := rows.Scan(&t.CategoryID, &total, &t.Count); err != nil {
			return nil, wrap("store.TotalsByCategory", err)
		}
		t.Total = money.Cents(total)
		out = append(out, t)
	}
	return out, wrap("store.TotalsByCategory", rows.Err())
}

// FlowPoint is money in and out over one period bucket.
type FlowPoint struct {
	Day civil.Date
	In  money.Cents
	Out money.Cents
}

// Flow totals income and spending per day or per month. Transfers and trades
// are never in either; a refund reduces spending.
func (q *Queries) Flow(ctx context.Context, userID uuid.UUID, accountIDs []uuid.UUID, from, to civil.Date, monthly bool) ([]FlowPoint, error) {
	f := EntryFilter{UserID: userID, AccountIDs: accountIDs, From: from, To: to,
		Kinds: append(append([]string{}, IncomeKinds...), SpendKinds...)}
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	bucket := "e.booked_on"
	if monthly {
		bucket = "date_trunc('month', e.booked_on)::date"
	}
	args = append(args, IncomeKinds)
	rows, err := q.q.Query(ctx, `
		SELECT `+bucket+` AS day,
		       COALESCE(SUM(e.amount_cents) FILTER (WHERE COALESCE(e.user_kind, e.kind)::text = ANY($`+strconv.Itoa(len(args))+`)), 0),
		       COALESCE(-SUM(e.amount_cents) FILTER (WHERE NOT (COALESCE(e.user_kind, e.kind)::text = ANY($`+strconv.Itoa(len(args))+`))), 0)
		FROM entries e WHERE `+where+`
		GROUP BY day ORDER BY day`, args...)
	if err != nil {
		return nil, wrap("store.Flow", err)
	}
	defer rows.Close()
	var out []FlowPoint
	for rows.Next() {
		var p FlowPoint
		var in, outflow int64
		var day time.Time
		if err := rows.Scan(&day, &in, &outflow); err != nil {
			return nil, wrap("store.Flow", err)
		}
		p.Day, p.In, p.Out = civil.Of(day.UTC()), money.Cents(in), money.Cents(outflow)
		out = append(out, p)
	}
	return out, wrap("store.Flow", rows.Err())
}
