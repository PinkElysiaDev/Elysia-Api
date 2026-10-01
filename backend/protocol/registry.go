package protocol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type registrySnapshot struct{ entries map[string]*Compiled }

// Service is shared by the editor, Agent and gateway. Administrative mutations
// are serialized; request reads pin a compiled pointer without locking or SQL.
type Service struct {
	compiler   *Compiler
	repository Repository
	mu         sync.Mutex
	snapshot   atomic.Pointer[registrySnapshot]
}

// NewService creates an empty registry. Reload validates persisted activations
// before they can serve requests; callers can still repair drafts on failure.
func NewService(compiler *Compiler, repository Repository) (*Service, error) {
	if compiler == nil || repository == nil {
		return nil, fmt.Errorf("protocol compiler and repository are required")
	}
	service := &Service{compiler: compiler, repository: repository}
	service.snapshot.Store(&registrySnapshot{entries: map[string]*Compiled{}})
	return service, nil
}

// Pin returns the immutable active revision for the duration of a request.
func (service *Service) Pin(id string) (*Compiled, bool) {
	compiled, exists := service.snapshot.Load().entries[id]
	return compiled, exists
}

// Schema exposes the installed compiler contract used by every authoring path.
func (service *Service) Schema() SchemaCatalog { return service.compiler.Schema() }

// Validate compiles an unsaved definition without changing drafts or activation.
func (service *Service) Validate(raw []byte) (*Compiled, []ConversionIssue) {
	return service.compiler.Compile(raw)
}

// SaveDraft saves bounded JSON even if compilation fails, allowing incomplete
// authoring. It never changes the active revision or manufactures verification.
func (service *Service) SaveDraft(ctx context.Context, id string, raw []byte, expectedHash string) (Draft, []ConversionIssue, error) {
	if !definitionIdentifier.MatchString(id) {
		return Draft{}, nil, draftInputError("/id", fmt.Errorf("invalid protocol ID"))
	}
	value, err := ParseValue(raw)
	if err != nil {
		return Draft{}, nil, draftInputError("/", err)
	}
	if err := checkValueLimits(value, service.compiler.limits); err != nil {
		return Draft{}, nil, draftInputError("/", err)
	}
	object, err := value.ReadObject()
	if err != nil {
		return Draft{}, nil, draftInputError("/", err)
	}
	var documentID string
	if err := object["id"].Decode(&documentID); err != nil || documentID != id {
		return Draft{}, nil, draftInputError("/id", fmt.Errorf("definition ID must match draft ID"))
	}
	canonical, err := EncodeValue(object)
	if err != nil {
		return Draft{}, nil, err
	}
	draft := Draft{ProtocolID: id, Hash: hashValue(canonical), Definition: value, UpdatedAt: time.Now().UTC()}
	draft, err = service.repository.SaveProtocolDraft(ctx, draft, expectedHash)
	if err != nil {
		return Draft{}, nil, err
	}
	_, issues := service.compiler.Compile(raw)
	return draft, issues, nil
}

func draftInputError(path string, err error) error {
	return IssuesError([]ConversionIssue{{Code: InvalidInput, Severity: SeverityError, Stage: "draft", Path: path, Reason: err.Error(), Suggestion: "Correct the draft JSON and retry with its current hash."}})
}

// ReadDraft returns editable content independently of the active pointer.
func (service *Service) ReadDraft(ctx context.Context, id string) (Draft, error) {
	return service.repository.ReadProtocolDraft(ctx, id)
}

// ListDrafts enumerates authored protocols without implicitly enabling them.
func (service *Service) ListDrafts(ctx context.Context) ([]Draft, error) {
	return service.repository.ListProtocolDrafts(ctx)
}

