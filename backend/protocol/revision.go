package protocol

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound identifies a missing draft, immutable revision or report.
	ErrNotFound = errors.New("protocol revision not found")
	// ErrRevisionConflict prevents stale editors from overwriting newer state.
	ErrRevisionConflict = errors.New("protocol revision changed; reload before saving or activating")
)

// Draft is editable JSON, separate from the active immutable revision.
type Draft struct {
	ProtocolID string    `json:"protocolId"`
	Hash       string    `json:"hash"`
	Definition Value     `json:"definition"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Revision is a content-addressed compiled definition retained for rollback.
type Revision struct {
	ProtocolID string    `json:"protocolId"`
	Hash       string    `json:"hash"`
	Definition Value     `json:"definition"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Activation selects one immutable revision for future requests.
type Activation struct {
	ProtocolID   string    `json:"protocolId"`
	RevisionHash string    `json:"revisionHash"`
	Generation   int64     `json:"generation"`
	ActivatedAt  time.Time `json:"activatedAt"`
}

// Repository persists protocol state. Draft and activation updates use
// compare-and-swap hashes; empty expected hashes mean the row must not exist.
type Repository interface {
	SaveProtocolDraft(context.Context, Draft, string) (Draft, error)
	ReadProtocolDraft(context.Context, string) (Draft, error)
	ListProtocolDrafts(context.Context) ([]Draft, error)
	SaveProtocolRevision(context.Context, Revision) error
	ReadProtocolRevision(context.Context, string, string) (Revision, error)
	ListProtocolRevisions(context.Context, string) ([]Revision, error)
	SaveProtocolReport(context.Context, string, string, VerificationReport) error
	ReadProtocolReport(context.Context, string, string) (VerificationReport, error)
	ListProtocolActivations(context.Context) ([]Activation, error)
	SetProtocolActivation(context.Context, string, string, string) (Activation, error)
}
