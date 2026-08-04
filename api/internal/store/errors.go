package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrNotFound means the row does not exist. Every lookup in this package
	// translates pgx.ErrNoRows into this, so callers never have to know which
	// driver is underneath.
	ErrNotFound = errors.New("store: not found")

	// ErrConflict means a unique constraint rejected the write.
	ErrConflict = errors.New("store: conflict")
)

// ConstraintError names the constraint that rejected a write, so a caller can
// tell "that e-mail is taken" from "that account number is taken" without
// pattern-matching on a driver message.
type ConstraintError struct {
	Constraint string
	cause      error
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("store: constraint %s violated: %v", e.Constraint, e.cause)
}

func (e *ConstraintError) Unwrap() error { return e.cause }

// Is makes errors.Is(err, ErrConflict) true for any constraint violation, so
// callers that do not care which constraint fired can still handle it in one
// place.
func (e *ConstraintError) Is(target error) bool { return target == ErrConflict }

// wrap normalises a pgx error into this package's vocabulary.
//
// Doing it in one function is what keeps PostgreSQL's error codes from leaking
// into the service layer: a constraint name is a schema detail, and a service
// deciding what to tell the user should not have to know SQLSTATE 23505.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return &ConstraintError{Constraint: pgErr.ConstraintName, cause: fmt.Errorf("%s: %w", op, err)}
		case "23503": // foreign_key_violation
			return &ConstraintError{Constraint: pgErr.ConstraintName, cause: fmt.Errorf("%s: %w", op, err)}
		case "23514": // check_violation
			return &ConstraintError{Constraint: pgErr.ConstraintName, cause: fmt.Errorf("%s: %w", op, err)}
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// IsConstraint reports whether err was the named constraint being violated.
func IsConstraint(err error, name string) bool {
	var ce *ConstraintError
	return errors.As(err, &ce) && ce.Constraint == name
}
