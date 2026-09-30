package domain

import "time"

// Issue is the persisted current projection of an issue.
type Issue struct {
	ID                 string
	DisplayID          string
	SequenceNo         int64
	Type               Type
	Title              string
	Description        *string
	AcceptanceCriteria *string
	Status             Status
	Priority           Priority
	// ReadyRank is the issue's optional explicit position in the READY queue.
	// It is advisory metadata: storage preserves it regardless of status, and
	// issue listing only consults it while the issue is stored as ready. A nil
	// value means "no explicit position"; ordering then falls back to the
	// priority/claimability/sequence order every issue already has.
	ReadyRank           *int64
	ParentID            *string
	BlockedReason       *string
	Version             int64
	CreatedBySessionID  *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ClosedAt            *time.Time
	ArchivedAt          *time.Time
	ArchivedBySessionID *string
	Labels              []Label
}
