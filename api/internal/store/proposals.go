package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Proposal is a change the assistant proposed, waiting for the owner.
type Proposal struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Kind      string
	Payload   json.RawMessage
	Summary   string
	Status    string
	CreatedAt time.Time
	ExpiresAt time.Time
	DecidedAt *time.Time
}

const proposalColumns = `id, user_id, kind, payload, summary, status, created_at, expires_at, decided_at`

func scanProposal(s scanner) (Proposal, error) {
	var p Proposal
	err := s.Scan(&p.ID, &p.UserID, &p.Kind, &p.Payload, &p.Summary, &p.Status, &p.CreatedAt, &p.ExpiresAt, &p.DecidedAt)
	return p, err
}

// CreateProposal stores a pending proposal.
func (q *Queries) CreateProposal(ctx context.Context, p Proposal) (Proposal, error) {
	row := q.q.QueryRow(ctx, `
		INSERT INTO ai_proposals (id, user_id, kind, payload, summary, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+proposalColumns,
		p.ID, p.UserID, p.Kind, p.Payload, p.Summary, p.ExpiresAt)
	out, err := scanProposal(row)
	return out, wrap("store.CreateProposal", err)
}

// ProposalByID returns one of the user's proposals, in any state.
func (q *Queries) ProposalByID(ctx context.Context, userID, id uuid.UUID) (Proposal, error) {
	row := q.q.QueryRow(ctx, `SELECT `+proposalColumns+` FROM ai_proposals WHERE id = $1 AND user_id = $2`, id, userID)
	p, err := scanProposal(row)
	return p, wrap("store.ProposalByID", err)
}

// PendingProposals lists the user's proposals still waiting and not expired,
// oldest first.
func (q *Queries) PendingProposals(ctx context.Context, userID uuid.UUID) ([]Proposal, error) {
	rows, err := q.q.Query(ctx, `SELECT `+proposalColumns+` FROM ai_proposals
		WHERE user_id = $1 AND status = 'pending' AND expires_at > now()
		ORDER BY created_at LIMIT 20`, userID)
	if err != nil {
		return nil, wrap("store.PendingProposals", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, wrap("store.PendingProposals", err)
		}
		out = append(out, p)
	}
	return out, wrap("store.PendingProposals", rows.Err())
}

// DecideProposal moves a pending, unexpired proposal to status, in one
// conditional update: of two concurrent decisions only one finds it pending.
// ErrNotFound means it was not pending any more, had expired, or is not the
// user's.
func (q *Queries) DecideProposal(ctx context.Context, userID, id uuid.UUID, status string) (Proposal, error) {
	row := q.q.QueryRow(ctx, `
		UPDATE ai_proposals SET status = $3, decided_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'pending' AND expires_at > now()
		RETURNING `+proposalColumns, id, userID, status)
	p, err := scanProposal(row)
	return p, wrap("store.DecideProposal", err)
}

// ReopenProposal puts an applied proposal back to pending, when applying it
// failed after it was claimed.
func (q *Queries) ReopenProposal(ctx context.Context, userID, id uuid.UUID) error {
	_, err := q.q.Exec(ctx, `UPDATE ai_proposals SET status = 'pending', decided_at = NULL
		WHERE id = $1 AND user_id = $2 AND status = 'applied'`, id, userID)
	return wrap("store.ReopenProposal", err)
}
