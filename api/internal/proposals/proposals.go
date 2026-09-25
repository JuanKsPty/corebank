// Package proposals holds the changes the assistant proposes and the owner
// decides.
//
// The assistant has no tool that writes. Each of its propose_* tools checks the
// change against the owner's own data, stores it here with a summary written
// by this package — never by the model, so the card cannot say one thing and
// do another — and the chat shows it. Only the owner's "Aplicar" makes the
// change, and the payload is checked again then, since the data may have
// moved in between.
//
// Nothing a proposal can do changes a balance: a category, a note, a rule, a
// transfer match or a stated balance, which only checks the imported
// movements against the bank.
package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/civil"
	"github.com/JuanKsPty/corebank/api/internal/money"
	"github.com/JuanKsPty/corebank/api/internal/movements"
	"github.com/JuanKsPty/corebank/api/internal/rules"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/transfers"
)

// TTL is how long a proposal waits for the owner.
const TTL = 24 * time.Hour

// Kinds of proposal.
const (
	KindRecategorize     = "recategorize"
	KindNote             = "note"
	KindRule             = "rule"
	KindTransferDecision = "transfer_decision"
	KindCheckpoint       = "checkpoint"
)

var (
	// ErrNotFound means no such proposal belongs to the caller.
	ErrNotFound = errors.New("proposals: proposal not found")
	// ErrDecided means the proposal was already applied, rejected or expired.
	ErrDecided = errors.New("proposals: proposal already decided or expired")
	// ErrInvalid means the proposed change does not make sense; the message
	// says why, in Spanish, for the model to correct itself.
	ErrInvalid = errors.New("proposals: invalid proposal")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Recategorize files a movement under a category, or marks it as a transfer
// between the owner's accounts (or not).
type Recategorize struct {
	EntryID    uuid.UUID  `json:"entry_id"`
	CategoryID *uuid.UUID `json:"category_id,omitempty"`
	Transfer   *bool      `json:"transfer,omitempty"`
}

// Note sets a movement's note.
type Note struct {
	EntryID uuid.UUID `json:"entry_id"`
	Note    string    `json:"note"`
}

// Rule creates a category rule.
type Rule struct {
	MatchText       string     `json:"match_text"`
	CategoryID      *uuid.UUID `json:"category_id,omitempty"`
	Transfer        bool       `json:"transfer"`
	ApplyToExisting bool       `json:"apply_to_existing"`
}

// TransferDecision confirms or rejects a suggested transfer pair.
type TransferDecision struct {
	OutEntryID uuid.UUID `json:"out_entry_id"`
	InEntryID  uuid.UUID `json:"in_entry_id"`
	Confirm    bool      `json:"confirm"`
}

// Checkpoint states an account's balance at the close of a day.
type Checkpoint struct {
	AccountID uuid.UUID  `json:"account_id"`
	AsOf      civil.Date `json:"as_of"`
	// BalanceCents is signed from the owner's view: a card owing 14.30 is -1430.
	BalanceCents int64  `json:"balance_cents"`
	Note         string `json:"note,omitempty"`
}

// Service stores, applies and rejects proposals.
type Service struct {
	db         *store.DB
	accounts   *accounts.Service
	categories *categories.Service
	movements  *movements.Service
	rules      *rules.Service
	transfers  *transfers.Service
	now        func() time.Time
}

func NewService(db *store.DB, accts *accounts.Service, cats *categories.Service, moves *movements.Service,
	rls *rules.Service, xfers *transfers.Service) *Service {
	return &Service{db: db, accounts: accts, categories: cats, movements: moves, rules: rls, transfers: xfers, now: time.Now}
}

// Propose checks a change and stores it as pending. payload is one of this
// package's payload types.
func (s *Service) Propose(ctx context.Context, userID uuid.UUID, payload any) (store.Proposal, error) {
	kind, summary, err := s.check(ctx, userID, payload)
	if err != nil {
		return store.Proposal{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return store.Proposal{}, err
	}
	return s.db.Q().CreateProposal(ctx, store.Proposal{
		ID: uuid.New(), UserID: userID, Kind: kind, Payload: raw,
		Summary: truncate(summary, 500), ExpiresAt: s.now().Add(TTL),
	})
}

// Pending lists the proposals still waiting for the owner.
func (s *Service) Pending(ctx context.Context, userID uuid.UUID) ([]store.Proposal, error) {
	return s.db.Q().PendingProposals(ctx, userID)
}

// Reject discards a pending proposal.
func (s *Service) Reject(ctx context.Context, userID, id uuid.UUID) (store.Proposal, error) {
	p, err := s.db.Q().DecideProposal(ctx, userID, id, "rejected")
	if err != nil {
		return store.Proposal{}, s.whyNotPending(ctx, userID, id, err)
	}
	return p, nil
}

// Apply makes a pending proposal's change.
//
// The proposal is claimed first, with a conditional update, so two clicks
// apply it once; if the change then fails it goes back to pending.
func (s *Service) Apply(ctx context.Context, userID, id uuid.UUID) (store.Proposal, error) {
	p, err := s.db.Q().DecideProposal(ctx, userID, id, "applied")
	if err != nil {
		return store.Proposal{}, s.whyNotPending(ctx, userID, id, err)
	}
	if err := s.apply(ctx, userID, p); err != nil {
		if reopen := s.db.Q().ReopenProposal(ctx, userID, id); reopen != nil {
			return store.Proposal{}, errors.Join(err, reopen)
		}
		return store.Proposal{}, err
	}
	return p, nil
}

func (s *Service) whyNotPending(ctx context.Context, userID, id uuid.UUID, err error) error {
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if _, err := s.db.Q().ProposalByID(ctx, userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return ErrDecided
}

func decode(p store.Proposal) (any, error) {
	var v any
	switch p.Kind {
	case KindRecategorize:
		v = &Recategorize{}
	case KindNote:
		v = &Note{}
	case KindRule:
		v = &Rule{}
	case KindTransferDecision:
		v = &TransferDecision{}
	case KindCheckpoint:
		v = &Checkpoint{}
	default:
		return nil, fmt.Errorf("proposals: unknown kind %q", p.Kind)
	}
	if err := json.Unmarshal(p.Payload, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Service) apply(ctx context.Context, userID uuid.UUID, p store.Proposal) error {
	payload, err := decode(p)
	if err != nil {
		return err
	}
	// Checked again: the movement may have been recategorized by hand, the
	// category deleted, or the pair decided since the assistant proposed it.
	if _, _, err := s.check(ctx, userID, payload); err != nil {
		return err
	}
	switch v := payload.(type) {
	case *Recategorize:
		if v.CategoryID != nil {
			if _, err := s.movements.SetCategory(ctx, userID, v.EntryID, v.CategoryID, "assistant"); err != nil {
				return err
			}
		}
		if v.Transfer != nil {
			if _, err := s.movements.SetTransfer(ctx, userID, v.EntryID, *v.Transfer); err != nil {
				return err
			}
		}
		return nil
	case *Note:
		_, err := s.movements.SetNote(ctx, userID, v.EntryID, v.Note)
		return err
	case *Rule:
		_, _, err := s.rules.Create(ctx, userID, rules.Input{
			MatchText: v.MatchText, CategoryID: v.CategoryID, Transfer: v.Transfer, ApplyToExisting: v.ApplyToExisting,
		})
		return err
	case *TransferDecision:
		return s.transfers.Decide(ctx, userID, v.OutEntryID, v.InEntryID, v.Confirm, "assistant")
	case *Checkpoint:
		_, err := s.accounts.AddCheckpoint(ctx, userID, v.AccountID, accounts.CheckpointInput{
			AsOf: v.AsOf, Balance: money.Cents(v.BalanceCents), Note: v.Note,
		})
		return err
	}
	return fmt.Errorf("proposals: unhandled payload %T", payload)
}

// check validates a payload against the owner's data and writes its summary.
func (s *Service) check(ctx context.Context, userID uuid.UUID, payload any) (string, string, error) {
	switch v := payload.(type) {
	case Recategorize:
		return s.check(ctx, userID, &v)
	case Note:
		return s.check(ctx, userID, &v)
	case Rule:
		return s.check(ctx, userID, &v)
	case TransferDecision:
		return s.check(ctx, userID, &v)
	case Checkpoint:
		return s.check(ctx, userID, &v)

	case *Recategorize:
		e, err := s.entry(ctx, userID, v.EntryID)
		if err != nil {
			return "", "", err
		}
		if v.CategoryID == nil && v.Transfer == nil {
			return "", "", invalid("indica una categoría o si es una transferencia")
		}
		if e.CategorySource != nil && *e.CategorySource == "user" && v.CategoryID != nil {
			return "", "", invalid("la persona eligió la categoría de este movimiento a mano; no la cambies")
		}
		var parts []string
		if v.CategoryID != nil {
			name, err := s.categoryName(ctx, userID, *v.CategoryID)
			if err != nil {
				return "", "", err
			}
			parts = append(parts, "categoría "+name)
		}
		if v.Transfer != nil {
			if *v.Transfer {
				parts = append(parts, "transferencia entre tus cuentas")
			} else {
				parts = append(parts, "no es una transferencia")
			}
		}
		return KindRecategorize, fmt.Sprintf("%s: %s", describe(e), strings.Join(parts, ", ")), nil

	case *Note:
		e, err := s.entry(ctx, userID, v.EntryID)
		if err != nil {
			return "", "", err
		}
		v.Note = strings.TrimSpace(v.Note)
		if utf8.RuneCountInString(v.Note) > 500 {
			return "", "", invalid("la nota admite como máximo 500 caracteres")
		}
		if v.Note == "" {
			return KindNote, fmt.Sprintf("%s: quitar la nota", describe(e)), nil
		}
		return KindNote, fmt.Sprintf("%s: nota «%s»", describe(e), v.Note), nil

	case *Rule:
		v.MatchText = strings.TrimSpace(v.MatchText)
		if v.MatchText == "" || utf8.RuneCountInString(v.MatchText) > 60 || (v.CategoryID == nil && !v.Transfer) {
			return "", "", invalid("la regla necesita un texto de hasta 60 caracteres y una categoría o marcarse como transferencia")
		}
		target := "transferencia entre tus cuentas"
		if v.CategoryID != nil {
			name, err := s.categoryName(ctx, userID, *v.CategoryID)
			if err != nil {
				return "", "", err
			}
			target = name
			if v.Transfer {
				target += " y transferencia"
			}
		}
		summary := fmt.Sprintf("Regla: lo que contenga «%s» va a %s", v.MatchText, target)
		if v.ApplyToExisting {
			summary += ", también en los movimientos que ya tienes"
		}
		return KindRule, summary, nil

	case *TransferDecision:
		out, err := s.entry(ctx, userID, v.OutEntryID)
		if err != nil {
			return "", "", err
		}
		in, err := s.entry(ctx, userID, v.InEntryID)
		if err != nil {
			return "", "", err
		}
		if out.AccountID == in.AccountID || out.Amount >= 0 || out.Amount != -in.Amount {
			return "", "", invalid("esos dos movimientos no pueden ser una transferencia: deben ser de cuentas distintas y de montos opuestos, el primero negativo")
		}
		verb := "Confirmar"
		if !v.Confirm {
			verb = "Descartar"
		}
		return KindTransferDecision, fmt.Sprintf("%s como transferencia: %s → %s", verb, describe(out), describe(in)), nil

	case *Checkpoint:
		a, err := s.accounts.Get(ctx, userID, v.AccountID)
		if err != nil {
			return "", "", err
		}
		if v.AsOf.IsZero() || v.AsOf.After(civil.Of(s.now())) {
			return "", "", invalid("la fecha del saldo debe ser un día que ya pasó")
		}
		name := a.Alias
		if name == "" {
			name = a.DisplayName
		}
		balance := money.Cents(v.BalanceCents)
		if a.Class == "liability" {
			return KindCheckpoint, fmt.Sprintf("%s: adeudado %s al cierre del %s", name, (-balance).Display(), v.AsOf), nil
		}
		return KindCheckpoint, fmt.Sprintf("%s: saldo %s al cierre del %s", name, balance.Display(), v.AsOf), nil
	}
	return "", "", fmt.Errorf("proposals: unknown payload %T", payload)
}

func (s *Service) entry(ctx context.Context, userID, id uuid.UUID) (store.Entry, error) {
	e, err := s.movements.Get(ctx, userID, id)
	if errors.Is(err, movements.ErrNotFound) {
		return store.Entry{}, invalid("el movimiento %s no existe: usa un id de list_movements", id)
	}
	return e, err
}

func (s *Service) categoryName(ctx context.Context, userID, id uuid.UUID) (string, error) {
	c, err := s.categories.Get(ctx, userID, id)
	if err != nil {
		if errors.Is(err, categories.ErrNotFound) || errors.Is(err, categories.ErrNotOwned) {
			return "", invalid("la categoría %s no existe: usa un id de list_categories", id)
		}
		return "", err
	}
	return c.Name, nil
}

func describe(e store.Entry) string {
	return fmt.Sprintf("%s %s %s", e.BookedOn, truncate(e.Description, 60), e.Amount.Display())
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
