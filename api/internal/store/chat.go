package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ChatMessage is one persisted turn of a conversation.
//
// Content is JSONB rather than text because a turn is not always prose: an
// assistant turn carries the tools it asked for, and a tool turn carries their
// results. Storing the whole turn means a reload restores the conversation the
// model actually had, not a transcript of it — so the assistant does not forget a
// reservation it just made because the customer refreshed the page.
type ChatMessage struct {
	ID        int64
	UserID    uuid.UUID
	Role      string
	Content   json.RawMessage
	CreatedAt time.Time
}

// AppendChatMessage records a turn.
func (q *Queries) AppendChatMessage(ctx context.Context, userID uuid.UUID, role string, content json.RawMessage) (ChatMessage, error) {
	const query = `
		INSERT INTO chat_messages (user_id, role, content)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`

	m := ChatMessage{UserID: userID, Role: role, Content: content}
	if err := q.q.QueryRow(ctx, query, userID, role, content).Scan(&m.ID, &m.CreatedAt); err != nil {
		return ChatMessage{}, wrap("store.AppendChatMessage", err)
	}
	return m, nil
}

// ChatHistory returns a user's most recent turns in chronological order.
//
// Bounded by design: a conversation is replayed into the model's context on every
// message, and an unbounded history would eventually cost more than it helps and
// then fail outright. The newest turns are the ones that matter, so the limit is
// applied from the end and the result reversed.
func (q *Queries) ChatHistory(ctx context.Context, userID uuid.UUID, limit int) ([]ChatMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}

	const query = `
		SELECT id, user_id, role, content, created_at
		FROM chat_messages
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT $2`

	rows, err := q.q.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, wrap("store.ChatHistory", err)
	}
	defer rows.Close()

	var messages []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.UserID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, wrap("store.ChatHistory", err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("store.ChatHistory", err)
	}

	// Back into chronological order, which is what a conversation needs to be
	// replayed in.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// ClearChatHistory deletes a user's conversation, for the interface's "start over".
func (q *Queries) ClearChatHistory(ctx context.Context, userID uuid.UUID) error {
	const query = `DELETE FROM chat_messages WHERE user_id = $1`

	_, err := q.q.Exec(ctx, query, userID)
	return wrap("store.ClearChatHistory", err)
}