// VerifyDraft creates immutable evidence for exactly the selected draft hash.
// Concurrent edits cannot change the definition being verified.
func (service *Service) VerifyDraft(ctx context.Context, id, expectedDraftHash string) (Revision, VerificationReport, error) {
	draft, err := service.repository.ReadProtocolDraft(ctx, id)
	if err != nil {
		return Revision{}, VerificationReport{}, err
	}
	if draft.Hash != expectedDraftHash {
		return Revision{}, VerificationReport{}, ErrRevisionConflict
	}
	compiled, issues := service.compiler.Compile(draft.Definition.Bytes())
	if err := IssuesError(issues); err != nil {
		return Revision{}, VerificationReport{}, err
	}
	revision := Revision{ProtocolID: id, Hash: compiled.hash, Definition: compiled.definition, CreatedAt: time.Now().UTC()}
	report := Verify(ctx, compiled)
	if err := ctx.Err(); err != nil {
		return Revision{}, VerificationReport{}, err
	}
	// The report binds its immutable revision even if the editable draft moves.
	// Activation always names the chosen revision rather than "latest".
	if err := service.repository.SaveProtocolRevision(ctx, revision); err != nil {
		return Revision{}, VerificationReport{}, err
	}
	if err := service.repository.SaveProtocolReport(ctx, id, revision.Hash, report); err != nil {
		return Revision{}, VerificationReport{}, err
	}
	return revision, report, nil
}

// Revisions lists retained definitions for comparison and explicit rollback.
func (service *Service) Revisions(ctx context.Context, id string) ([]Revision, error) {
	return service.repository.ListProtocolRevisions(ctx, id)
}

// Activate compiles and verifies evidence before atomically publishing a
// revision. Requests that already pinned the old pointer remain unaffected.
func (service *Service) Activate(ctx context.Context, id, hash, expectedActive string) (Activation, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	compiled, err := service.loadVerifiedRevision(ctx, id, hash)
	if err != nil {
		return Activation{}, err
	}
	activation, err := service.repository.SetProtocolActivation(ctx, id, hash, expectedActive)
	if err != nil {
		return Activation{}, err
	}
	current := service.snapshot.Load()
	entries := make(map[string]*Compiled, len(current.entries)+1)
	for key, entry := range current.entries {
		entries[key] = entry
	}
	entries[id] = compiled
	service.snapshot.Store(&registrySnapshot{entries: entries})
	return activation, nil
}

// Rollback applies the same compiler and evidence checks as activation.
func (service *Service) Rollback(ctx context.Context, id, hash, expectedActive string) (Activation, error) {
	return service.Activate(ctx, id, hash, expectedActive)
}

// Reload publishes all persisted active revisions in one atomic snapshot.
// Failure leaves the previous snapshot intact and returns repair diagnostics.
func (service *Service) Reload(ctx context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	activations, err := service.repository.ListProtocolActivations(ctx)
	if err != nil {
		return err
	}
	entries := make(map[string]*Compiled, len(activations))
	for _, active := range activations {
		compiled, err := service.loadVerifiedRevision(ctx, active.ProtocolID, active.RevisionHash)
		if err != nil {
			return fmt.Errorf("reload protocol %s: %w", active.ProtocolID, err)
		}
		entries[active.ProtocolID] = compiled
	}
	service.snapshot.Store(&registrySnapshot{entries: entries})
	return nil
}

func (service *Service) loadVerifiedRevision(ctx context.Context, id, hash string) (*Compiled, error) {
	revision, err := service.repository.ReadProtocolRevision(ctx, id, hash)
	if err != nil {
		return nil, err
	}
	compiled, issues := service.compiler.Compile(revision.Definition.Bytes())
	if err := IssuesError(issues); err != nil {
		return nil, err
	}
	if compiled.identity.DefinitionID != id || compiled.hash != hash {
		return nil, fmt.Errorf("stored revision identity/hash mismatch")
	}
	report, err := service.repository.ReadProtocolReport(ctx, id, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, IssuesError([]ConversionIssue{verificationIssue(compiled, "", "/verification", VerificationRequired, "this revision has no offline report for the current compiler", "")})
		}
		return nil, err
	}
	if err := IssuesError(CanActivate(compiled, report)); err != nil {
		return nil, err
	}
	return compiled, nil
}
