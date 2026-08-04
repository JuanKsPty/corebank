// Package identity carries the authenticated user through a request.
//
// It is deliberately tiny and depends on nothing. Authentication puts the user
// here after verifying a token; every other package reads it. Keeping it
// separate from the auth package is what lets a feature ask "who is calling?"
// without importing the machinery that answers "are they who they claim to be?",
// and avoids a cycle between the two.
//
// There is no setter reachable from a request body or a tool argument. That is
// the whole security property: identity can only enter a context from code that
// has already verified it, so no caller — and in particular no language model
// choosing arguments for a tool — can name a user it is not.
package identity

import (
	"context"

	"github.com/google/uuid"
)

type userKey struct{}

// WithUser returns a context carrying an authenticated user.
func WithUser(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

// FromContext returns the authenticated user, or false if there is none.
func FromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}

// MustFromContext returns the authenticated user or panics.
//
// For code that only ever runs behind authentication, where a missing user is a
// wiring mistake. It panics rather than returning the zero UUID because the zero
// UUID would silently become "some other user" — a handler operating on nobody's
// accounts is a far worse outcome than a 500.
func MustFromContext(ctx context.Context) uuid.UUID {
	id, ok := FromContext(ctx)
	if !ok {
		panic("identity: no authenticated user in context")
	}
	return id
}
